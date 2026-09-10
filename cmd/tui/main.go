// tui is a SAP GUI-protocol terminal: it draws the screens a dispatcher sends
// the way SAP GUI lays them out — the window chrome (title bar, menu bar,
// application toolbar, status bar) around a canvas of dynpro elements or a
// classic list, in colour, with icons and framed group boxes. Two modes:
//
//	# read-only: connect, send a captured hello, draw every screen the server
//	# sends and nothing back but NI_PONG keepalives.
//	tui --addr host:port --hello captures/probe.jsonl
//
//	# live logon: same, but answer the logon screen once with credentials from
//	# a .mcp.json server, then keep drawing each screen the server sends.
//	tui --addr host:32NN --hello captures/probe.jsonl --logon --mcp .mcp.json --server a4h
//
// It never sends a keystroke or a function code of its own, and it makes at
// most one logon attempt per run: a second wrong password would count towards
// locking the user, so a logon screen that comes back is drawn, not answered.
// The password is read from the .mcp.json the user points at (or ODGP_PASSWORD
// when --user is given on the command line) and is never logged or drawn.
// Point this only at your own system.
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
	plain := flag.Bool("plain", false, "draw without colour or chrome (the old bare grid)")
	logon := flag.Bool("logon", false, "answer the logon screen once, then keep drawing the session")
	mcpPath := flag.String("mcp", "", "a .mcp.json to read credentials from, for --logon")
	server := flag.String("server", "", "which server in --mcp to use")
	user := flag.String("user", "", "logon user (password from ODGP_PASSWORD); overrides --mcp")
	client := flag.String("client", "", "logon client; with --user")
	lang := flag.String("lang", "EN", "logon language; with --user")
	compress := flag.Bool("compress", false, "LZH-compress the frames we send (default off: DIAG accepts plain)")
	render := flag.String("render", "", "offline: read this tap capture and draw every S->C screen it holds, no connection")
	demo := flag.Bool("demo", false, "offline: animate a self-contained demo through the styled TUI, no connection")
	sceneMS := flag.Int("scene-ms", 4000, "milliseconds each demo scene runs (with --demo)")
	flag.Parse()

	// Offline demo: no socket, no SAP. Animate locally through the renderer.
	if *demo {
		if err := runDemo(*sceneMS, *plain); err != nil {
			fmt.Fprintln(os.Stderr, "tui: demo:", err)
			os.Exit(1)
		}
		return
	}

	// Offline render: no socket, no SAP. Walk a capture's server frames and
	// draw each screen, stepping on Enter. The way to eyeball the renderer.
	if *render != "" {
		if err := renderCapture(*render, *plain, *once); err != nil {
			fmt.Fprintln(os.Stderr, "tui: render:", err)
			os.Exit(1)
		}
		return
	}

	if *hello == "" {
		fmt.Fprintln(os.Stderr, "tui: --hello <capture.jsonl> is required")
		os.Exit(2)
	}
	if *addr == "" {
		fmt.Fprintln(os.Stderr, "tui: --addr host:port is required")
		os.Exit(2)
	}

	var creds credentials
	if *logon {
		var err error
		creds, err = resolveCreds(*mcpPath, *server, *user, *client, *lang)
		if err != nil {
			fmt.Fprintln(os.Stderr, "tui: logon:", err)
			os.Exit(1)
		}
	}

	helloBytes, err := helloFromCapture(*hello)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tui: hello:", err)
		os.Exit(1)
	}
	template, _ := logonTemplate(*hello) // the captured client logon PAI, for --logon

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	s := &session{
		plain:    *plain,
		once:     *once,
		logon:    *logon,
		creds:    creds,
		template: template,
		compress: *compress,
	}
	if err := s.run(ctx, *addr, helloBytes); err != nil {
		fmt.Fprintln(os.Stderr, "tui:", err)
		os.Exit(1)
	}
}

// resolveCreds gathers logon credentials: from a .mcp.json server when --mcp
// is given, else from --user with the password in ODGP_PASSWORD.
func resolveCreds(mcpPath, server, user, client, lang string) (credentials, error) {
	if mcpPath != "" {
		if server == "" {
			return credentials{}, fmt.Errorf("--mcp needs --server")
		}
		c, _, err := fromMCP(mcpPath, server)
		return c, err
	}
	if user == "" {
		return credentials{}, fmt.Errorf("give --mcp/--server or --user")
	}
	pw := os.Getenv("ODGP_PASSWORD")
	if pw == "" {
		return credentials{}, fmt.Errorf("set ODGP_PASSWORD for --user %s", user)
	}
	return credentials{Client: client, User: user, Password: pw, Lang: lang}, nil
}

