// server is the rogue DIAG server, in its first form: a replay. A real SAP
// GUI connects to it, and it answers with the frames a real server sent in
// a capture — the logon screen, the start menu — and then pushes screens
// of its own making at its own cadence, the way Phase 0 showed a server
// may. What the GUI checks and what it lets pass is learned here.
//
//	server -capture captures/probe.jsonl -mode logon     # answer as the capture did, from the first frame
//	server -capture captures/probe.jsonl -mode menu      # skip the logon: the first client frame gets the start menu
//	server -capture captures/probe.jsonl -mode counter   # menu, then the probe's screen and a counter pushed every 300 ms
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/oisee/open-rfc-go/ni"

	"github.com/oisee/open-diag-go-pro/pkg/diag"
	"github.com/oisee/open-diag-go-pro/pkg/frame"
	"github.com/oisee/open-diag-go-pro/pkg/replay"
)

func main() {
	listen := flag.String("listen", ":3201", "address SAP GUI connects to")
	capture := flag.String("capture", "captures/probe.jsonl", "tap capture to replay")
	conn := flag.Int("conn", 1, "connection of the capture to replay")
	mode := flag.String("mode", "logon", "logon | menu | counter")
	menuAt := flag.Int("menu-at", 2, "client frame index whose replies are the start menu (mode menu, counter)")
	screenFrame := flag.Int("screen", 209, "server frame index that shows the probe's screen (mode counter)")
	pushFrame := flag.Int("push", 222, "server frame index the pushed counter frames are made from (mode counter)")
	pushMS := flag.Int("push-ms", 300, "cadence of the pushed frames")
	flag.Parse()

	cap, err := replay.Load(*capture, *conn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "server:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "server: %d client frames, %d server frames on connection %d; mode %s; listening on %s\n", len(cap.Client), len(cap.Server), *conn, *mode, *listen)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "server:", err)
		os.Exit(1)
	}
	go func() { <-ctx.Done(); ln.Close() }()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			fmt.Fprintln(os.Stderr, "server: accept:", err)
			continue
		}
		go serve(ctx, c, cap, *mode, *menuAt, *screenFrame, *pushFrame, time.Duration(*pushMS)*time.Millisecond)
	}
}

// findCounterFrames locates the probe's screen in the capture by content
// rather than a fixed index, since a growing capture shifts the numbers:
// the first server frame whose DYNT_ATOM names GV_TICKS is the screen, the
// last is a steady counter frame to push from.

// findSelectionFrame locates a captured selection screen by content: the
// first server frame whose DYNT_ATOM has a field named P_MS. Its input
// fields are in the dynpro definition, so the client submits what the user
// types into them.
func findSelectionFrame(cap *replay.Capture) (int, bool) {
	for _, f := range cap.Server {
		m, err := diag.ParseMessage(f.Data, false)
		if err != nil {
			continue
		}
		for _, it := range diag.ParseItems(m.Body) {
			if it.Type == diag.ItemAPPL4 && it.ID == 0x09 && it.SID == 0x02 {
				if _, byName := diag.FieldIndex(it.Value); byName["P_MS"] != 0 || hasKey(byName, "P_MS") {
					return f.Index, true
				}
			}
		}
	}
	return 0, false
}

func hasKey(m map[string]int, k string) bool { _, ok := m[k]; return ok }

// inputRespond reads what the user typed into P_MS and shows it back with
// its square, reusing the real selection screen's atoms so its dynpro
// definition still matches and the client still submits. This is data in
// and data out, our Go code the PBO and PAI.
func inputRespond(cap *replay.Capture, selFrame int, client []diag.FieldValue, log func(string, ...any)) []byte {
	f, ok := cap.ServerFrame(selFrame)
	if !ok {
		return nil
	}
	m, err := diag.ParseMessage(f.Data, false)
	if err != nil {
		return nil
	}
	items := diag.ParseItems(m.Body)
	for i, it := range items {
		if it.Type != diag.ItemAPPL4 || it.ID != 0x09 || it.SID != 0x02 {
			continue
		}
		atoms, byName := diag.FieldIndex(it.Value)
		// The value the user left in P_MS: the client echoes it at the same
		// cell the server placed the field.
		typed := ""
		if idx, ok := byName["P_MS"]; ok {
			ms := atoms[idx]
			for _, fv := range client {
				if fv.Row == ms.Row && fv.Col == ms.Col {
					typed = strings.TrimSpace(fv.Value)
				}
			}
		}
		n, perr := strconv.Atoi(typed)
		diag.SetField(atoms, byName, "P_MS", typed)
		if perr == nil {
			diag.SetField(atoms, byName, "P_TICKS", strconv.Itoa(n*n))
			diag.SetField(atoms, byName, "P_BAR", fmt.Sprintf("%d squared is %d", n, n*n))
			diag.SetField(atoms, byName, "P_TIME", "ok")
		} else if typed != "" {
			diag.SetField(atoms, byName, "P_BAR", "type a whole number")
			diag.SetField(atoms, byName, "P_TIME", "?")
		}
		log("input: P_MS=%q -> %s", typed, func() string {
			if perr == nil {
				return strconv.Itoa(n * n)
			}
			return "n/a"
		}())
		items[i].Value = diag.EncodeDyntAtoms(atoms)
		h := m.Header
		h.Compress = 0
		out, err := diag.EncodeMessage(h, items, false)
		if err != nil {
			return nil
		}
		return out
	}
	return nil
}

