// Package lsd is the DIAG stub as a library: a rogue dispatcher that answers
// SAP GUI with one still screen (or the light-show) and needs no external
// file — the wrapper frames are embedded and scrubbed. cmd/lsd is the
// command over it; cmd/osd-up mounts it beside the workbench and the RFC
// bridge as one command.
package lsd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oisee/open-rfc-go/ni"

	"github.com/oisee/open-diag-go/pkg/diag"
	"github.com/oisee/open-diag-go/pkg/frame"
)

// Options is what a stub needs besides a listener.
type Options struct {
	// SID names the system in the screen identity fields; empty defaults to LSD.
	// Values are space-padded or truncated to each field's wire width.
	SID string
	// Stub names the still screen: tape (the default) | boot | c64 | guru |
	// rotate; an empty string plays the light-show instead.
	Stub string
	// Hold is how long a stub screen stays before the session ends itself,
	// the way a real F8 session ends; zero is twelve seconds; a negative
	// value disables the hold timer (IdleTimeout still applies).
	Hold time.Duration
	// MaxPayloadLength bounds an incoming NI payload, excluding its four-byte
	// prefix. Zero defaults to 1 MiB; values outside 1..2^32-1 are invalid.
	MaxPayloadLength int
	// MaxConnections caps active sockets, including those waiting after EOP.
	// Zero defaults to 64; negative values are invalid.
	MaxConnections int
	// IdleTimeout bounds inactivity from acceptance, including before the
	// first frame and after EOP. Zero defaults to 30 seconds; negative disables it.
	// Reads and responses refresh it, including partial frames and NI controls.
	IdleTimeout time.Duration
	// List draws the stub in the classic list channel: colour, a border,
	// and more to go wrong. The plain dynpro is the default.
	List bool
	// SceneMS is how long each light-show scene runs; zero is 3000.
	SceneMS int
	// Cadence is the light-show frame interval; zero is 80 ms, floored at 60.
	Cadence time.Duration
	// Log receives the stub's lines; nil writes them to stderr as before.
	Log func(format string, args ...any)
}

func (o Options) normalised() (Options, time.Duration) {
	if o.MaxPayloadLength == 0 {
		o.MaxPayloadLength = 1 << 20
	}
	if o.MaxConnections == 0 {
		o.MaxConnections = 64
	}
	if o.IdleTimeout == 0 {
		o.IdleTimeout = 30 * time.Second
	}
	if o.SID == "" {
		o.SID = "LSD"
	}
	hold := o.Hold
	switch {
	case hold == 0:
		hold = 12 * time.Second
	case hold < 0:
		hold = 0
	}
	if o.SceneMS <= 0 {
		o.SceneMS = 3000
	}
	if o.Cadence <= 0 {
		o.Cadence = 80 * time.Millisecond
	}
	if o.Cadence < 60*time.Millisecond {
		o.Cadence = 60 * time.Millisecond
	}
	if o.Log == nil {
		o.Log = func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
	}
	return o, hold
}

// Serve answers every SAP GUI that connects on ln until the context ends.
// Cancellation closes the listener and all active connections.
func Serve(ctx context.Context, ln net.Listener, o Options) error {
	o, hold := o.normalised()
	if o.MaxPayloadLength < 0 || uint64(o.MaxPayloadLength) > uint64(^uint32(0)) || o.MaxConnections < 0 {
		return fmt.Errorf("lsd: invalid payload or connection limit")
	}
	a, err := loadAsset()
	if err != nil {
		return fmt.Errorf("lsd: loading embedded show: %w", err)
	}
	a.setSID(o.SID)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	active := make(map[net.Conn]struct{})
	var workers sync.WaitGroup
	defer workers.Wait()
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		for c := range active {
			c.Close()
		}
	}()
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	// Connections are numbered so a rotating stub can walk its chronology,
	// one screen per GUI that arrives.
	conn := 0
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		mu.Lock()
		if len(active) >= o.MaxConnections {
			mu.Unlock()
			c.Close()
			continue
		}
		active[c] = struct{}{}
		mu.Unlock()
		conn++
		workers.Add(1)
		go func(number int) {
			defer workers.Done()
			defer func() { mu.Lock(); delete(active, c); mu.Unlock() }()
			serve(ctx, c, a, o, number, hold)
		}(conn - 1)
	}
}

// Instance is the SAP instance number a dispatcher address belongs to: the
// low two digits of its port, 32NN.
func Instance(listen string) int { return instanceFromListen(listen) }

// LocalIPv4 is the address to put in a SAP GUI connection on another machine.
func LocalIPv4() string { return localIPv4() }

// Banner prints the connection a SAP GUI needs, as the command does.
// The optional SID defaults to LSD.
func Banner(ip string, inst int, listen string, sid ...string) { printBanner(ip, inst, listen, sid...) }