// session is one connection's state: the flags, the chrome carried between
// screens, and whether the one logon attempt has been made.
type session struct {
	plain    bool
	once     bool
	logon    bool
	compress bool
	creds    credentials
	template []byte
	chrome   chrome
	loggedOn bool
	drewOnce bool
}

// run connects, sends the hello once, and loops rendering screens until the
// connection closes, the context is cancelled, or (with once) the first
// screen is drawn.
func (s *session) run(parent context.Context, addr string, helloBytes []byte) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	go func() { <-ctx.Done(); conn.Close() }()

	frame, err := ni.EncodeFrame(helloBytes)
	if err != nil {
		return fmt.Errorf("encoding hello: %w", err)
	}
	if _, err := conn.Write(frame); err != nil {
		return fmt.Errorf("sending hello: %w", err)
	}
	mode := "read-only"
	if s.logon {
		mode = "live logon as " + s.creds.User
	}
	fmt.Fprintf(os.Stderr, "tui: connected to %s, hello sent (%d bytes); %s\n", addr, len(helloBytes), mode)

	if !s.once {
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
			drawn, err := s.handleFrame(conn, payload)
			if err != nil {
				fmt.Fprintf(os.Stderr, "tui: frame skipped: %v\n", err)
				continue
			}
			if drawn && s.once {
				return nil
			}
		}
	}
}

// handleFrame decodes one server frame, updates the chrome from it, answers it
// when we are logging on, and draws whatever screen it carries. It returns
// whether a screen was drawn.
func (s *session) handleFrame(conn net.Conn, payload []byte) (bool, error) {
	m, err := diag.ParseMessage(payload, false)
	if err != nil {
		return false, err
	}
	items := diag.ParseItems(m.Body)
	msgType, msg := s.chrome.update(items)

	if s.logon {
		if err := s.answerLogon(conn, items); err != nil {
			fmt.Fprintf(os.Stderr, "tui: logon step: %v\n", err)
		}
	}

	canvas, note, ok := s.chrome.frameCanvas(items)
	if !ok {
		// A handshake or status-only frame: repaint the status bar so a
		// "saving…" or an error message still shows under the last chrome.
		if s.drewOnce && msg != "" {
			s.drawStatusOnly(msgType, msg)
		}
		return false, nil
	}
	s.draw(canvas, msgType, msg, note)
	s.drewOnce = true
	return true, nil
}

// answerLogon answers the logon screen exactly once, shaping the PAI from the
// captured template and this frame's session id and counter.
func (s *session) answerLogon(conn net.Conn, items []diag.Item) error {
	if s.loggedOn || !isLogonScreen(items) {
		return nil
	}
	if s.template == nil {
		return fmt.Errorf("no captured logon PAI to shape the answer from")
	}
	out, err := buildLogonPAI(s.template, items, s.creds, counterOf(items)+1, s.compress)
	if err != nil {
		return err
	}
	if err := writeFrame(conn, out); err != nil {
		return err
	}
	s.loggedOn = true
	fmt.Fprintln(os.Stderr, "tui: logon sent")
	return nil
}

// draw renders the frame. With chrome it composes the GUI window round the
// canvas and prints it in colour; plain, it prints the bare grid.
func (s *session) draw(canvas *tui.Grid, msgType byte, msg, note string) {
	rows, cols := terminalSize()
	if s.plain {
		if rows > 1 {
			canvas = canvas.Clip(rows-1, cols)
		}
		printClear(canvas.String() + "\n\x1b[7m " + s.bareStatus(note) + " \x1b[0m")
		return
	}
	g := s.chrome.compose(canvas, msgType, msg, note+"  q quits", rows, cols)
	printClear(g.ANSI())
}

// drawStatusOnly repaints only the status bar under the current chrome, for a
// frame that carried a message but no new screen.
func (s *session) drawStatusOnly(msgType byte, msg string) {
	if s.plain {
		return
	}
	rows, cols := terminalSize()
	g := s.chrome.compose(tui.NewGrid(1, cols), msgType, msg, "q quits", rows, cols)
	printClear(g.ANSI())
}

