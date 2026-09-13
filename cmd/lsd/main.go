// lsd — the odgp light-show, standalone. A pure-Go rogue SAP GUI DIAG server
// that plays the demo scene sequence to any SAP GUI that connects. It needs NO
// external files: the wrapper DIAG frames it splices its scenes into are
// embedded (compressed, and scrubbed of every identifier — see asset.go and the
// scratch generator that built show.bin).
//
//	lsd                 # listen on :3232 (SAP instance 32), print the connect banner
//	lsd -listen :3200   # instance 00
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/oisee/open-rfc-go/ni"

	"github.com/oisee/open-diag-go/pkg/diag"
	"github.com/oisee/open-diag-go/pkg/frame"
)

func main() {
	listen := flag.String("listen", ":3232", "address SAP GUI connects to; the low two digits are the SAP instance number")
	sceneMS := flag.Int("scene-ms", 3000, "how long each scene runs, in milliseconds (wall clock)")
	cadenceMS := flag.Int("push-ms", 80, "frame cadence in milliseconds (floored at 60)")
	flag.Parse()

	demoSceneMS = *sceneMS

	a, err := loadAsset()
	if err != nil {
		fmt.Fprintln(os.Stderr, "lsd: loading embedded show:", err)
		os.Exit(1)
	}

	ip := localIPv4()
	inst := instanceFromListen(*listen)
	printBanner(ip, inst, *listen)

	cad := time.Duration(*cadenceMS) * time.Millisecond
	if cad < 60*time.Millisecond {
		cad = 60 * time.Millisecond
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lsd:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "lsd: listening on %s; scene %d ms; cadence %s\n", *listen, *sceneMS, cad)
	go func() { <-ctx.Done(); ln.Close() }()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			fmt.Fprintln(os.Stderr, "lsd: accept:", err)
			continue
		}
		go serve(ctx, c, a, cad)
	}
}

func printBanner(ip string, inst int, listen string) {
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
	fmt.Println("   System ID:           LSD")
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
func serve(ctx context.Context, c net.Conn, a *asset, cadence time.Duration) {
	defer c.Close()
	log := func(format string, x ...any) {
		fmt.Fprintf(os.Stderr, "[%s] "+format+"\n", append([]any{c.RemoteAddr()}, x...)...)
	}
	log("connected")

	send := func(what string, data []byte) error {
		frameBytes, err := ni.EncodeFrame(data)
		if err != nil {
			return err
		}
		if _, err := c.Write(frameBytes); err != nil {
			return err
		}
		log("-> %s, %d bytes", what, len(data))
		return err
	}
	closeSession := func() {
		h := diag.Header{ComFlag: diag.FlagTermEOC | diag.FlagTermEOP, MsgInfo: 0x01}
		if fr, err := ni.EncodeFrame(h.Bytes()); err == nil {
			_, _ = c.Write(fr)
		}
		log("-> session end (EOP)")
	}

	const screenFrame = 0 // the counter (GV_TICKS) wrapper is at capture index 0
	rend := demoRenderer(a.cap, screenFrame, a.listWrap, log)

	dec, _ := ni.NewFrameDecoder(64 << 20)
	buf := make([]byte, 64<<10)
	var pushing chan struct{}
	jokeStep := 0

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
			// The client's first frame carries a 200-byte DP header.
			first := jokeStep == 0 && pushing == nil
			m, perr := diag.ParseMessage(payload, first && len(payload) > diag.DPHeaderLen)
			items := []diag.Item(nil)
			if perr == nil {
				items = diag.ParseItems(m.Body)
				log("<- client frame, %d bytes, %d items", len(payload), len(items))
			} else {
				log("<- client frame, %d bytes (%v)", len(payload), perr)
			}

			// Window close: the joke, then a clean end.
			if perr == nil && jokeStep == 0 && isClose(items) {
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