func findCounterFrames(cap *replay.Capture) (screen, pushIdx int, ok bool) {
	first, last := -1, -1
	for _, f := range cap.Server {
		m, err := diag.ParseMessage(f.Data, false)
		if err != nil {
			continue
		}
		for _, it := range diag.ParseItems(m.Body) {
			if it.Type == diag.ItemAPPL4 && it.ID == 0x09 && it.SID == 0x02 && strings.Contains(string(it.Value), "GV_TICKS") {
				if first < 0 {
					first = f.Index
				}
				last = f.Index
			}
		}
	}
	if first < 0 {
		return 0, 0, false
	}
	return first, last, true
}

func serve(ctx context.Context, c net.Conn, cap *replay.Capture, mode string, menuAt, screenFrame, pushFrame int, cadence time.Duration) {
	defer c.Close()
	log := func(format string, a ...any) {
		fmt.Fprintf(os.Stderr, "[%s] "+format+"\n", append([]any{c.RemoteAddr()}, a...)...)
	}
	log("connected")
	send := func(what string, data []byte) error {
		plain, err := replay.Plain(data)
		if err != nil {
			log("%s: cannot flatten (%v); sending as captured", what, err)
			plain = data
		}
		frame, err := ni.EncodeFrame(plain)
		if err != nil {
			return err
		}
		_, err = c.Write(frame)
		log("-> %s, %d bytes", what, len(plain))
		return err
	}
	dec, _ := ni.NewFrameDecoder(64 << 20)
	buf := make([]byte, 64<<10)
	// group is the client frame of the capture the next reply group belongs to.
	group := 0
	if mode == "menu" || mode == "counter" {
		group = menuAt
	}
	st := &appState{}
	selFrame, selOK := 0, false
	if mode == "input" {
		selFrame, selOK = findSelectionFrame(cap)
		if selOK {
			log("selection screen located by content: server frame #%d", selFrame)
		}
	}
	if mode == "counter" || mode == "flash" || mode == "synth" || mode == "list" || mode == "app" {
		if sf, pf, ok := findCounterFrames(cap); ok {
			screenFrame, pushFrame = sf, pf
			log("counter screen located by content: screen #%d, push #%d", sf, pf)
		}
	}
	var pushing chan struct{}
	for {
		n, err := c.Read(buf)
		if err != nil {
			log("closed: %v", err)
			return
		}
		frames, err := dec.Push(buf[:n])
		if err != nil {
			log("ni: %v", err)
			return
		}
		for _, payload := range frames {
			if name, ok := diag.NIControl(payload); ok {
				log("<- %s", name)
				if name == "NI_PING" {
					_ = send("NI_PONG", []byte("NI_PONG\x00"))
				}
				continue
			}
			first := group == 0 || (group == menuAt && mode != "logon")
			m, perr := diag.ParseMessage(payload, first && len(payload) > diag.DPHeaderLen)
			if perr == nil {
				log("<- client frame, %d bytes, %s, %d items", len(payload), m.Header, len(diag.ParseItems(m.Body)))
			} else {
				log("<- client frame, %d bytes (%v)", len(payload), perr)
			}
			if mode == "input" {
				var client []diag.FieldValue
				if perr == nil {
					client = diag.ClientFields(diag.ParseItems(m.Body))
				}
				if selOK {
					if out := inputRespond(cap, selFrame, client, log); out != nil {
						_ = send("selection screen with the answer", out)
					}
				}
				continue
			}
			if mode == "app" {
				st.turns++
				if perr == nil {
					st.fields = diag.ClientFields(diag.ParseItems(m.Body))
					st.events = diag.Events(diag.ParseItems(m.Body))
				}
				if out, ok := appRespond(cap, screenFrame, st); ok {
					_ = send(fmt.Sprintf("app screen, turn %d", st.turns), out)
				}
				continue
			}
			if (mode == "flash" || mode == "synth" || mode == "list") && pushing == nil {
				rend := renderer(mode, cap, screenFrame, pushFrame, log)
				if mode == "flash" {
					// flash replays the captured screen as it was.
					if f, ok := cap.ServerFrame(screenFrame); ok {
						_ = send(fmt.Sprintf("the probe's screen, no handshake (capture S->C #%d)", screenFrame), f.Data)
					}
				} else {
					// synth and list draw their own first frame.
					_ = send("our own screen, no handshake", rend(0))
				}
				pushing = make(chan struct{})
				go push(ctx, c, rend, cadence, log)
				continue
			}
			if pushing != nil {
				// The GUI answered a pushed screen; keep pushing, say nothing.
				continue
			}
			if group >= len(cap.Replies) {
				log("no captured reply for client frame %d; silent", group)
				continue
			}
			for i, f := range cap.Replies[group] {
				if err := send(fmt.Sprintf("reply %d/%d to client frame %d (capture S->C #%d)", i+1, len(cap.Replies[group]), group, f.Index), f.Data); err != nil {
					log("write: %v", err)
					return
				}
			}
			group++
			if mode == "counter" && group == menuAt+3 {
				// The start menu is up. Show the probe's screen, then push.
				if f, ok := cap.ServerFrame(screenFrame); ok {
					_ = send(fmt.Sprintf("the probe's screen (capture S->C #%d)", screenFrame), f.Data)
				}
				pushing = make(chan struct{})
				go push(ctx, c, renderer(mode, cap, screenFrame, pushFrame, log), cadence, log)
			}
		}
	}
}