func (s *session) bareStatus(note string) string {
	parts := []string{}
	if s.chrome.program != "" {
		parts = append(parts, "prog "+s.chrome.program)
	}
	parts = append(parts, note, "read-only  q quits")
	return strings.Join(parts, "  |  ")
}

// firstPaint tracks whether the screen has been cleared once this run.
var firstPaint = true

// printClear paints one frame without flicker or vertical jitter. It homes the
// cursor (a full \x1b[2J every frame flickers, and a trailing newline scrolls
// the viewport whenever a frame's height changes — that is the up/down jitter),
// clears each line to its end so a shorter line leaves no ghost, and clears
// everything below the last line so a shorter frame leaves no tail. No trailing
// newline is written, so the viewport never scrolls. The screen is fully
// cleared only once, on the first frame.
func printClear(s string) {
	var b strings.Builder
	if firstPaint {
		b.WriteString("\x1b[2J")
		firstPaint = false
	}
	b.WriteString("\x1b[H")
	for i, ln := range strings.Split(s, "\n") {
		if i > 0 {
			b.WriteString("\r\n")
		}
		b.WriteString(ln)
		b.WriteString("\x1b[K") // clear to end of line
	}
	b.WriteString("\x1b[J") // clear everything below the last line
	fmt.Print(b.String())
}

// writeFrame NI-frames and sends one DIAG payload.
func writeFrame(conn net.Conn, payload []byte) error {
	fr, err := ni.EncodeFrame(payload)
	if err != nil {
		return err
	}
	_, err = conn.Write(fr)
	return err
}

// helloFromCapture reads the first C->S frame of a tap capture and returns its
// bytes, to be sent verbatim as the opening hello.
func helloFromCapture(path string) ([]byte, error) {
	frames, err := clientFrames(path, 1)
	if err != nil {
		return nil, err
	}
	if len(frames) == 0 {
		return nil, fmt.Errorf("%s: no C->S frame to use as a hello", path)
	}
	return frames[0], nil
}

// logonTemplate returns the capture's second client frame — the logon PAI a
// real GUI sent (SES echo, ST_USER.26 counter, the changed field atoms, the
// metrics XML) — as the shape our own logon answer is built from.
func logonTemplate(path string) ([]byte, error) {
	frames, err := clientFrames(path, 2)
	if err != nil {
		return nil, err
	}
	if len(frames) < 2 {
		return nil, fmt.Errorf("%s: no second C->S frame for a logon template", path)
	}
	return frames[1], nil
}

// clientFrames reads up to n C->S frames (NI keepalives skipped) from a tap
// capture, decoding the hex of each.
func clientFrames(path string, n int) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	var out [][]byte
	for sc.Scan() && len(out) < n {
		var l struct {
			Dir string `json:"dir"`
			Hex string `json:"hex"`
		}
		if json.Unmarshal(sc.Bytes(), &l) != nil || l.Dir != "C->S" {
			continue
		}
		data, err := hex.DecodeString(l.Hex)
		if err != nil {
			return nil, fmt.Errorf("decoding hex: %w", err)
		}
		if len(data) == 0 {
			continue
		}
		if _, ok := diag.NIControl(data); ok {
			continue
		}
		out = append(out, data)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// mustPong is the NI_PONG keepalive answer.
func mustPong() []byte {
	frame, err := ni.EncodeFrame([]byte("NI_PONG\x00"))
	if err != nil {
		panic(err)
	}
	return frame
}

// watchQuit ends the session when q is typed on standard input. It never sends
// anything to SAP; the byte read here only cancels the local context. On end
// of input (stdin is not a terminal, e.g. the demo piped) it stops watching but
// does NOT cancel, so a non-interactive run keeps going until Ctrl-C.
func watchQuit(ctx context.Context, cancel context.CancelFunc) {
	r := bufio.NewReader(os.Stdin)
	for {
		if ctx.Err() != nil {
			return
		}
		b, err := r.ReadByte()
		if err != nil {
			return // end of input: stop watching, leave the session running
		}
		if b == 'q' || b == 'Q' {
			cancel()
			return
		}
	}
}

// terminalSize asks the controlling terminal for its size in character cells,
// returning zeros when standard output is not a terminal.
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