func printBanner(ip string, inst int, listen string, sid ...string) {
	systemID := "LSD"
	if len(sid) > 0 && sid[0] != "" {
		systemID = sid[0]
	}
	port := listen
	if strings.HasPrefix(port, ":") {
		port = port[1:]
	}
	if h, p, err := net.SplitHostPort(listen); err == nil {
		port = p
		if h != "" {
			ip = h // an explicit bind host overrides the guessed LAN IP
		}
	}
	fmt.Println("odgp light-show ready.")
	fmt.Println(`In SAP GUI, add a connection → "Custom Application Server":`)
	fmt.Printf("   Application Server:  %s\n", ip)
	fmt.Printf("   Instance Number:     %02d\n", inst)
	fmt.Printf("   System ID:           %s\n", systemID)
	fmt.Printf("Then connect (or use the raw sapgui host %s port %s).\n", ip, port)
	fmt.Println()
}

// localIPv4 returns the machine's first non-loopback, non-link-local IPv4, or a
// placeholder when none is found.
func localIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "<your-LAN-IP>"
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			ip4 := ip.To4()
			if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
				continue // skips 127.0.0.1 and 169.254.*
			}
			return ip4.String()
		}
	}
	return "<your-LAN-IP>"
}

// instanceFromListen derives the SAP instance number from the port's low two
// digits (sapdiag port = 3200 + instance).
func instanceFromListen(listen string) int {
	_, p, err := net.SplitHostPort(listen)
	if err != nil {
		p = strings.TrimPrefix(listen, ":")
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		return 0
	}
	return n % 100
}

