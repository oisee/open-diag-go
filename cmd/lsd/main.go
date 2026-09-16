// lsd — the DIAG stub, standalone: a rogue SAP GUI server that answers one
// still screen (or the light-show) to any SAP GUI that connects, with no
// external files. The library is pkg/lsd; this is the command over it.
//
//	lsd                 # listen on :3232 (SAP instance 32), print the connect banner
//	lsd -listen :3201   # instance 01
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/oisee/open-diag-go/pkg/lsd"
)

func main() {
	listen := flag.String("listen", ":3232", "address SAP GUI connects to; the low two digits are the SAP instance number")
	sceneMS := flag.Int("scene-ms", 3000, "how long each scene runs, in milliseconds (wall clock)")
	hold := flag.Duration("stub-hold", 12*time.Second, "how long a stub screen stays before the session ends itself; 0 waits for the user")
	stub := flag.String("stub", "tape", "still-screen mode: tape | boot | c64 | guru | rotate; empty plays the light-show")
	useList := flag.Bool("stub-list", false, "draw the stub in the classic list channel: colour, a border, and more to go wrong")
	cadenceMS := flag.Int("push-ms", 80, "frame cadence in milliseconds (floored at 60)")
	flag.Parse()

	lsd.Banner(lsd.LocalIPv4(), lsd.Instance(*listen), *listen)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lsd:", err)
		os.Exit(1)
	}
	h := *hold
	if h == 0 {
		h = -1 // the command's 0 means "wait for the user"; the library's zero is the default
	}
	fmt.Fprintf(os.Stderr, "lsd: listening on %s; stub %q; scene %d ms\n", *listen, *stub, *sceneMS)
	if err := lsd.Serve(ctx, ln, lsd.Options{Stub: *stub, Hold: h, List: *useList, SceneMS: *sceneMS, Cadence: time.Duration(*cadenceMS) * time.Millisecond}); err != nil {
		fmt.Fprintln(os.Stderr, "lsd:", err)
		os.Exit(1)
	}
}