var counterText = regexp.MustCompile(`\x20{4,9}[0-9]{1,6}\x20`)

// push sends the probe's pushed frame again and again, the counter in it
// replaced, the header's stat=f0 kept as the capture had it.
// renderer picks how the pushed frames are built: synth from our own
// frame.Screen, or a patch of a captured frame.
// listRenderer builds a static classic list from frame.Lines and serves
// it in the wrapper of the located screen frame — the write-list shown with
// the primitives we have, no capture of a real list needed.
func listRenderer(cap *replay.Capture, wrapFrame int, log func(string, ...any)) func(n int) []byte {
	f, ok := cap.ServerFrame(wrapFrame)
	if !ok {
		log("no server frame #%d to wrap", wrapFrame)
		return func(int) []byte { return nil }
	}
	m, err := diag.ParseMessage(f.Data, false)
	if err != nil {
		log("wrap frame: %v", err)
		return func(int) []byte { return nil }
	}
	items := diag.ParseItems(m.Body)
	atomIdx := -1
	for i, it := range items {
		if it.Type == diag.ItemAPPL4 && it.ID == 0x09 && it.SID == 0x02 {
			atomIdx = i
		}
	}
	if atomIdx < 0 {
		return func(int) []byte { return nil }
	}
	lines := []string{
		"odgp classic list  --  a write-list, drawn from frame.Lines",
		"------------------------------------------------------------",
		"idx    label      value      square",
		"------------------------------------------------------------",
	}
	for i := 1; i <= 18; i++ {
		lines = append(lines, fmt.Sprintf("%3d    row        %-9d  %d", i, i, i*i))
	}
	lines = append(lines, "------------------------------------------------------------", "end of list")
	scr := frame.New(27, 120).Lines(0, lines)
	items[atomIdx].Value = scr.Encode()
	h := m.Header
	h.Compress = 0
	payload, err := diag.EncodeMessage(h, items, false)
	if err != nil {
		return func(int) []byte { return nil }
	}
	return func(int) []byte { return payload }
}

// appState is what a Go "PBO/PAI" keeps between screens: how many times the
// user acted, and the last values the client returned.
type appState struct {
	turns  int
	fields []diag.FieldValue
	events []diag.Event
}

// appScreen is the demo handler: it draws what the user has done. Each PAI
// (an Enter, a button) is one turn; any value the client echoed and any
// control event are shown. This is a Go program's PBO — the screen — built
// from the PAI it just received.
func appScreen(st *appState) *frame.Screen {
	scr := frame.New(27, 120).
		Text(1, 2, "odgp interactive  --  a Go PBO/PAI loop").
		Text(3, 2, "Enters so far").
		Number(3, 20, 10, "GV_TICKS", st.turns).
		Text(5, 2, "press Enter to count; what you type in a field comes back below")
	row := 7
	for _, f := range st.fields {
		if f.Value == "" {
			continue
		}
		scr.Text(row, 2, fmt.Sprintf("field @%d,%d", f.Row, f.Col))
		scr.Text(row, 20, f.Value)
		row++
	}
	for _, e := range st.events {
		scr.Text(row, 2, fmt.Sprintf("event %s/%s %s", e.ShellID, e.EventID, e.Value))
		row++
	}
	return scr
}