// serve plays the light-show to one connected GUI: on the first client frame it
// sends the opening scene and starts pushing frames on a timer; a later frame
// freezes the show; the window-close (/i) gets the two-popup joke, then a clean
// session end.
func serve(ctx context.Context, c net.Conn, a *asset, o Options, conn int, hold time.Duration) {
	cadence, stub, dynpro := o.Cadence, o.Stub, !o.List
	defer c.Close()
	log := func(format string, x ...any) {
		o.Log("[%s] "+format, append([]any{c.RemoteAddr()}, x...)...)
	}
	log("connected")

	idleDeadline := time.Time{}
	refreshIdle := func() {
		if o.IdleTimeout > 0 {
			idleDeadline = time.Now().Add(o.IdleTimeout)
		}
	}
	refreshIdle()

	send := func(what string, data []byte) error {
		frameBytes, err := ni.EncodeFrame(data)
		if err != nil {
			return err
		}
		_ = c.SetWriteDeadline(idleDeadline)
		if _, err := c.Write(frameBytes); err != nil {
			return err
		}
		refreshIdle()
		log("-> %s, %d bytes", what, len(data))
		return nil
	}
	// Like socket.end(sessionEnd), send EOP and half-close the write side.
	// Retain the connection slot until peer EOF, inactivity, or shutdown.
	dec, _ := ni.NewFrameDecoder(o.MaxPayloadLength)
	defer dec.Reset()
	closeSession := func() {
		h := diag.Header{ComFlag: diag.FlagTermEOC | diag.FlagTermEOP, MsgInfo: 0x01}
		if err := send("session end (EOP)", h.Bytes()); err != nil {
			return
		}
		if cw, ok := c.(interface{ CloseWrite() error }); ok {
			if err := cw.CloseWrite(); err != nil {
				return
			}
		}
		buf := make([]byte, 4096)
		for {
			_ = c.SetReadDeadline(idleDeadline)
			n, err := c.Read(buf)
			if n > 0 {
				refreshIdle()
				if _, derr := dec.Push(buf[:n]); derr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}

	const screenFrame = 0 // the counter (GV_TICKS) wrapper is at capture index 0
	rend := demoRenderer(a.cap, screenFrame, a.listWrap, o.SceneMS, log)

	buf := make([]byte, 64<<10)
	var pushing chan struct{}
	defer func() {
		if pushing != nil {
			close(pushing)
		}
	}()
	var holdDeadline time.Time
	armHold := func() {
		if hold > 0 {
			holdDeadline = time.Now().Add(hold)
		}
	}
	seenFirst := false
	jokeStep := 0

	for {
		deadline := idleDeadline
		if !holdDeadline.IsZero() && (deadline.IsZero() || holdDeadline.Before(deadline)) {
			deadline = holdDeadline
		}
		_ = c.SetReadDeadline(deadline)
		n, err := c.Read(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() && !holdDeadline.IsZero() &&
				!time.Now().Before(holdDeadline) && (idleDeadline.IsZero() || holdDeadline.Before(idleDeadline)) {
				log("stub: shown for %s, ending the session", hold)
				closeSession()
			}
			return
		}
		refreshIdle()
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
			// The client's first frame carries a 200-byte DP header and no
			// later one does. "Have we answered yet" is the only way to know
			// that, and it must be its own flag: the light-show could infer
			// it from `pushing`, because the show starts on the first frame
			// and never stops, but a stub starts nothing, so pushing stayed
			// nil forever and every frame had 200 bytes cut off its front.
			// The header then read at the wrong offset — compress=72 on a
			// frame that is not compressed — the body failed to decompress,
			// and we answered a frame we had not understood with a screen.
			// That is what took SAP GUI down a few frames later.
			first := !seenFirst
			items, perr := parseClientMessage(payload, first)
			if perr != nil {
				log("protocol-error: %v", perr)
				return // JS destroys the socket without an error frame or screen.
			}
			seenFirst = true
			log("<- client frame, %d bytes, %d items", len(payload), len(items))

			// Window close: the joke, then a clean end.
			if stub == "" && jokeStep == 0 && isClose(items) {
				if pushing != nil {
					close(pushing)
					pushing = nil
				}
				if jp := jokePopup1(a.popup); jp != nil {
					jokeStep = 1
					_ = send("joke popup 1", jp)
					continue
				}
				closeSession()
				return
			}
			if jokeStep == 1 {
				if jp := jokePopup2(a.popup); jp != nil {
					jokeStep = 2
					_ = send("joke popup 2", jp)
					continue
				}
				closeSession()
				return
			}
			if jokeStep == 2 {
				closeSession()
				return
			}
			_ = isNewWindow // window handling kept minimal here

			// A stub: the same still screen for every frame, no show. What a
			// system with no dialog programs says when something lands on
			// its dispatcher — the window is a screen, not a blank.
			if stub != "" {
				if isExit(items) {
					closeSession()
					return
				}
				// the list channel first: it has the monospace grid, the
				// colour bands and therefore a border. The dynpro is the
				// fallback, and what -stub-dynpro asks for.
				if !dynpro {
					if out := listStubFrame(a.listWrap, stubListSegments(stub, conn), first); out != nil {
						_ = send("stub "+stub+" (list)", out)
						armHold()
						continue
					}
				}
				if out := staticRespondWrap(a.cap, screenFrame, stubScreen(stub, conn)); out != nil {
					_ = send("stub "+stub+" (dynpro)", out)
					armHold()
				}
				continue
			}

			// First frame: open the show and start pushing.
			if pushing == nil {
				_ = send("light-show: opening scene", rend(0))
				pushing = make(chan struct{})
				go push(ctx, c, rend, cadence, pushing, log)
				continue
			}
			// A later frame is the user pressing a key: freeze the show.
			close(pushing)
			pushing = nil
			log("show stopped by the user")
			frozen := frame.New(27, 120).
				Text(1, 2, "light-show stopped").
				Text(3, 2, "close the window to exit")
			if out := staticRespondWrap(a.cap, screenFrame, frozen); out != nil {
				_ = send("show stopped", out)
			}
		}
	}
}

// push renders every tick and sends only when the frame changed since the last
// one sent (adaptive skip-on-unchanged), the same pacing the demo uses.
func push(ctx context.Context, c net.Conn, render func(n int) []byte, cadence time.Duration, stop <-chan struct{}, log func(string, ...any)) {
	n := 0
	t := time.NewTicker(cadence)
	defer t.Stop()
	var last []byte
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
		data := render(n)
		if data == nil {
			continue
		}
		if bytes.Equal(data, last) {
			skipped++
			continue
		}
		frameBytes, err := ni.EncodeFrame(data)
		if err != nil {
			return
		}
		if _, err := c.Write(frameBytes); err != nil {
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

// parseClientMessage applies the JS tape server's admission rules. ParseItems is
// deliberately tolerant for capture inspection; Raw items are errors on a server.
func parseClientMessage(payload []byte, first bool) ([]diag.Item, error) {
	if first {
		if len(payload) < diag.DPHeaderLen+diag.HeaderLen {
			return nil, fmt.Errorf("short DIAG hello: %d bytes", len(payload))
		}
		if !bytes.Equal(payload[:4], []byte{0xff, 0xff, 0xff, 0xff}) {
			return nil, fmt.Errorf("DIAG hello has no dispatcher route prefix")
		}
		if payload[diag.DPHeaderLen+1]&diag.FlagTermINI == 0 {
			return nil, fmt.Errorf("DIAG hello has no INI flag")
		}
		if payload[diag.DPHeaderLen+7] != 0 {
			return nil, fmt.Errorf("compressed DIAG hello is unsupported")
		}
	}
	m, err := diag.ParseMessage(payload, first)
	if err != nil {
		return nil, err
	}
	items := diag.ParseItems(m.Body)
	if first && len(items) == 0 {
		return nil, fmt.Errorf("DIAG hello has no items")
	}
	for _, item := range items {
		if item.Status == diag.Raw {
			return nil, fmt.Errorf("malformed DIAG item at offset %d", item.Offset)
		}
	}
	return items, nil
}
