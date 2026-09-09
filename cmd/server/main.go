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
	"math"
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

var capturePath string
var animMsgType byte
var animMsgLoop int
var demoSceneMS int

func main() {
	listen := flag.String("listen", ":3201", "address SAP GUI connects to")
	capture := flag.String("capture", "captures/probe.jsonl", "tap capture to replay")
	conn := flag.Int("conn", 1, "connection of the capture to replay")
	mode := flag.String("mode", "logon", "logon | menu | counter | widgets | anim | demo | colorlist | ...")
	menuAt := flag.Int("menu-at", 2, "client frame index whose replies are the start menu (mode menu, counter)")
	screenFrame := flag.Int("screen", 209, "server frame index that shows the probe's screen (mode counter)")
	pushFrame := flag.Int("push", 222, "server frame index the pushed counter frames are made from (mode counter)")
	pushMS := flag.Int("push-ms", 300, "cadence of the pushed frames")
	msgType := flag.String("msg-type", "E", "status message to trigger a sound under the animation: S, W, E or I (empty = none)")
	msgLoop := flag.Int("msg-loop", 0, "re-send the sound every N frames (0 = once, on the first frame)")
	sceneMS := flag.Int("scene-ms", 3000, "how long each scene of the demo mode runs, in milliseconds (wall clock, not frames)")
	flag.Parse()

	capturePath = *capture
	demoSceneMS = *sceneMS
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
		cad := time.Duration(*pushMS) * time.Millisecond
		if *mode == "anim" || *mode == "demo" {
			if *pushMS == 300 {
				cad = 80 * time.Millisecond // faster default for the full-screen effect
			}
			if cad < 60*time.Millisecond {
				cad = 60 * time.Millisecond // a floor: the GUI cannot consume a full-screen frame faster
			}
		}
		if *mode == "widgets" && *pushMS == 300 {
			cad = 60 * time.Millisecond // light frames, so a brisker default
		}
		go serve(ctx, c, cap, *mode, *menuAt, *screenFrame, *pushFrame, cad, msgByte(*msgType), *msgLoop)
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
func inputRespond(cap *replay.Capture, selFrame int, client []diag.FieldValue, st *appState, log func(string, ...any)) []byte {
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
		// Read what the user left in P_MS: the client echoes it at the cell
		// the server placed the field. Log every value the client returned,
		// so a miss is visible.
		typed := ""
		if idx, ok := byName["P_MS"]; ok {
			ms := atoms[idx]
			for _, fv := range client {
				if fv.Row == ms.Row && fv.Col == ms.Col {
					typed = strings.TrimSpace(fv.Value)
				}
			}
		}
		log("input turn %d: client returned %d field(s), P_MS=%q", st.turns, len(client), typed)
		// The number to work from: what the user typed if it parses, else
		// what we last showed. Then P_MS = P_MS + 1, shown back.
		n := st.lastMS
		if v, perr := strconv.Atoi(typed); perr == nil {
			n = v
		}
		n++
		st.lastMS = n
		diag.SetField(atoms, byName, "P_MS", strconv.Itoa(n))
		diag.SetField(atoms, byName, "P_TICKS", strconv.Itoa(n))
		diag.SetField(atoms, byName, "P_TIME", fmt.Sprintf("turn %d", st.turns))
		diag.SetField(atoms, byName, "P_BAR", fmt.Sprintf("Go did P_MS+1 -> %d", n))
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

func serve(ctx context.Context, c net.Conn, cap *replay.Capture, mode string, menuAt, screenFrame, pushFrame int, cadence time.Duration, msgType byte, msgLoop int) {
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
	// closeSession ends the dialog the way the real server does: a bare
	// DIAG header with the end-of-conversation and end-of-program flags,
	// no body. The GUI closes the window on it, where a dropped socket
	// gave a "connection broken" error instead.
	closeSession := func() {
		h := diag.Header{ComFlag: diag.FlagTermEOC | diag.FlagTermEOP, MsgInfo: 0x01}
		if fr, err := ni.EncodeFrame(h.Bytes()); err == nil {
			_, _ = c.Write(fr)
		}
		log("-> session end (EOP)")
	}
	dec, _ := ni.NewFrameDecoder(64 << 20)
	buf := make([]byte, 64<<10)
	// group is the client frame of the capture the next reply group belongs to.
	group := 0
	if mode == "menu" || mode == "counter" {
		group = menuAt
	}
	st := &appState{}
	popup := replay.FindPopup(capturePath)
	var listWrap []byte
	if mode == "colorlist" {
		listWrap = replay.FindListWrap(capturePath)
		if listWrap != nil {
			log("plain list wrapper located in the capture")
		}
	}
	jokeStep := 0
	animOn := false
	animCadence := cadence
	animMsgType = msgType
	animMsgLoop = msgLoop
	selFrame, selOK := 0, false
	if mode == "input" {
		selFrame, selOK = findSelectionFrame(cap)
		if selOK {
			log("selection screen located by content: server frame #%d", selFrame)
		}
	}
	if mode == "counter" || mode == "flash" || mode == "synth" || mode == "list" || mode == "app" || mode == "showcase" || mode == "states" || mode == "anim" || mode == "widgets" || mode == "demo" {
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
			// A window-close: the GUI sends the system command "/i". Answer
			// with the joke popup once, then accept the next click (any
			// button) by closing.
			if perr == nil && jokeStep == 0 && isClose(diag.ParseItems(m.Body)) {
				if jp := jokePopup1(popup); jp != nil {
					jokeStep = 1
					_ = send("joke popup 1: Where are you going???", jp)
					continue
				}
				closeSession()
				log("window close accepted (no popup template)")
				return
			}
			if jokeStep == 1 {
				if jp := jokePopup2(popup); jp != nil {
					jokeStep = 2
					_ = send("joke popup 2: =(", jp)
					continue
				}
				closeSession()
				return
			}
			if jokeStep == 2 {
				closeSession()
				log("closing after the two jokes")
				return
			}
			// /o (new window) turns the current window into an animation.
			if perr == nil && animOn == false && isNewWindow(diag.ParseItems(m.Body)) {
				animOn = true
				log("/o: starting animation in this window")
				go push(ctx, c, animRenderer(cap, screenFrame, log), animCadence, pushing, log)
				continue
			}
			if animOn {
				continue
			}
			if mode == "input" {
				var client []diag.FieldValue
				if perr == nil {
					cItems := diag.ParseItems(m.Body)
					client = diag.ClientFields(cItems)
					var keys []string
					for _, it := range cItems {
						k := it.Key()
						if it.Type == diag.ItemAPPL || it.Type == diag.ItemAPPL4 {
							k = fmt.Sprintf("%s(%d)", k, len(it.Value))
						}
						keys = append(keys, k)
					}
					log("client items: %s", strings.Join(keys, " "))
				}
				if selOK {
					st.turns++
					if out := inputRespond(cap, selFrame, client, st, log); out != nil {
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
			if mode == "states" || mode == "showcase" {
				var client []diag.FieldValue
				if perr == nil {
					client = diag.ClientFields(diag.ParseItems(m.Body))
				}
				if out := staticRespond(cap, screenFrame, mode, client); out != nil {
					_ = send("static screen (input preserved)", out)
				}
				continue
			}
			if (mode == "flash" || mode == "synth" || mode == "list" || mode == "anim" || mode == "colorlist" || mode == "widgets" || mode == "demo") && pushing == nil {
				rend := renderer(mode, cap, screenFrame, pushFrame, listWrap, log)
				if mode == "flash" {
					// flash replays the captured screen as it was.
					if f, ok := cap.ServerFrame(screenFrame); ok {
						_ = send(fmt.Sprintf("the probe's screen, no handshake (capture S->C #%d)", screenFrame), f.Data)
					}
				} else {
					// synth and the static demos draw their own first frame.
					_ = send("our own screen, no handshake", rend(0))
				}
				pushing = make(chan struct{})
				// A static screen is sent once and left alone; only the
				// animated modes keep pushing on a timer. Pushing a static
				// screen every tick overwrote what the user was typing.
				if mode == "list" || mode == "colorlist" {
					// a list is static: sent once, no timer.
				} else {
					go push(ctx, c, rend, cadence, pushing, log)
				}
				continue
			}
			if (mode == "anim" || mode == "widgets" || mode == "demo" || animOn) && pushing != nil {
				// While animating, any client frame is the user pressing a
				// key (F3, Back, Enter): stop the animation and freeze the
				// last frame. A window-close was handled just above.
				close(pushing)
				pushing = nil
				animOn = false
				log("animation stopped by the user")
				frozen := frame.New(27, 120).
					Text(1, 2, "animation stopped").
					Text(3, 2, "close the window to exit")
				if out := staticRespondWrap(cap, screenFrame, frozen); out != nil {
					_ = send("animation stopped", out)
				}
				continue
			}
			if pushing != nil {
				// A static screen or a pushing animation. Log what the client
				// sent — its events and any function code — so a window-close
				// signal is visible in the trace.
				if perr == nil {
					ci := diag.ParseItems(m.Body)
					if evs := diag.Events(ci); len(evs) > 0 {
						log("client events: %+v (com=%02x)", evs, m.Header.ComFlag)
					} else {
						log("client frame: %d items, com=%02x type=%02x", len(ci), m.Header.ComFlag, m.Header.MsgType)
					}
				}
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
				go push(ctx, c, renderer(mode, cap, screenFrame, pushFrame, listWrap, log), cadence, pushing, log)
			}
		}
	}
}

var counterText = regexp.MustCompile(`\x20{4,9}[0-9]{1,6}\x20`)

// push sends the probe's pushed frame again and again, the counter in it
// replaced, the header's stat=f0 kept as the capture had it.
// renderer picks how the pushed frames are built: synth from our own
// frame.Screen, or a patch of a captured frame.
// statesScreen shows an input field in each state we can set: active,
// protected (inactive), hidden, and value-help (F4). The hidden one is
// there but not drawn; the F4 one shows the matchcode button.
func statesScreen() *frame.Screen {
	return frame.New(27, 120).
		Frame(0, 0, 70, 14, "Input field states and types").
		Text(1, 2, "active").Input(1, 20, 20, "S_ACT", "type here").
		Text(2, 2, "inactive").InputProtected(2, 20, 20, "S_INA", "cannot edit").
		Text(3, 2, "hidden").InputHidden(3, 20, 20, "S_HID", "secret").
		Text(3, 44, "(hidden is here, not shown)").
		Text(4, 2, "F4 help").InputF4(4, 20, 20, "S_F4", "press F4").
		Text(6, 2, "date").Date(6, 20, "S_DAT", "2026-09-09").
		Text(7, 2, "time").Time(7, 20, "S_TIM", "14:30:00").
		Text(9, 2, "static screen: type freely, it will not be overwritten")
}

// statesRenderer wraps the located screen frame and swaps in the states screen.
func statesRenderer(cap *replay.Capture, wrapFrame int, log func(string, ...any)) func(n int) []byte {
	f, ok := cap.ServerFrame(wrapFrame)
	if !ok {
		return func(int) []byte { return nil }
	}
	m, err := diag.ParseMessage(f.Data, false)
	if err != nil {
		return func(int) []byte { return nil }
	}
	items := diag.ParseItems(m.Body)
	for i, it := range items {
		if it.Type == diag.ItemAPPL4 && it.ID == 0x09 && it.SID == 0x02 {
			items[i].Value = statesScreen().Encode()
			h := m.Header
			h.Compress = 0
			out, err := diag.EncodeMessage(h, items, false)
			if err != nil {
				return func(int) []byte { return nil }
			}
			return func(int) []byte { return out }
		}
	}
	return func(int) []byte { return nil }
}

// isClose reports whether a client frame is the window-close request: the
// GUI sends the system command "/i" in a VARINFO.04 item when the user
// shuts the window.
// isNewWindow reports whether the client sent the /o system command, the
// one that opens a new session window.
func isNewWindow(items []diag.Item) bool {
	for _, it := range items {
		if it.Type == diag.ItemAPPL && it.ID == 0x0c && it.SID == 0x04 && strings.HasPrefix(strings.TrimSpace(string(it.Value)), "/o") {
			return true
		}
	}
	return false
}

func isClose(items []diag.Item) bool {
	for _, it := range items {
		if it.Type == diag.ItemAPPL && it.ID == 0x0c && it.SID == 0x04 && strings.TrimSpace(string(it.Value)) == "/i" {
			return true
		}
	}
	return false
}

// jokePopup swaps the text and buttons of the captured modal log-off popup
// for a joke: "Where are you going?" with two buttons that both say No.
// Reusing the captured frame keeps it a real modal dialog box; only the
// DYNT_ATOM changes. Empty when the capture had no popup to borrow.
func popupWith(popup []byte, scr *frame.Screen) []byte {
	if popup == nil {
		return nil
	}
	m, err := diag.ParseMessage(popup, false)
	if err != nil {
		return nil
	}
	items := diag.ParseItems(m.Body)
	for i, it := range items {
		if it.Type != diag.ItemAPPL4 || it.ID != 0x09 || it.SID != 0x02 {
			continue
		}
		items[i].Value = scr.Encode()
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

// jokePopup1 asks where you are going, with two buttons that both say No.
func jokePopup1(popup []byte) []byte {
	return popupWith(popup, frame.New(6, 60).
		Text(1, 7, "Where are you going???").
		Text(2, 7, "FIORI???").
		Button(4, 7, 10, "No", "=NO1").
		Button(4, 20, 10, "No", "=NO2"))
}

// jokePopup2 is the sad face with a single ok.
func jokePopup2(popup []byte) []byte {
	return popupWith(popup, frame.New(6, 60).
		Text(1, 7, "=(").
		Button(3, 7, 10, "ok", "=OK"))
}

// withSound inserts a status message before EOM on the first frame (and
// every animMsgLoop frames) so the GUI plays that type's sound under an
// animation. The type and loop come from the flags via package state.
func withSound(items []diag.Item, n int) []diag.Item {
	if animMsgType == 0 || !(n == 1 || (animMsgLoop > 0 && n%animMsgLoop == 1)) {
		return items
	}
	msg := diag.StatusMessage(animMsgType, "odgp: now playing")
	out := make([]diag.Item, 0, len(items)+1)
	for _, it := range items {
		if it.Type == diag.ItemEOM {
			out = append(out, msg)
		}
		out = append(out, it)
	}
	return out
}

// widgetsScreen animates a few real widgets — buttons and a framed box —
// by moving them, not by redrawing a grid of characters. A frame is a
// handful of elements, so it is small and the GUI keeps up at a fast
// cadence; the motion is smoother than the starfield's.
// centre pads text to width with spaces on both sides.
func centre(text string, width int) string {
	if len(text) >= width {
		return text
	}
	left := (width - len(text)) / 2
	right := width - len(text) - left
	return fmt.Sprintf("%*s%s%*s", left, "", text, right, "")
}

func widgetsScreen(t int) *frame.Screen {
	scr := frame.New(27, 120)
	scr.Frame(0, 0, 78, 24, "OPEN-DIAG-GO-PRO  --  widgets orbiting, drawn by Go")
	// Three buttons on a circle, 120 degrees apart, turning counter-clockwise.
	// The column radius is larger than the row radius because a character
	// cell is about twice as tall as it is wide, so the path reads round.
	const cx, cy, rx, ry = 39.0, 12.0, 28.0, 9.0
	const speed = 0.06 // radians per frame
	labels := []string{"Go", "DIAG", "no ABAP"}
	for i, lab := range labels {
		ang := -float64(t)*speed + float64(i)*(2.0*math.Pi/3.0) // minus = counter-clockwise
		col := int(cx + rx*math.Cos(ang))
		row := int(cy + ry*math.Sin(ang))
		// Depth: 0 at the back (top), 1 at the front (bottom). The button
		// grows with depth, so the nearer ones look bigger, and the caption
		// is centred in the wider box.
		depth := (math.Sin(ang) + 1.0) / 2.0
		w := 6 + int(depth*12.0)    // 6 wide at the back, 18 at the front
		h := 1 + int(depth*2.0+0.5) // 1 row at the back, up to 3 at the front
		scr.ButtonH(row, col, w, h, centre(lab, w-2), fmt.Sprintf("=B%d", i))
	}
	scr.Text(int(cy), int(cx)-3, "( o )")
	scr.Text(25, 2, fmt.Sprintf("frame %d   3 buttons orbiting CCW   F3/Back stops", t))
	return scr
}

// widgetsRenderer wraps the located screen frame and moves the widgets.
func widgetsRenderer(cap *replay.Capture, wrapFrame int, log func(string, ...any)) func(n int) []byte {
	f, ok := cap.ServerFrame(wrapFrame)
	if !ok {
		return func(int) []byte { return nil }
	}
	m, err := diag.ParseMessage(f.Data, false)
	if err != nil {
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
	h := m.Header
	h.Compress = 0
	base := items
	return func(n int) []byte {
		items := append([]diag.Item{}, base...)
		items[atomIdx].Value = widgetsScreen(n).Encode()
		items = withSound(items, n)
		out, err := diag.EncodeMessage(h, items, false)
		if err != nil {
			return nil
		}
		return out
	}
}

// A scene is one act of the demo: a name, the approach it shows off, and a
// draw that lays it onto the screen given ts — the seconds elapsed inside
// this scene. Every scene is driven by wall-clock time, so the motion is the
// same speed whatever the frame cadence, and a dropped frame never stutters
// it: at 3 s a scene ends after 3 real seconds, not after N frames.
type scene struct {
	name     string
	approach string
	draw     func(ts float64, scr *frame.Screen)
}

// demoScenes are the acts, each a different way of getting motion onto a real
// GUI, ordered from the lightest frame to the heaviest so the contrast in
// bytes-per-frame is easy to feel.
func demoScenes() []scene {
	return []scene{
		{"bounce", "one label bouncing — the fewest bytes a frame can carry", sceneBounce},
		{"orbit", "3 widgets moved by coordinate, sized by depth", sceneOrbit},
		{"equalizer", "a row of buttons whose Height is the graphics — bars", sceneEqualizer},
		{"boxes", "nested frames breathing — the frame primitive as graphics", sceneBoxes},
		{"snake", "a label snake on a Lissajous path, with a fading trail", sceneSnake},
		{"matrix", "sparse falling columns — the grid used lightly", sceneMatrix},
		{"starfield", "the whole character grid redrawn every frame (~80 labels)", sceneStars},
		{"icons", "probe: does the GUI substitute @xx@ icon tokens in a label?", sceneIcons},
	}
}

// sceneBounce is a DVD-logo bounce: one label ricocheting off the edges. The
// position is a triangle wave of ts, so it turns at the walls on its own.
func sceneBounce(ts float64, scr *frame.Screen) {
	const w, h = 66, 18
	logo := "[ Go > DIAG ]"
	px := triangle(ts*22.0, w-len(logo))
	py := triangle(ts*9.0, h-1)
	scr.Text(2+py, 4+px, logo)
	scr.Text(int(3+py+1), 4+px, "  no ABAP  ")
}

// triangle bounces a value between 0 and span: it rises, hits the wall, and
// comes back, forever.
func triangle(x float64, span int) int {
	if span <= 0 {
		return 0
	}
	p := math.Mod(x, float64(2*span))
	if p < 0 {
		p += float64(2 * span)
	}
	if p > float64(span) {
		p = float64(2*span) - p
	}
	return int(p)
}

// sceneOrbit is the three widgets orbiting counter-clockwise, each button
// grown by its depth on the circle — the same effect as the widgets mode,
// but its angle comes from ts so the spin is one turn every ~5.7 s whatever
// the cadence.
func sceneOrbit(ts float64, scr *frame.Screen) {
	const cx, cy, rx, ry = 39.0, 11.0, 28.0, 8.0
	const angSpeed = 1.1 // radians per second
	labels := []string{"Go", "DIAG", "no ABAP"}
	for i, lab := range labels {
		ang := -ts*angSpeed + float64(i)*(2.0*math.Pi/3.0)
		col := int(cx + rx*math.Cos(ang))
		row := int(cy + ry*math.Sin(ang))
		depth := (math.Sin(ang) + 1.0) / 2.0
		w := 6 + int(depth*12.0)
		h := 1 + int(depth*2.0+0.5)
		scr.ButtonH(row, col, w, h, centre(lab, w-2), fmt.Sprintf("=B%d", i))
	}
	scr.Text(int(cy), int(cx)-2, "( o )")
}

// sceneBoxes breathes four concentric frames in and out around the centre —
// the FRAME atom used as a drawing primitive, a handful of elements a frame,
// almost no bytes. The boxes are centred and modest: the outer is ~44x14,
// not the whole screen.
func sceneBoxes(ts float64, scr *frame.Screen) {
	const cx, cy = 39, 10 // centre column, centre row
	breath := (math.Sin(ts*2.0) + 1.0) / 2.0
	names := []string{"DIAG", "no", "ABAP", "Go"}
	for i := 0; i < 4; i++ {
		hw := 22 - i*6 + int(breath*3.0) // half width: 22,16,10,4 (+breath)
		hh := 7 - i*2                    // half height: 7,5,3,1
		if hw < 3 || hh < 1 {
			continue
		}
		scr.Frame(cy-hh, cx-hw, hw*2, hh*2, names[i])
	}
}

// sceneEqualizer is a row of narrow buttons whose Height rises and falls in a
// travelling sine wave — the pushbutton Height field driven as a bar chart,
// so the graphics live in the element's own dimensions, not in drawn glyphs.
func sceneEqualizer(ts float64, scr *frame.Screen) {
	const bars, baseRow = 12, 20 // bars stand on baseRow and grow upward
	for i := 0; i < bars; i++ {
		amp := (math.Sin(ts*3.0+float64(i)*0.5) + 1.0) / 2.0 // 0..1
		h := 1 + int(amp*10.0)                               // 1..11 rows tall
		col := 6 + i*6
		scr.ButtonH(baseRow-h, col, 4, h, "", fmt.Sprintf("=EQ%d", i))
	}
	for c := 4; c < 6+bars*6; c++ {
		scr.Text(baseRow, c, "-") // a floor the bars stand on
	}
}

// sceneSnake walks a marker along a Lissajous path and draws a fading trail
// behind it — each segment is one label, so the whole snake is a dozen-odd
// bytes. The two frequencies are not commensurate, so the path never repeats
// exactly.
func sceneSnake(ts float64, scr *frame.Screen) {
	const cx, cy, rx, ry = 39.0, 10.0, 30.0, 8.0
	const seg = 16
	for k := 0; k < seg; k++ {
		tt := ts - float64(k)*0.05
		col := int(cx + rx*math.Sin(tt*1.7))
		row := int(cy + ry*math.Sin(tt*2.3))
		ch := "O"
		switch {
		case k > 10:
			ch = "."
		case k > 4:
			ch = "o"
		}
		scr.Text(row, col, ch)
	}
}

// sceneMatrix drops sparse columns of glyphs down the screen, each column at
// its own speed and phase, a short trail behind every head — the character
// grid used lightly (every third column, a five-cell trail) rather than
// filled edge to edge.
func sceneMatrix(ts float64, scr *frame.Screen) {
	const w, h = 78, 20
	const glyphs = "01<>[]{}=+*/\\ABCDEF$#@abcdef"
	for x := 0; x < w; x += 3 {
		speed := 6.0 + float64((x*37)%11)
		off := float64((x * 13) % 23)
		head := int(math.Mod(ts*speed+off, float64(h+8)))
		for t := 0; t < 5; t++ {
			y := head - t
			if y < 0 || y >= h {
				continue
			}
			g := glyphs[(x+y*7+int(ts*10.0))%len(glyphs)]
			scr.Text(1+y, 1+x, string(g))
		}
	}
}

// sceneIcons is a probe, not an effect: it lays a grid of SAP icon tokens
// (@00@ .. @2F@) with their hex under each, to see whether a real GUI
// substitutes the icon bitmap for the token inside a plain label. If the
// tokens show as literal text, icons need the list channel or an icon field
// and this label path is not enough — either way we learn it here. A slow
// sweep highlights one cell so the scene still moves.
func sceneIcons(ts float64, scr *frame.Screen) {
	scr.Text(1, 2, "icon probe: if these become pictures, @xx@ works in a label")
	const cols = 8
	sweep := int(ts*6.0) % 48
	for code := 0; code < 48; code++ {
		r := 3 + (code/cols)*2
		c := 4 + (code%cols)*9
		mark := " "
		if code == sweep {
			mark = ">"
		}
		scr.Text(r, c, fmt.Sprintf("%s@%02X@", mark, code))
		scr.Text(r+1, c+1, fmt.Sprintf("%02X", code))
	}
}

// sceneStars is the starfield and marquee: the whole grid rewritten each
// frame. Its scroll and wave phases come from ts, so it moves at a fixed
// speed and the heavier payload does not change the animation's pace.
func sceneStars(ts float64, scr *frame.Screen) {
	const w, h = 78, 18
	banner := "  OPEN-DIAG-GO-PRO  ***  the whole character grid, redrawn  ***  driven by Go  "
	off := int(ts * 12.0) // 12 characters a second
	line := make([]byte, w)
	for i := 0; i < w; i++ {
		line[i] = banner[(off+i)%len(banner)]
	}
	scr.Text(1, 1, string(line))
	for x := 0; x < w; x++ {
		y := h/2 + int(float64(h/2-1)*math.Sin(float64(x)/6.0+ts*2.0))
		if y >= 0 && y < h {
			scr.Text(3+y, 1+x, "*")
		}
	}
}

// demoRenderer cycles the scenes on a wall clock: each runs demoSceneMS
// milliseconds, then the next, then back to the first. The renderer ignores
// the frame counter push hands it and reads the real elapsed time, so the
// scenes advance by seconds, not by frames.
func demoRenderer(cap *replay.Capture, wrapFrame int, log func(string, ...any)) func(n int) []byte {
	f, ok := cap.ServerFrame(wrapFrame)
	if !ok {
		return func(int) []byte { return nil }
	}
	m, err := diag.ParseMessage(f.Data, false)
	if err != nil {
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
	h := m.Header
	h.Compress = 0
	base := items
	scenes := demoScenes()
	sceneDur := time.Duration(demoSceneMS) * time.Millisecond
	if sceneDur <= 0 {
		sceneDur = 3 * time.Second
	}
	var start time.Time
	lastScene := -1
	return func(n int) []byte {
		if start.IsZero() {
			start = time.Now()
		}
		total := sceneDur * time.Duration(len(scenes))
		pos := time.Since(start) % total
		idx := int(pos / sceneDur)
		if idx >= len(scenes) {
			idx = len(scenes) - 1
		}
		ts := (pos - time.Duration(idx)*sceneDur).Seconds()
		if idx != lastScene {
			log("scene %d/%d: %s (%s)", idx+1, len(scenes), scenes[idx].name, scenes[idx].approach)
			lastScene = idx
		}
		scr := frame.New(27, 120)
		scenes[idx].draw(ts, scr)
		scr.Text(24, 1, fmt.Sprintf("scene %d/%d  %-9s  approach: %s", idx+1, len(scenes), scenes[idx].name, scenes[idx].approach))
		scr.Text(25, 1, "F3/Back or close the window to stop")
		out := append([]diag.Item{}, base...)
		out[atomIdx].Value = scr.Encode()
		out = withSound(out, n)
		msg, err := diag.EncodeMessage(h, out, false)
		if err != nil {
			return nil
		}
		return msg
	}
}

// animScreen is one frame of a timed animation: a scrolling marquee and a
// sine wave of stars that moves with t. Every element is a label, so it
// draws on a real GUI and in the TUI alike. This is the effect-engine
// kernel — each tick writes a fresh screen.
func animScreen(t int) *frame.Screen {
	const w, h = 78, 20
	scr := frame.New(27, 120)
	// A marquee scrolling left across the top.
	banner := "  OPEN-DIAG-GO-PRO  ***  a screen SAP GUI draws, driven by Go  ***"
	line := make([]byte, w)
	for i := 0; i < w; i++ {
		line[i] = banner[(t+i)%len(banner)]
	}
	scr.Text(0, 1, string(line))
	// A sine wave of stars.
	for x := 0; x < w; x++ {
		y := h/2 + int(float64(h/2-1)*math.Sin(float64(x+t)/6.0))
		if y >= 0 && y < h {
			scr.Text(2+y, 1+x, "*")
		}
	}
	scr.Text(24, 1, fmt.Sprintf("frame %d   (press F3/Back or close to stop)", t))
	return scr
}

// animRenderer wraps the located screen frame and animates its DYNT_ATOM.
func animRenderer(cap *replay.Capture, wrapFrame int, log func(string, ...any)) func(n int) []byte {
	f, ok := cap.ServerFrame(wrapFrame)
	if !ok {
		return func(int) []byte { return nil }
	}
	m, err := diag.ParseMessage(f.Data, false)
	if err != nil {
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
	h := m.Header
	h.Compress = 0
	base := items
	return func(n int) []byte {
		items := append([]diag.Item{}, base...)
		items[atomIdx].Value = animScreen(n).Encode()
		items = withSound(items, n)
		out, err := diag.EncodeMessage(h, items, false)
		if err != nil {
			return nil
		}
		return out
	}
}

// animRenderer's message type/loop are closed over from serve via package
// state set below.
func msgByte(s string) byte {
	if s == "" {
		return 0
	}
	return s[0]
}

// staticRespondWrap wraps a screen in the located screen frame, for a
// one-off static send such as a frozen animation.
func staticRespondWrap(cap *replay.Capture, wrapFrame int, scr *frame.Screen) []byte {
	f, ok := cap.ServerFrame(wrapFrame)
	if !ok {
		return nil
	}
	m, err := diag.ParseMessage(f.Data, false)
	if err != nil {
		return nil
	}
	items := diag.ParseItems(m.Body)
	for i, it := range items {
		if it.Type == diag.ItemAPPL4 && it.ID == 0x09 && it.SID == 0x02 {
			items[i].Value = scr.Encode()
			h := m.Header
			h.Compress = 0
			out, _ := diag.EncodeMessage(h, items, false)
			return out
		}
	}
	return nil
}

// findListFrame locates a captured classic-list frame by content: a server
// frame that carries list segments (the SBA/SFE/SLC/VARINFO.0b stream) — the
// list viewer's shell, which a colourful list of ours reuses.
func findListFrame(cap *replay.Capture) (int, bool) {
	// Prefer a plain WRITE list (our ZODGP_LIST, marked by "end of list"): it
	// carries no controls, so its wrapper does not drag an SE16 settings
	// dialog along the way a data-browser list would. Fall back to any list.
	best, ok := 0, false
	for _, f := range cap.Server {
		m, err := diag.ParseMessage(f.Data, false)
		if err != nil {
			continue
		}
		items := diag.ParseItems(m.Body)
		if !diag.HasListSegments(items) || len(diag.ParseListItems(items)) <= 5 {
			continue
		}
		if !ok {
			best, ok = f.Index, true
		}
		for _, seg := range diag.ParseListItems(items) {
			if strings.Contains(strings.ToLower(seg.Text), "end of list") {
				return f.Index, true
			}
		}
	}
	return best, ok
}

// colourListSegments is the demo list: a heading, a rule, and rows in the
// list colours, so the whole palette shows at once.
func colourListSegments() []diag.ListSegment {
	segs := []diag.ListSegment{
		diag.ListText(0, 2, diag.ColHeading, "OPEN-DIAG-GO-PRO  --  a colourful classic list, drawn by Go"),
		diag.ListText(2, 2, diag.ColHeading, "colour"),
		diag.ListText(2, 20, diag.ColHeading, "sample text"),
	}
	rows := []struct {
		name  string
		color byte
	}{
		{"NORMAL", diag.ColNormal}, {"KEY", diag.ColKey}, {"POSITIVE", diag.ColPositive},
		{"NEGATIVE", diag.ColNegative}, {"TOTAL", diag.ColTotal}, {"GROUP", diag.ColGroup},
	}
	for i, r := range rows {
		row := 4 + i
		segs = append(segs,
			diag.ListText(row, 2, diag.ColNormal, r.name),
			diag.ListText(row, 20, r.color, "the quick brown fox 12345"),
		)
	}
	segs = append(segs, diag.ListText(4+len(rows)+1, 2, diag.ColNormal, "each row uses one FORMAT COLOR; set the colours in your theme"))
	// A row of real icons, drawn by our own bytes: an icon is just the "@XX@"
	// token in a text run, which the list channel turns into a picture.
	iconRow := 4 + len(rows) + 3
	segs = append(segs, diag.ListText(iconRow, 2, diag.ColHeading, "icons, drawn by Go:"))
	icons := []string{diag.IconGreenLight, diag.IconYellowLight, diag.IconRedLight,
		diag.IconLEDGreen, diag.IconChecked, diag.IconOkay, diag.IconCancel}
	for i, ic := range icons {
		segs = append(segs, diag.ListIcon(iconRow, 22+i*3, ic))
	}
	return segs
}

// colorlistRenderer splices a colourful list into the captured list frame:
// its own list stream is dropped and ours put in its place, everything else
// (the env block, the list dynpro, EOM) kept.
func colorlistRenderer(wrap []byte, log func(string, ...any)) func(n int) []byte {
	if wrap == nil {
		log("no plain list frame in the capture to wrap")
		return func(int) []byte { return nil }
	}
	m, err := diag.ParseMessage(wrap, false)
	if err != nil {
		return func(int) []byte { return nil }
	}
	items := diag.ParseItems(m.Body)
	var keep []diag.Item
	insertAt := -1
	for _, it := range items {
		isList := it.Type == diag.ItemSBA || it.Type == diag.ItemSFE || it.Type == diag.ItemSLC ||
			(it.Type == diag.ItemAPPL && it.ID == 0x0c && it.SID == 0x0b)
		if isList {
			if insertAt < 0 {
				insertAt = len(keep)
			}
			continue
		}
		keep = append(keep, it)
	}
	if insertAt < 0 {
		insertAt = len(keep)
	}
	mine := diag.EncodeListItems(colourListSegments())
	final := append(append(append([]diag.Item{}, keep[:insertAt]...), mine...), keep[insertAt:]...)
	h := m.Header
	h.Compress = 0
	payload, err := diag.EncodeMessage(h, final, false)
	if err != nil {
		return func(int) []byte { return nil }
	}
	return func(int) []byte { return payload }
}

// staticScreen builds the screen for a static demo mode.
func staticScreen(mode string) *frame.Screen {
	switch mode {
	case "showcase":
		return showcaseScreen()
	case "states":
		return statesScreen()
	}
	return frame.New(24, 80)
}

// staticRespond re-renders a static screen, keeping the values the client
// returned, wrapped in the located screen frame. Answering every PAI this
// way keeps the GUI from hanging on Enter and never loses what was typed.
func staticRespond(cap *replay.Capture, wrapFrame int, mode string, client []diag.FieldValue) []byte {
	f, ok := cap.ServerFrame(wrapFrame)
	if !ok {
		return nil
	}
	m, err := diag.ParseMessage(f.Data, false)
	if err != nil {
		return nil
	}
	items := diag.ParseItems(m.Body)
	for i, it := range items {
		if it.Type == diag.ItemAPPL4 && it.ID == 0x09 && it.SID == 0x02 {
			items[i].Value = staticScreen(mode).Overlay(client).Encode()
			h := m.Header
			h.Compress = 0
			out, err := diag.EncodeMessage(h, items, false)
			if err != nil {
				return nil
			}
			return out
		}
	}
	return nil
}

// showcaseScreen draws one of every element the encoder knows, so a real
// GUI and the TUI both show the whole vocabulary at once.
func showcaseScreen() *frame.Screen {
	return frame.New(27, 120).
		Frame(0, 0, 64, 13, "Field types open-diag-go-pro can encode").
		Text(1, 2, "label").Text(1, 16, "a static caption").
		Text(2, 2, "output").Output(2, 16, 24, "F_OUT", "read-only text", false).
		Text(3, 2, "number").Number(3, 16, 10, "F_NUM", 42).
		Text(4, 2, "input").Input(4, 16, 24, "F_INP", "edit me").
		Text(5, 2, "checkbox").Checkbox(5, 16, "F_CHK", "enabled", true).
		Text(6, 2, "radio").Radio(6, 16, "F_RAD", "option A", true).
		Radio(7, 16, "F_RAD", "option B", false).
		Text(9, 2, "button").Button(9, 16, 16, "Press me", "=GO").
		Text(11, 2, "frame is the box around all of this")
}

// showcaseRenderer wraps the located screen frame and swaps in the showcase.
func showcaseRenderer(cap *replay.Capture, wrapFrame int, log func(string, ...any)) func(n int) []byte {
	f, ok := cap.ServerFrame(wrapFrame)
	if !ok {
		return func(int) []byte { return nil }
	}
	m, err := diag.ParseMessage(f.Data, false)
	if err != nil {
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
	items[atomIdx].Value = showcaseScreen().Encode()
	h := m.Header
	h.Compress = 0
	payload, err := diag.EncodeMessage(h, items, false)
	if err != nil {
		return func(int) []byte { return nil }
	}
	return func(int) []byte { return payload }
}

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
	lastMS int
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

func renderer(mode string, cap *replay.Capture, screenFrame, pushFrame int, listWrap []byte, log func(string, ...any)) func(n int) []byte {
	switch mode {
	case "synth":
		return synthRenderer(cap, pushFrame, log)
	case "list":
		return listRenderer(cap, screenFrame, log)
	case "showcase":
		return showcaseRenderer(cap, screenFrame, log)
	case "states":
		return statesRenderer(cap, screenFrame, log)
	case "anim":
		return animRenderer(cap, screenFrame, log)
	case "colorlist":
		return colorlistRenderer(listWrap, log)
	case "widgets":
		return widgetsRenderer(cap, screenFrame, log)
	case "demo":
		return demoRenderer(cap, screenFrame, log)
	}
	return patchRenderer(cap, pushFrame, log)
}

func push(ctx context.Context, c net.Conn, render func(n int) []byte, cadence time.Duration, stop <-chan struct{}, log func(string, ...any)) {
	n := 0
	t := time.NewTicker(cadence)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
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
