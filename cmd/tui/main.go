// tui is a read-only SAP GUI-protocol terminal. It connects to a dispatcher
// over the classic NI/DIAG transport, sends one opening hello taken verbatim
// from a local capture, and then only listens: for every screen the server
// sends it decodes the DYNT_ATOM item and draws the screen on the terminal.
//
//	tui --addr host:port --hello captures/probe.jsonl          # keep drawing each screen
//	tui --addr host:port --hello captures/probe.jsonl --once   # draw the first screen and quit
//
// It is read-only in the strong sense: after the hello the only bytes it ever
// writes are NI_PONG replies to the server's NI_PING keepalives. It never
// sends a keystroke, a function code, an Enter/PAI, or any logon data, and it
// has no input path at all. A wrong-password logon against a real user locks
// the account, so v1 renders and quits and offers no way to type into SAP.
//
// The address is given at runtime and the hello comes from a local capture
// the user points at; neither is baked in. Point this only at your own system.
package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"unsafe"

	"github.com/oisee/open-rfc-go/ni"

	"github.com/oisee/open-diag-go-pro/pkg/diag"
	"github.com/oisee/open-diag-go-pro/pkg/tui"
)

func main() {
	addr := flag.String("addr", "", "dispatcher to connect to, host:port (required)")
	hello := flag.String("hello", "", "capture whose first C->S frame is sent as the opening hello (required)")
	once := flag.Bool("once", false, "draw the first screen received, then quit")
	flag.Parse()

	if *addr == "" || *hello == "" {
		fmt.Fprintln(os.Stderr, "tui: --addr host:port and --hello <capture.jsonl> are required")
		os.Exit(2)
	}

	helloBytes, err := helloFromCapture(*hello)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tui: hello:", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := run(ctx, *addr, helloBytes, *once); err != nil {
		fmt.Fprintln(os.Stderr, "tui:", err)
		os.Exit(1)
	}
}

// run connects, sends the hello once, and loops rendering screens until the
// connection closes, the context is cancelled, or (with once) the first
// screen is drawn.
func run(parent context.Context, addr string, helloBytes []byte, once bool) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	go func() { <-ctx.Done(); conn.Close() }()

	// The opening hello: the one and only unsolicited thing this program
	// sends. Everything after this is a reply to the server.
	frame, err := ni.EncodeFrame(helloBytes)
	if err != nil {
		return fmt.Errorf("encoding hello: %w", err)
	}
	if _, err := conn.Write(frame); err != nil {
		return fmt.Errorf("sending hello: %w", err)
	}
	fmt.Fprintf(os.Stderr, "tui: connected to %s, hello sent (%d bytes); read-only, listening\n", addr, len(helloBytes))

	// q on standard input quits, the same as Ctrl-C. Nothing typed here ever
	// reaches SAP; it only ends the local session.
	if !once {
		go watchQuit(ctx, cancel)
	}

	dec, err := ni.NewFrameDecoder(64 << 20)
	if err != nil {
		return err
	}
	buf := make([]byte, 64<<10)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			fmt.Fprintf(os.Stderr, "tui: connection closed: %v\n", err)
			return nil
		}
		frames, err := dec.Push(buf[:n])
		if err != nil {
			return fmt.Errorf("ni framing: %w", err)
		}
		for _, payload := range frames {
			if name, ok := diag.NIControl(payload); ok {
				if name == "NI_PING" {
					if _, err := conn.Write(mustPong()); err != nil {
						return fmt.Errorf("sending NI_PONG: %w", err)
					}
				}
				continue
			}
			drawn, err := handleFrame(payload)
			if err != nil {
				fmt.Fprintf(os.Stderr, "tui: frame skipped: %v\n", err)
				continue
			}
			if drawn && once {
				return nil
			}
		}
	}
}

// handleFrame decodes one DIAG frame and, when it carries a screen, draws it.
// It returns whether a screen was drawn.
func handleFrame(payload []byte) (bool, error) {
	m, err := diag.ParseMessage(payload, false)
	if err != nil {
		return false, err
	}
	items := diag.ParseItems(m.Body)

	var atomItem *diag.Item
	program, screen := "", ""
	for i := range items {
		it := items[i]
		if it.Type == diag.ItemAPPL4 && it.ID == 0x09 && it.SID == 0x02 {
			atomItem = &items[i]
		}
		if it.Type == diag.ItemAPPL && it.ID == 0x06 {
			switch it.SID {
			case 0x0d:
				program = trimField(it.Value)
			case 0x0e:
				screen = trimField(it.Value)
			}
		}
	}
	// A classic ABAP list arrives as positioned text runs, not as a DYNT_ATOM
	// screen; the frame still carries a small DYNT_ATOM for the list viewer's
	// dynpro shell, so the list runs take precedence when they are present.
	if diag.HasListSegments(items) {
		segs := diag.ParseListItems(items)
		grid := tui.RenderList(segs, tui.DefaultRows, tui.DefaultCols)
		draw(grid, listStatusLine(program, screen, len(items), len(segs)))
		return true, nil
	}

	if atomItem == nil {
		// A handshake or control frame with no screen; nothing to draw.
		return false, nil
	}

	atoms, aerr := diag.ParseDyntAtoms(atomItem.Value)
	// aerr means the tail of the chain was unreadable; the atoms before it
	// still draw, so a partial screen is shown rather than nothing.

	rows, cols := screenSize(items)
	grid := tui.Render(atoms, rows, cols)

	status := statusLine(program, screen, len(items), len(atoms), aerr)
	draw(grid, status)
	return true, nil
}

