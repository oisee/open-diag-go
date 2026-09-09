// tap sits between a real SAP GUI and a sandbox's dispatcher port and writes
// every NI frame of both directions to a JSONL file, with the time it went
// by. It forwards bytes unchanged; the decoding is lens's job.
//
//	tap -listen :3200 -target sandbox.example:3200 -dump captures/probe.jsonl
//
// Then point SAP GUI at the machine running tap, system number 00. DIAG is
// compressed but not encrypted, so this works only without SNC — fine on a
// sandbox, and not to be pointed at anything else.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/oisee/open-rfc-go/sniffer"
)

// Line is one captured frame.
type Line struct {
	At    time.Time `json:"at"`
	Dir   string    `json:"dir"`
	Conn  int       `json:"conn"`
	Index int       `json:"index"`
	Len   int       `json:"len"`
	Hex   string    `json:"hex"`
}

func main() {
	listen := flag.String("listen", ":3200", "address SAP GUI connects to")
	target := flag.String("target", "", "the sandbox's dispatcher, host:port (port 32xx)")
	dump := flag.String("dump", "capture.jsonl", "file the frames go to, one JSON line each")
	flag.Parse()
	if *target == "" {
		fmt.Fprintln(os.Stderr, "tap: -target host:port is required")
		os.Exit(2)
	}
	f, err := os.OpenFile(*dump, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tap:", err)
		os.Exit(1)
	}
	defer f.Close()
	var mu sync.Mutex
	enc := json.NewEncoder(f)
	frames := 0
	p := &sniffer.Proxy{
		Target: *target,
		Label:  "disp",
		Observe: func(fr sniffer.Frame) {
			line := Line{At: time.Now(), Dir: string(fr.Direction), Conn: fr.ConnID, Index: fr.Index, Len: len(fr.Payload), Hex: hex.EncodeToString(fr.Payload)}
			mu.Lock()
			_ = enc.Encode(&line)
			frames++
			n := frames
			mu.Unlock()
			fmt.Fprintf(os.Stderr, "\r%d frames (%s #%d %s %d bytes)   ", n, fr.Direction, fr.ConnID, fr.Note, len(fr.Payload))
		},
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	fmt.Fprintf(os.Stderr, "tap: %s -> %s, frames to %s\n", *listen, *target, *dump)
	if err := p.Serve(ctx, *listen); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "tap:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr)
}