// appRespond builds one server frame for the app: the demo screen wrapped
// in the located screen frame, its DYNT_ATOM ours.
func appRespond(cap *replay.Capture, wrapFrame int, st *appState) ([]byte, bool) {
	f, ok := cap.ServerFrame(wrapFrame)
	if !ok {
		return nil, false
	}
	m, err := diag.ParseMessage(f.Data, false)
	if err != nil {
		return nil, false
	}
	items := diag.ParseItems(m.Body)
	for i, it := range items {
		if it.Type == diag.ItemAPPL4 && it.ID == 0x09 && it.SID == 0x02 {
			items[i].Value = appScreen(st).Encode()
			h := m.Header
			h.Compress = 0
			out, err := diag.EncodeMessage(h, items, false)
			return out, err == nil
		}
	}
	return nil, false
}

func renderer(mode string, cap *replay.Capture, screenFrame, pushFrame int, log func(string, ...any)) func(n int) []byte {
	switch mode {
	case "synth":
		return synthRenderer(cap, pushFrame, log)
	case "list":
		return listRenderer(cap, screenFrame, log)
	}
	return patchRenderer(cap, pushFrame, log)
}

func push(ctx context.Context, c net.Conn, render func(n int) []byte, cadence time.Duration, log func(string, ...any)) {
	n := 0
	t := time.NewTicker(cadence)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		n++
		frame, err := ni.EncodeFrame(render(n))
		if err != nil {
			return
		}
		if _, err := c.Write(frame); err != nil {
			log("push: %v", err)
			return
		}
		if n%10 == 1 {
			log("-> pushed frame %d (%d bytes)", n, len(render(n)))
		}
	}
}

// patchRenderer reuses a captured push frame and rewrites the counter text
// in its decompressed bytes — the screen is the capture's, only the number
// is ours.
func patchRenderer(cap *replay.Capture, pushFrame int, log func(string, ...any)) func(n int) []byte {
	f, ok := cap.ServerFrame(pushFrame)
	if !ok {
		log("no server frame #%d to push", pushFrame)
		return func(int) []byte { return nil }
	}
	plain, err := replay.Plain(f.Data)
	if err != nil {
		log("push frame: %v", err)
		return func(int) []byte { return nil }
	}
	loc := counterText.FindIndex(plain)
	if loc == nil {
		log("push frame: no counter text found; pushing it unchanged")
	}
	return func(n int) []byte {
		out := append([]byte{}, plain...)
		if loc != nil {
			width := loc[1] - loc[0] - 1
			copy(out[loc[0]:loc[1]], fmt.Sprintf("%*d ", width, n))
		}
		return out
	}
}

// synthRenderer keeps a captured frame only as the wrapper — the env block,
// the dynpro, the DataManager XML — and rebuilds the screen itself each
// tick from a frame.Screen we describe. This is the near side proving
// itself against a real GUI: the number the GUI shows is drawn from our
// own DYNT_ATOM, not the capture's.
func synthRenderer(cap *replay.Capture, pushFrame int, log func(string, ...any)) func(n int) []byte {
	f, ok := cap.ServerFrame(pushFrame)
	if !ok {
		log("no server frame #%d to wrap", pushFrame)
		return func(int) []byte { return nil }
	}
	m, err := diag.ParseMessage(f.Data, false)
	if err != nil {
		log("wrap frame: %v", err)
		return func(int) []byte { return nil }
	}
	items := diag.ParseItems(m.Body)
	atomIdx := -1
	for i, it := range items {
		if it.Type == diag.ItemAPPL4 && it.ID == 0x09 && it.SID == 0x02 {
			atomIdx = i
		}
	}
	if atomIdx < 0 {
		log("wrap frame has no DYNT_ATOM; nothing to synthesize into")
		return func(int) []byte { return nil }
	}
	h := m.Header
	h.Compress = 0
	return func(n int) []byte {
		scr := frame.New(27, 120).
			Text(1, 1, "Ticks").
			Number(1, 9, 10, "GV_TICKS", n)
		items[atomIdx].Value = scr.Encode()
		out, err := diag.EncodeMessage(h, items, false)
		if err != nil {
			return nil
		}
		return out
	}
}
