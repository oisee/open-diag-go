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
	"time"

	"github.com/oisee/open-rfc-go/ni"

	"github.com/oisee/open-diag-go-pro/pkg/diag"
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
			if mode == "flash" && pushing == nil {
				if f, ok := cap.ServerFrame(screenFrame); ok {
					_ = send(fmt.Sprintf("the probe's screen, no handshake (capture S->C #%d)", screenFrame), f.Data)
				}
				pushing = make(chan struct{})
				go push(ctx, c, cap, pushFrame, cadence, log)
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
				go push(ctx, c, cap, pushFrame, cadence, log)
			}
		}
	}
}

var counterText = regexp.MustCompile(`\x20{4,9}[0-9]{1,6}\x20`)

// push sends the probe's pushed frame again and again, the counter in it
// replaced, the header's stat=f0 kept as the capture had it.
func push(ctx context.Context, c net.Conn, cap *replay.Capture, pushFrame int, cadence time.Duration, log func(string, ...any)) {
	f, ok := cap.ServerFrame(pushFrame)
	if !ok {
		log("no server frame #%d to push", pushFrame)
		return
	}
	plain, err := replay.Plain(f.Data)
	if err != nil {
		log("push frame: %v", err)
		return
	}
	loc := counterText.FindIndex(plain)
	if loc == nil {
		log("push frame: no counter text found; pushing it unchanged")
	}
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
		out := append([]byte{}, plain...)
		if loc != nil {
			width := loc[1] - loc[0] - 1
			text := fmt.Sprintf("%*d ", width, n)
			copy(out[loc[0]:loc[1]], text)
		}
		frame, err := ni.EncodeFrame(out)
		if err != nil {
			return
		}
		if _, err := c.Write(frame); err != nil {
			log("push: %v", err)
			return
		}
		if n%10 == 1 {
			log("-> pushed frame %d (%d bytes)", n, len(out))
		}
	}
}
