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
	"bytes"
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
		if *mode == "iconanim" || *mode == "led" {
			if *pushMS == 300 {
				cad = 180 * time.Millisecond // gentle default for a pushed list
			}
			if cad < 80*time.Millisecond {
				cad = 80 * time.Millisecond
			}
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
	if mode == "colorlist" || mode == "iconanim" || mode == "led" || mode == "demo" {
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
	if mode == "counter" || mode == "flash" || mode == "synth" || mode == "list" || mode == "app" || mode == "showcase" || mode == "states" || mode == "anim" || mode == "widgets" || mode == "demo" || mode == "snake" || mode == "echo" {
		if sf, pf, ok := findCounterFrames(cap); ok {
			screenFrame, pushFrame = sf, pf
			log("counter screen located by content: screen #%d, push #%d", sf, pf)
		}
	}
	var pushing chan struct{}
	var game *snakeGame
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
				// Stop any animation first: a running push loop would otherwise
				// keep drawing over the log-off popup, and keep writing after the
				// session ends. This is the /i reaction every mode now shares.
				if pushing != nil {
					close(pushing)
					pushing = nil
				}
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
			if mode == "echo" {
				// No buttons: every client frame is a keypress or menu action.
				// The function code is not always in VARINFO.04 — a key can
				// arrive as a UI_EVENT — so show the VALUES of the items that
				// carry an action, not just their keys, to read what each key
				// actually sent.
				st.turns++
				if perr == nil {
					items := diag.ParseItems(m.Body)
					fc := funcCode(items)
					changed := echoDiff(st, items)
					// The interesting part first: the function code, then what
					// changed since the last press. Constant housekeeping is
					// dropped, so a key that actually differs stands out.
					head := "same"
					if changed != "" {
						head = changed
					}
					line := fmt.Sprintf("#%d fc=%q  %s", st.turns, fc, head)
					st.echo = append(st.echo, line)
					if len(st.echo) > 17 {
						st.echo = st.echo[len(st.echo)-17:]
					}
					log("echo #%d fc=%q com=%02x type=%02x  changed:{%s}  all:[%s]",
						st.turns, fc, m.Header.ComFlag, m.Header.MsgType, changed, itemVals(items))
				}
				if out, ok := echoRespond(cap, screenFrame, st); ok {
					_ = send("echo screen", out)
				}
				continue
			}
			if mode == "snake" {
				// The first client frame starts the game and a step loop; every
				// later frame is a button press feeding the game a direction.
				// Unlike the animations, a keypress does NOT stop it — it steers.
				if game == nil {
					game = newSnakeGame(48, 16)
					_ = send("snake: first frame", staticRespondWrap(cap, screenFrame, game.render()))
					pushing = make(chan struct{})
					go func(stop <-chan struct{}) {
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
							game.step()
							fr, err := ni.EncodeFrame(staticRespondWrap(cap, screenFrame, game.render()))
							if err != nil {
								return
							}
							if _, err := c.Write(fr); err != nil {
								log("snake push: %v", err)
								return
							}
						}
					}(pushing)
					continue
				}
				if perr == nil {
					items := diag.ParseItems(m.Body)
					fc := funcCode(items)
					log("snake input: fcode=%q items=[%s]", fc, itemKeys(items))
					game.input(fc)
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
			if (mode == "flash" || mode == "synth" || mode == "list" || mode == "anim" || mode == "colorlist" || mode == "widgets" || mode == "demo" || mode == "iconanim" || mode == "led") && pushing == nil {
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
			if (mode == "anim" || mode == "widgets" || mode == "demo" || mode == "iconanim" || mode == "led" || animOn) && pushing != nil {
				// While animating, any client frame is the user pressing a
				// key (F3, Back, Enter): stop the animation and freeze the
				// last frame. A window-close was handled just above.
				close(pushing)
				pushing = nil
				animOn = false
				log("animation stopped by the user")
				// The dynpro modes swap in a "stopped" screen; the list-channel
				// icon animation stays on its list frame instead of switching
				// channels, so it just freezes on the last frame it pushed.
				if mode != "iconanim" && mode != "led" {
					frozen := frame.New(27, 120).
						Text(1, 2, "animation stopped").
						Text(3, 2, "close the window to exit")
					if out := staticRespondWrap(cap, screenFrame, frozen); out != nil {
						_ = send("animation stopped", out)
					}
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

// funcCode is the function code the client sent in its OK-code field
// (VARINFO.04) — a pushbutton's "=UP", a system command, or empty.
func funcCode(items []diag.Item) string {
	for _, it := range items {
		if it.Type == diag.ItemAPPL && it.ID == 0x0c && it.SID == 0x04 {
			return strings.TrimSpace(string(it.Value))
		}
	}
	return ""
}

// itemKeys is a one-line list of a frame's item keys, for the log.
func itemKeys(items []diag.Item) string {
	ks := make([]string, 0, len(items))
	for _, it := range items {
		ks = append(ks, it.Key())
	}
	return strings.Join(ks, " ")
}

// itemVals renders the values of the items that can carry an action — the
// OK-code, the UI events, the function-info varinfos — as key="text"|hex, so a
// key's identity (which arrives as a named UI event, not always a function
// code) is visible. Housekeeping items (session, user, system) are skipped.
func itemVals(items []diag.Item) string {
	var events, rest []string
	for _, it := range items {
		k := it.Key()
		if len(it.Value) == 0 {
			continue
		}
		switch {
		case strings.Contains(k, "UI_EVENT"), strings.Contains(k, "VARINFO.04"):
			// The action channels — the OK-code and the control events — go
			// first, since they are what a keypress or a click carries.
			events = append(events, fmt.Sprintf("%s=%s", k, showVal(it.Value)))
		case strings.Contains(k, "VARINFO.06"), strings.Contains(k, "VARINFO.08"),
			strings.Contains(k, "VARINFO.09"), strings.HasPrefix(k, "APPL DYNT"):
			rest = append(rest, fmt.Sprintf("%s=%s", k, showVal(it.Value)))
		}
	}
	return strings.Join(append(events, rest...), "  ")
}

// echoDiff compares this frame's screen/action items against the last one and
// returns the ones that changed, value included. Housekeeping items (session,
// user, system, RFC) are ignored, so what is left is what a keypress actually
// moved. It updates the stored snapshot in place.
func echoDiff(st *appState, items []diag.Item) string {
	cur := map[string]string{}
	for _, it := range items {
		k := it.Key()
		if strings.HasPrefix(k, "APPL ST_") || strings.HasPrefix(k, "APPL RFC_TR") ||
			k == "SES" || k == "EOM" || k == "CHL" {
			continue
		}
		cur[k] = fmt.Sprintf("%x", it.Value)
	}
	var changed []string
	for _, it := range items { // report in wire order
		k := it.Key()
		v, ok := cur[k]
		if !ok {
			continue
		}
		if st.prev[k] != v {
			changed = append(changed, fmt.Sprintf("%s=%s", k, showVal(it.Value)))
		}
		delete(cur, k) // so a repeated key is reported once
	}
	for k := range st.prev {
		if _, stillHere := indexKey(items, k); !stillHere {
			changed = append(changed, k+"(gone)")
		}
	}
	// Rebuild the snapshot from the frame.
	next := map[string]string{}
	for _, it := range items {
		k := it.Key()
		if strings.HasPrefix(k, "APPL ST_") || strings.HasPrefix(k, "APPL RFC_TR") ||
			k == "SES" || k == "EOM" || k == "CHL" {
			continue
		}
		next[k] = fmt.Sprintf("%x", it.Value)
	}
	st.prev = next
	return strings.Join(changed, "  ")
}

// indexKey reports whether any item carries the given key.
func indexKey(items []diag.Item, key string) (int, bool) {
	for i, it := range items {
		if it.Key() == key {
			return i, true
		}
	}
	return 0, false
}

// showVal is a value as printable text, with a hex tail for a short one so a
// non-printable byte is still readable.
func showVal(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		if c >= 0x20 && c < 0x7f {
			sb.WriteByte(c)
		} else {
			sb.WriteByte('.')
		}
	}
	s := sb.String()
	if len(b) <= 24 {
		return fmt.Sprintf("%q|%x", s, b)
	}
	return fmt.Sprintf("%q", s)
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
	// dur is this scene's length; 0 means use the demo's default (-scene-ms).
	// The login opener needs longer than a beat, so it sets its own.
	dur time.Duration
	// bare drops the demo's caption and footer for this scene, so the login
	// opener can look like a real logon screen and nothing else.
	bare bool
}

// demoScenes are the acts, each a different way of getting motion onto a real
// GUI, ordered from the lightest frame to the heaviest so the contrast in
// bytes-per-frame is easy to feel.
func demoScenes() []scene {
	return []scene{
		{"login", "a login form that sits, drifts a square, orbits, then multiplies", sceneLogin, 26 * time.Second, true},
		{"orbit", "3 widgets moved by coordinate, sized by depth", sceneOrbit, 0, false},
		{"equalizer", "a row of buttons whose Height is the graphics — bars", sceneEqualizer, 0, false},
		{"snake", "a label snake on a Lissajous path, with a fading trail", sceneSnake, 0, false},
		{"matrix", "sparse falling columns — the grid used lightly", sceneMatrix, 0, false},
		{"plasma", "LED plasma in the list channel — colour + letters", ledFallback, 0, false},
		{"rings", "LED rings in the list channel", ledFallback, 0, false},
		{"ball", "a bright ball bouncing on the LED field", ledFallback, 0, false},
		{"starfield", "the whole character grid redrawn every frame (~80 labels)", sceneStars, 0, false},
		{"icons", "a grid of real SAP icons, drawn by us via output fields", sceneIcons, 0, false},
	}
}

// loginFields places the four logon fields as loose elements at (top,left):
// no box around them, just the labels and inputs the way the real screen has
// them (label, then input 19 columns over), so a copy that moves or multiplies
// is fields on the canvas, not a widget in a frame. idx keeps each copy's
// field names distinct.
func loginFields(scr *frame.Screen, top, left, idx int) {
	if top < 0 {
		top = 0
	}
	if left < 0 {
		left = 0
	}
	scr.Text(top+0, left+0, "Client")
	scr.Input(top+0, left+19, 3, fmt.Sprintf("MANDT%d", idx), "001")
	scr.Text(top+2, left+0, "User")
	scr.Input(top+2, left+19, 12, fmt.Sprintf("BNAME%d", idx), "")
	scr.Text(top+3, left+0, "Password")
	scr.InputHidden(top+3, left+19, 12, fmt.Sprintf("BCODE%d", idx), "")
	scr.Text(top+5, left+0, "Logon Language")
	scr.Input(top+5, left+19, 2, fmt.Sprintf("LANGU%d", idx), "EN")
}

// nativeLogon draws the logon screen the way the capture showed it: the four
// loose fields at their real rows and columns, and the Information box to the
// right whose lines are output fields beginning with the @0S@ info icon — the
// same output-field-with-icon the real screen uses. Placeholder values only,
// never the captured credentials.
func nativeLogon(scr *frame.Screen) {
	// Exactly the capture's layout: fields at row 0/2/3/5, label col 1 and
	// input col 20, and the Information box at row 0 col 35, 56 wide and 19
	// tall — the real dimensions, so it is compact, not a page-tall panel.
	loginFields(scr, 0, 1, 0)
	scr.Frame(0, 35, 56, 19, "Information")
	// The welcome lines, wrapped to the box's inner width (col 37..90, so ~53
	// characters) so nothing is clipped; the @0S@ counts as its four bytes.
	scr.Output(1, 37, 53, "INFO0", "@0S@ ABAP Cloud Developer Trial 2023 initial shipment", false)
	scr.Output(3, 37, 53, "INFO1", "@0S@ Since ABAP Cloud Developer Trial is a free", false)
	scr.Output(4, 37, 53, "INFO2", "offering for education and demo purposes only,", false)
	scr.Output(5, 37, 53, "INFO3", "we offer it with SAP Community support. That", false)
	scr.Output(6, 37, 53, "INFO4", "means that no primary support is available", false)
	scr.Output(7, 37, 53, "INFO5", "for this product.", false)
	// The standard logon actions (New password, and so on) live in the GUI
	// status bar above the canvas, which we do not synthesize yet, so they are
	// not drawn here — a canvas button in their place read as wrong.
}

// orbitLogins draws count logon forms orbiting a centre at the given angle,
// spaced evenly round the circle.
func orbitLogins(scr *frame.Screen, ang float64, count int) {
	const cx, cy, rx, ry = 38.0, 9.0, 30.0, 6.0
	for i := 0; i < count; i++ {
		a := ang + float64(i)*(2.0*math.Pi/float64(count))
		left := int(cx + rx*math.Cos(a))
		top := int(cy + ry*math.Sin(a))
		loginFields(scr, top, left, i)
	}
}

// sceneLogin is the demo's opener: an ordinary-looking logon box that holds
// still for a few seconds, then drifts once round a square, then orbits, then
// becomes two, then three — a familiar thing behaving impossibly, all drawn by
// Go. Its phases are keyed to ts (seconds into the scene).
func sceneLogin(ts float64, scr *frame.Screen) {
	const homeTop, homeLeft = 4, 10
	const dx, dy = 44.0, 11.0 // the square's sides (wider than tall: cells are)
	switch {
	case ts < 6: // sit still, exactly like the real logon screen
		nativeLogon(scr)
	case ts < 12: // one lap round a square: right, down, left, up
		f := (ts - 6) / 6 * 4 // 0..4, one side per unit
		seg := int(f)
		fr := f - float64(seg)
		top, left := float64(homeTop), float64(homeLeft)
		switch seg {
		case 0:
			left = homeLeft + fr*dx
		case 1:
			left = homeLeft + dx
			top = homeTop + fr*dy
		case 2:
			left = homeLeft + (1-fr)*dx
			top = homeTop + dy
		default:
			top = homeTop + (1-fr)*dy
		}
		loginFields(scr, int(top), int(left), 0)
	case ts < 18: // orbit, one form
		orbitLogins(scr, (ts-12)*1.4, 1)
	case ts < 22: // two forms
		orbitLogins(scr, (ts-12)*1.4, 2)
	default: // three forms, then the demo moves on
		orbitLogins(scr, (ts-12)*1.4, 3)
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

// sceneIcons shows a grid of real SAP icons, drawn by us: each cell is an
// output field holding the @XX@ token, which a dynpro output field substitutes
// for the bitmap (a plain label does not — that is why the earlier version
// printed the tokens as text). The hex id sits under each icon, and a marker
// sweeps the grid so the scene keeps moving.
func sceneIcons(ts float64, scr *frame.Screen) {
	scr.Output(1, 2, 52, "IHDR", "@0S@ SAP icons drawn by Go via output fields", false)
	const cols, count = 12, 48
	sweep := int(ts*8.0) % count
	for code := 0; code < count; code++ {
		r := 3 + (code/cols)*3
		c := 4 + (code%cols)*8
		scr.Output(r, c, 4, fmt.Sprintf("IC%02X", code), fmt.Sprintf("@%02X@", code), false)
		label := fmt.Sprintf("%02X", code)
		if code == sweep {
			label = ">" + label // the sweeping marker
		}
		scr.Text(r+1, c, label)
	}
}

// ledSceneEffect maps a demo LED scene's name to its effect index (0 plasma,
// 1 rings, 2 ball), or -1 if the scene is not an LED scene. These scenes render
// in the list channel via the captured list wrapper, not as a dynpro screen.
func ledSceneEffect(name string) int {
	switch name {
	case "plasma":
		return 0
	case "rings":
		return 1
	case "ball":
		return 2
	}
	return -1
}

// ledFallback is what an LED scene draws when the capture has no list frame to
// wrap — a note instead of the effect.
func ledFallback(ts float64, scr *frame.Screen) {
	scr.Text(2, 2, "LED effect needs the capture's list frame")
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

// logonWrap is the captured logon screen kept as a backdrop: all of its items
// (the real menu bar, the New password status entry, the Information text) plus
// the index of the fields DYNT_ATOM, so the login scene can swap just the
// fields into it and inherit everything else, native.
type logonWrap struct {
	items      []diag.Item
	header     diag.Header
	fieldIdx   int // the DYNT_ATOM with the fields (and the native Information box)
	welcomeIdx int // the DYNT_ATOM with the welcome text, or -1
}

// loadLogonWrap finds the captured logon screen — the server frame whose
// DYNT_ATOM names RSYST-MANDT — and prepares it as that backdrop, noting the
// fields atom and the welcome-text atom so the login scene can keep the still
// frame verbatim, then swap the fields and drop the welcome once it moves.
func loadLogonWrap(cap *replay.Capture, log func(string, ...any)) (*logonWrap, bool) {
	for _, f := range cap.Server {
		m, err := diag.ParseMessage(f.Data, false)
		if err != nil {
			continue
		}
		items := diag.ParseItems(m.Body)
		fieldIdx, welcomeIdx := -1, -1
		for i, it := range items {
			if it.Type != diag.ItemAPPL4 || it.ID != 0x09 || it.SID != 0x02 {
				continue
			}
			v := string(it.Value)
			if strings.Contains(v, "RSYST-MANDT") {
				fieldIdx = i
			} else if strings.Contains(v, "ABAP Cloud") || strings.Contains(v, "INFO_TAB") {
				welcomeIdx = i
			}
		}
		if fieldIdx < 0 {
			continue
		}
		h := m.Header
		h.Compress = 0
		log("logon wrap located: server frame #%d, fields atom #%d, welcome atom #%d, %d items", f.Index, fieldIdx, welcomeIdx, len(items))
		return &logonWrap{items: items, header: h, fieldIdx: fieldIdx, welcomeIdx: welcomeIdx}, true
	}
	return nil, false
}

// loginFieldsScreen is the login scene when it runs inside the real logon
// backdrop: only the fields (and the Information box that lived in the same
// atom) — the menu, status and welcome text come from the capture. The phases
// are the same as sceneLogin: still, a square drift, then orbiting copies.
func loginFieldsScreen(ts float64) *frame.Screen {
	const homeTop, homeLeft = 0, 1
	const dx, dy = 44.0, 11.0
	scr := frame.New(27, 120)
	// No Information box here: the still frame is shown verbatim (native box),
	// and once the fields move the box is meant to be gone.
	switch {
	case ts < 6:
		loginFields(scr, homeTop, homeLeft, 0)
	case ts < 12:
		f := (ts - 6) / 6 * 4
		seg := int(f)
		fr := f - float64(seg)
		top, left := float64(homeTop), float64(homeLeft)
		switch seg {
		case 0:
			left = homeLeft + fr*dx
		case 1:
			left = homeLeft + dx
			top = homeTop + fr*dy
		case 2:
			left = homeLeft + (1-fr)*dx
			top = homeTop + dy
		default:
			top = homeTop + (1-fr)*dy
		}
		loginFields(scr, int(top), int(left), 0)
	case ts < 18:
		orbitLogins(scr, (ts-12)*1.4, 1)
	case ts < 22:
		orbitLogins(scr, (ts-12)*1.4, 2)
	default:
		orbitLogins(scr, (ts-12)*1.4, 3)
	}
	return scr
}

// demoRenderer cycles the scenes on a wall clock: each runs demoSceneMS
// milliseconds, then the next, then back to the first. The renderer ignores
// the frame counter push hands it and reads the real elapsed time, so the
// scenes advance by seconds, not by frames.
func demoRenderer(cap *replay.Capture, wrapFrame int, listWrap []byte, log func(string, ...any)) func(n int) []byte {
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
	logon, haveLogon := loadLogonWrap(cap, log)
	// Prepare the list wrapper so the LED scenes can render in the list channel:
	// keep everything but the list stream, remember where the stream goes.
	var listKeep []diag.Item
	var listHdr diag.Header
	listInsertAt := -1
	haveList := false
	if listWrap != nil {
		if lm, lerr := diag.ParseMessage(listWrap, false); lerr == nil {
			for _, it := range diag.ParseItems(lm.Body) {
				isList := it.Type == diag.ItemSBA || it.Type == diag.ItemSFE || it.Type == diag.ItemSLC ||
					(it.Type == diag.ItemAPPL && it.ID == 0x0c && it.SID == 0x0b)
				if isList {
					if listInsertAt < 0 {
						listInsertAt = len(listKeep)
					}
					continue
				}
				listKeep = append(listKeep, it)
			}
			if listInsertAt < 0 {
				listInsertAt = len(listKeep)
			}
			listHdr = lm.Header
			listHdr.Compress = 0
			haveList = true
			log("demo: list wrapper ready for LED scenes")
		}
	}
	scenes := demoScenes()
	def := time.Duration(demoSceneMS) * time.Millisecond
	if def <= 0 {
		def = 3 * time.Second
	}
	// Each scene's length: its own if it set one, else the default beat.
	durs := make([]time.Duration, len(scenes))
	var total time.Duration
	for i, s := range scenes {
		d := s.dur
		if d <= 0 {
			d = def
		}
		durs[i] = d
		total += d
	}
	var start time.Time
	lastScene := -1
	return func(n int) []byte {
		if start.IsZero() {
			start = time.Now()
		}
		pos := time.Since(start) % total
		idx, acc := 0, time.Duration(0)
		for i, d := range durs {
			if pos < acc+d {
				idx = i
				break
			}
			acc += d
		}
		ts := (pos - acc).Seconds()
		if idx != lastScene {
			log("scene %d/%d: %s (%s)", idx+1, len(scenes), scenes[idx].name, scenes[idx].approach)
			lastScene = idx
		}
		// The LED scenes render in the list channel via the list wrapper. The
		// effect time is quantised to ~180ms steps, so consecutive 80ms ticks
		// produce identical frames and the adaptive push (§push) skips them —
		// the LED runs at its own gentle rate while the dynpro scenes stay fast.
		if eff := ledSceneEffect(scenes[idx].name); eff >= 0 && haveList {
			t := float64(int(ts/0.18)) * 0.15
			mine := diag.EncodeListItems(ledSegmentsEff(eff, t))
			out := append(append(append([]diag.Item{}, listKeep[:listInsertAt]...), mine...), listKeep[listInsertAt:]...)
			msg, err := diag.EncodeMessage(listHdr, out, false)
			if err != nil {
				return nil
			}
			return msg
		}
		// The login scene runs inside the real captured logon frame when we
		// have one: swap just the fields atom, so the menu bar, the New
		// password status entry and the Information text are all the genuine
		// article, and only the fields move.
		if scenes[idx].name == "login" && haveLogon {
			out := append([]diag.Item{}, logon.items...)
			// Still: the captured frame verbatim — the real Information box and
			// all. Moving: swap the fields for the flying ones (no box) and
			// clear the welcome text, so the box is gone once it comes alive.
			if ts >= 6 {
				out[logon.fieldIdx].Value = loginFieldsScreen(ts).Encode()
				if logon.welcomeIdx >= 0 {
					out[logon.welcomeIdx].Value = nil
				}
			}
			msg, err := diag.EncodeMessage(logon.header, out, false)
			if err != nil {
				return nil
			}
			return msg
		}
		scr := frame.New(27, 120)
		scenes[idx].draw(ts, scr)
		if !scenes[idx].bare {
			scr.Text(24, 1, fmt.Sprintf("scene %d/%d  %-9s  approach: %s", idx+1, len(scenes), scenes[idx].name, scenes[idx].approach))
			scr.Text(25, 1, "F3/Back or close the window to stop")
		}
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

// iconAnimSegments is one frame of the icon animation, built from the list
// primitives: a scanner of LEDs sweeping back and forth, a traffic light that
// cycles green-yellow-red on the spot, and a red light bouncing round a box.
// Every glyph is a "@XX@" token in a coloured run placed by (row, col) — a
// moving picture drawn entirely in the list channel.
func iconAnimSegments(n int) []diag.ListSegment {
	var segs []diag.ListSegment
	segs = append(segs, diag.ListText(0, 2, diag.ColHeading, "OPEN-DIAG-GO-PRO  --  animated icons in the list channel"))

	// A KITT scanner: a green LED head with a two-step yellow trail, sweeping a
	// track and bouncing at the ends. Only the lit cells are drawn.
	const track = 22
	pos := triangle(float64(n), track-1)
	for k := 0; k < 3; k++ {
		i := pos - k
		if i < 0 || i >= track {
			continue
		}
		icon := diag.IconYellowLight
		if k == 0 {
			icon = diag.IconLEDGreen
		}
		segs = append(segs, diag.ListIcon(2, 6+i*2, icon))
	}
	segs = append(segs, diag.ListText(3, 6, diag.ColNormal, "scanner (LED head, yellow trail)"))

	// A traffic light cycling on the spot: one lamp lit at a time.
	lights := []string{diag.IconGreenLight, diag.IconYellowLight, diag.IconRedLight}
	segs = append(segs,
		diag.ListText(5, 6, diag.ColNormal, "cycle:"),
		diag.ListIcon(5, 14, lights[(n/6)%3]))

	// A red light bouncing round a box, so motion runs in two dimensions.
	const bw, bh = 30, 8
	bx := triangle(float64(n)*1.3, bw)
	by := triangle(float64(n)*0.7, bh)
	segs = append(segs, diag.ListIcon(7+by, 40+bx, diag.IconRedLight))

	segs = append(segs, diag.ListText(18, 2, diag.ColNormal,
		fmt.Sprintf("frame %d   F3/Back stops   (list-channel push test)", n)))
	return segs
}

// iconanimRenderer splices a fresh icon-animation list into the captured list
// wrapper each frame — the same splice colorlistRenderer does, but rebuilt per
// tick so the icons move. This is the experiment: whether the GUI's list
// processor accepts a server pushing new list frames on a timer.
func iconanimRenderer(wrap []byte, log func(string, ...any)) func(n int) []byte {
	return listPushRenderer(wrap, log, iconAnimSegments)
}

// listPushRenderer splices a fresh list (built by seg for frame n) into the
// captured list wrapper each tick — the shared engine behind every animated
// list-channel effect (icon animation, the LED display). Everything but the
// list stream is kept; ours goes in its place.
func listPushRenderer(wrap []byte, log func(string, ...any), seg func(n int) []diag.ListSegment) func(n int) []byte {
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
	h := m.Header
	h.Compress = 0
	return func(n int) []byte {
		mine := diag.EncodeListItems(seg(n))
		final := append(append(append([]diag.Item{}, keep[:insertAt]...), mine...), keep[insertAt:]...)
		payload, err := diag.EncodeMessage(h, final, false)
		if err != nil {
			return nil
		}
		return payload
	}
}

func ledRenderer(wrap []byte, log func(string, ...any)) func(n int) []byte {
	return listPushRenderer(wrap, log, ledSegments)
}

// ledRamp is the ASCII density ramp, light to dark, made of LETTERS (plus a
// space and two dots for the lightest steps) — letters give smoother tonal
// transitions than punctuation. The run's colour is the cell background and the
// glyph is dark foreground, so a denser letter darkens the cell: a halftone
// within the hue.
const ledRamp = " .:iclosnuaewmyqpdbkhOQMWNB"

// ledSpectrum is the vivid list colours ordered as a spectrum.
var ledSpectrum = []byte{diag.ColKey, diag.ColHeading, diag.ColPositive, diag.ColTotal, diag.ColGroup, diag.ColNegative}

// The LED grid is LOGICAL: ledRows x ledCols cells, each drawn as a bw x bh
// block of character cells (§ ledSegments), so the display is big and chunky
// while the run count stays tied to this logical resolution.
const ledRows, ledCols = 10, 22

var ledEffects = []string{"plasma", "rings", "ball"}

// ledCell is the colour and density glyph for one logical LED, for the current
// effect. Every effect returns smooth regions so the per-row RLE stays tight.
func ledCell(lr, lc int, t float64, eff int) (byte, byte) {
	switch eff {
	case 1: // concentric rings breathing out from the centre
		cx, cy := float64(ledCols)/2, float64(ledRows)/2
		d := math.Hypot(float64(lc)-cx, (float64(lr)-cy)*2)
		v := (math.Sin(d/2.2-t*2.0) + 1.0) / 2.0
		gi := clampi(int(v*float64(len(ledSpectrum))), 0, len(ledSpectrum)-1)
		di := clampi(int(v*float64(len(ledRamp))), 0, len(ledRamp)-1)
		return ledSpectrum[gi], ledRamp[di]
	case 2: // a bright ball bouncing on a dark field
		bx := triangle(t*9.0, ledCols-1)
		by := triangle(t*5.0, ledRows-1)
		d := math.Hypot(float64(lc-bx), float64(lr-by)*2)
		if d < 2.5 {
			return diag.ColNegative, ledRamp[len(ledRamp)-1] // solid ball
		}
		if d < 5.0 {
			return diag.ColTotal, ledRamp[len(ledRamp)/2] // halo
		}
		return diag.ColKey, ledRamp[0] // dark background (space)
	default: // plasma: hue and luminance from two sine fields
		fr, fc := float64(lr), float64(lc)
		hv := (math.Sin(fc/3.0+t) + math.Sin(fr/2.0-t) + math.Sin((fc+fr)/4.0+t*1.3) + 3.0) / 6.0
		lv := (math.Sin(fc/2.5-t*0.7) + math.Cos(fr/3.0+t*0.9) + 2.0) / 4.0
		gi := clampi(int(hv*float64(len(ledSpectrum))), 0, len(ledSpectrum)-1)
		di := clampi(int(lv*float64(len(ledRamp))), 0, len(ledRamp)-1)
		return ledSpectrum[gi], ledRamp[di]
	}
}

func clampi(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ledSegments is one frame of the LED display, **run-length encoded per row**:
// adjacent cells sharing a colour and letter collapse into one list run, so the
// big grid stays a few segments (the list channel stalls on hundreds). Each
// logical LED is drawn as a bw x bh character block; the effect cycles every
// few seconds among plasma, rings and a bouncing ball.
func ledSegments(n int) []diag.ListSegment {
	return ledSegmentsEff((n/45)%len(ledEffects), float64(n)*0.15)
}

// ledSegmentsEff renders one specific effect at time t — used both by the led
// mode (cycling) and by the demo's LED scenes (a fixed effect per scene).
func ledSegmentsEff(eff int, t float64) []diag.ListSegment {
	const bw, bh = 4, 2
	segs := []diag.ListSegment{
		diag.ListText(0, 2, diag.ColHeading, "OPEN-DIAG-GO-PRO  --  LED display: colour + letters (RLE)"),
	}
	for lr := 0; lr < ledRows; lr++ {
		type run struct {
			startLC, wLC int
			col, ch      byte
		}
		var runs []run
		for lc := 0; lc < ledCols; lc++ {
			col, ch := ledCell(lr, lc, t, eff)
			if k := len(runs) - 1; k >= 0 && runs[k].col == col && runs[k].ch == ch {
				runs[k].wLC++
			} else {
				runs = append(runs, run{lc, 1, col, ch})
			}
		}
		for b := 0; b < bh; b++ {
			sr := 2 + lr*bh + b
			for _, rn := range runs {
				segs = append(segs, diag.ListText(sr, 2+rn.startLC*bw, rn.col,
					strings.Repeat(string(rn.ch), rn.wLC*bw)))
			}
		}
	}
	segs = append(segs, diag.ListText(2+ledRows*bh+1, 2, diag.ColNormal,
		fmt.Sprintf("%s   %dx%d LEDs   F3/Back stops", ledEffects[eff], ledRows, ledCols)))
	return segs
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
	echo   []string          // the echo server's recent-keypress log
	prev   map[string]string // echo: last frame's item values, for the diff
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

// echoScreen shows what the GUI sent on the last few PAIs: the function code
// (a key's or a menu entry's), the header flags, how many control events came,
// and the item keys. It is how we read the keyboard — press a key, see the
// code it produced — with no on-screen buttons at all.
func echoScreen(st *appState) *frame.Screen {
	scr := frame.New(27, 120).
		Frame(0, 0, 110, 24, "odgp echo  --  press keys; this is what SAP GUI sent back").
		Text(2, 2, "PAIs received").
		Number(2, 20, 8, "GV_N", st.turns).
		Text(3, 2, "press function keys, Enter, menu shortcuts (Ctrl/Shift+F..) — most recent first:")
	row := 5
	for i := len(st.echo) - 1; i >= 0 && row < 24; i-- {
		scr.Text(row, 2, st.echo[i])
		row++
	}
	scr.Text(25, 2, "close the window (the [X] / system close sends /i) to exit")
	return scr
}

// echoRespond renders the echo screen into the located screen frame.
func echoRespond(cap *replay.Capture, wrapFrame int, st *appState) ([]byte, bool) {
	out := staticRespondWrap(cap, wrapFrame, echoScreen(st))
	return out, out != nil
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
		return demoRenderer(cap, screenFrame, listWrap, log)
	case "iconanim":
		return iconanimRenderer(listWrap, log)
	case "led":
		return ledRenderer(listWrap, log)
	}
	return patchRenderer(cap, pushFrame, log)
}

func push(ctx context.Context, c net.Conn, render func(n int) []byte, cadence time.Duration, stop <-chan struct{}, log func(string, ...any)) {
	n := 0
	t := time.NewTicker(cadence)
	defer t.Stop()
	var last []byte // the last frame actually sent, for adaptive skipping
	sent, skipped := 0, 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-t.C:
		}
		n++
		// Adaptive rate: render every tick, but only SEND when the frame
		// differs from the last one sent. A static screen (the still logon)
		// is drawn once and then goes quiet; a moving scene sends every frame.
		data := render(n)
		if data == nil {
			continue
		}
		if bytes.Equal(data, last) {
			skipped++
			continue
		}
		frame, err := ni.EncodeFrame(data)
		if err != nil {
			return
		}
		if _, err := c.Write(frame); err != nil {
			log("push: %v", err)
			return
		}
		last = data
		sent++
		if sent%20 == 1 {
			log("-> pushed frame %d (%d bytes; %d sent, %d skipped)", n, len(data), sent, skipped)
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