// screenSize is the dynpro size if a DYNN or VARINFO item makes it plain, and
// the classic 24x80 otherwise. The size fields inside those items are not
// confirmed on the captures, so this keeps the default and lets Render grow
// the grid to whatever the atoms actually need; no atom is lost either way.
func screenSize(items []diag.Item) (rows, cols int) {
	return tui.DefaultRows, tui.DefaultCols
}

// draw clears the terminal and paints the grid, clipped to the terminal size
// so a screen larger than the window loses its overflow rather than wrapping,
// with the status line last.
func draw(g *tui.Grid, status string) {
	rows, cols := terminalSize()
	if rows > 1 {
		// Leave one line for the status.
		g = g.Clip(rows-1, cols)
	} else if cols > 0 {
		g = g.Clip(0, cols)
	}
	var b strings.Builder
	b.WriteString("\x1b[2J\x1b[H") // clear screen, cursor home
	b.WriteString(g.String())
	b.WriteString("\n")
	b.WriteString("\x1b[7m") // reverse video for the status line
	b.WriteString(status)
	b.WriteString("\x1b[0m\n")
	fmt.Print(b.String())
}

// statusLine names the program and screen if the frame carried them, the item
// and atom counts, and a note when the atom chain did not fully parse.
func statusLine(program, screen string, items, atoms int, aerr error) string {
	var parts []string
	if program != "" {
		parts = append(parts, "prog "+program)
	}
	if screen != "" {
		parts = append(parts, "dynpro "+screen)
	}
	parts = append(parts, fmt.Sprintf("%d items", items), fmt.Sprintf("%d atoms", atoms))
	if aerr != nil {
		parts = append(parts, "partial")
	}
	parts = append(parts, "read-only  q quits")
	return " " + strings.Join(parts, "  |  ") + " "
}

// listStatusLine names the program and screen a list frame carried, its item
// count and the number of text runs drawn.
func listStatusLine(program, screen string, items, segs int) string {
	var parts []string
	if program != "" {
		parts = append(parts, "prog "+program)
	}
	if screen != "" {
		parts = append(parts, "dynpro "+screen)
	}
	parts = append(parts, fmt.Sprintf("%d items", items), fmt.Sprintf("%d list runs", segs))
	parts = append(parts, "read-only  q quits")
	return " " + strings.Join(parts, "  |  ") + " "
}

// helloFromCapture reads the first C->S frame of a tap capture and returns its
// bytes, to be sent verbatim as the opening hello.
func helloFromCapture(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var l struct {
			Dir string `json:"dir"`
			Hex string `json:"hex"`
		}
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		if l.Dir != "C->S" {
			continue
		}
		data, err := hex.DecodeString(l.Hex)
		if err != nil {
			return nil, fmt.Errorf("decoding hello hex: %w", err)
		}
		if len(data) == 0 {
			continue
		}
		return data, nil
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("%s: no C->S frame to use as a hello", path)
}

// mustPong is the NI_PONG keepalive answer: the one reply this program sends
// after the hello.
func mustPong() []byte {
	frame, err := ni.EncodeFrame([]byte("NI_PONG\x00"))
	if err != nil {
		// EncodeFrame only fails on an oversized payload; this one is eight
		// bytes, so this cannot happen.
		panic(err)
	}
	return frame
}

// watchQuit ends the session when q is typed on standard input. It never
// sends anything to SAP; the byte read here only cancels the local context.
func watchQuit(ctx context.Context, cancel context.CancelFunc) {
	defer cancel()
	r := bufio.NewReader(os.Stdin)
	for {
		if ctx.Err() != nil {
			return
		}
		b, err := r.ReadByte()
		if err != nil {
			return
		}
		if b == 'q' || b == 'Q' {
			return
		}
	}
}

// terminalSize asks the controlling terminal for its size in character cells.
// It returns zeros when standard output is not a terminal, in which case the
// caller does not clip.
func terminalSize() (rows, cols int) {
	type winsize struct{ Row, Col, X, Y uint16 }
	ws := &winsize{}
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		os.Stdout.Fd(),
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(ws)),
	)
	if errno != 0 {
		return 0, 0
	}
	return int(ws.Row), int(ws.Col)
}

// trimField reads a NUL/space-padded field value as a string.
func trimField(b []byte) string {
	return strings.Trim(string(b), " \x00")
}
