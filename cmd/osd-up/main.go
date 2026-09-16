// osd-up: the local system as one command.
//
// Three processes used to be three terminals: the workbench (test/run.mjs of
// open-steamgate — the ADT façade with the transpiled system in a child it
// supervises), the RFC bridge that lets a stock Eclipse reach it over the
// gateway port, and the DIAG stub that answers SAP GUI on the dispatcher
// port. This starts the workbench, waits for its discovery document, mounts
// the bridge and the stub in this process on the ports the instance number
// implies, prints the one banner a project needs, and takes everything down
// on Ctrl-C. It is the "up" of the sidecar in open-steamgate's
// docs/architecture-split.md, with the workbench still under Node until the
// Bun binary exists.
//
//	osd-up -root ../open-steamgate                # instance 01: http 3030, RFC 3301, DIAG 3201
//	osd-up -root ../open-steamgate -instance 6    # http 3036, RFC 3306, DIAG 3206
//	osd-up -root ../open-steamgate -dev           # and the dev loop: a save rebuilds and recycles
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/oisee/open-diag-go/pkg/lsd"
	"github.com/oisee/open-rfc-go/pkg/adtbridge"
)

func main() {
	root := flag.String("root", ".", "the open-steamgate tree to serve")
	httpPort := flag.Int("http", 0, "the workbench's HTTP port; 0 means 3030 + instance")
	instance := flag.Int("instance", 1, "SAP instance number NN: RFC on 33NN, DIAG on 32NN")
	sid := flag.String("sid", "OSD", "the system id the workbench answers with")
	stub := flag.String("stub", "tape", "the DIAG still screen: tape | boot | c64 | guru | rotate; empty plays the light-show")
	hold := flag.Duration("stub-hold", 12*time.Second, "how long the screen stays before the session ends itself")
	node := flag.String("node", "node", "the Node executable")
	dev := flag.Bool("dev", false, "the dev loop: a save on disk is a check, a build and a recycle")
	database := flag.String("db", "", "the rows' file (STG_DB_PATH); empty means the tree's default")
	packages := flag.String("local-packages", "", "OSD_LOCAL_PACKAGES: which root packages sit under $TMP")
	attach := flag.String("attach", "", "mount the two doors against a workbench that is already running at this URL, and start none")
	noRFC := flag.Bool("no-rfc", false, "do not mount the RFC bridge")
	noDiag := flag.Bool("no-diag", false, "do not mount the DIAG stub")
	flag.Parse()

	if *instance < 0 || *instance > 99 {
		fmt.Fprintln(os.Stderr, "osd-up: the instance number is two digits")
		os.Exit(2)
	}
	port := *httpPort
	if port == 0 {
		port = 3030 + *instance
	}
	say := func(format string, args ...any) { fmt.Fprintf(os.Stderr, "osd-up: "+format+"\n", args...) }

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// -attach: the bridge and the stub as one process over a workbench
	// somebody else runs — the proxy alone, which is what a laptop with the
	// façade already up wants
	backend := fmt.Sprintf("http://127.0.0.1:%d", port)
	exited := make(chan error, 1)
	var cmd *exec.Cmd
	if *attach != "" {
		backend = *attach
		say("attaching to %s", backend)
	} else {
		cmd = startWorkbench(ctx, *node, *root, port, *sid, *dev, *database, *packages, say)
		go func() { exited <- cmd.Wait() }()
		waitForDiscovery(ctx, backend, exited, say, stop)
	}

	rfcAddr := fmt.Sprintf(":33%02d", *instance)
	diagAddr := fmt.Sprintf(":32%02d", *instance)
	if !*noRFC {
		ln, err := net.Listen("tcp", rfcAddr)
		if err != nil {
			say("RFC %s: %v", rfcAddr, err)
			stop()
			os.Exit(1)
		}
		go func() {
			err := adtbridge.Serve(ctx, ln, adtbridge.Options{
				Backend: adtbridge.Backend{URL: backend},
				Log:     func(format string, args ...any) { fmt.Fprintf(os.Stderr, "rfc  | "+format+"\n", args...) },
			})
			if err != nil {
				say("RFC bridge: %v", err)
			}
		}()
	}
	if !*noDiag {
		ln, err := net.Listen("tcp", diagAddr)
		if err != nil {
			say("DIAG %s: %v", diagAddr, err)
			stop()
			os.Exit(1)
		}
		go func() {
			err := lsd.Serve(ctx, ln, lsd.Options{
				Stub: *stub,
				Hold: *hold,
				Log:  func(format string, args ...any) { fmt.Fprintf(os.Stderr, "diag | "+format+"\n", args...) },
			})
			if err != nil {
				say("DIAG stub: %v", err)
			}
		}()
	}

	ip := lsd.LocalIPv4()
	fmt.Fprintf(os.Stderr, `
osd-up: the system is up.

  Eclipse, New ABAP Project -> Custom Application Server
    System ID:           %s
    Application Server:  %s
    Instance Number:     %02d          (RFC on %s%s)
  SAP GUI, System Entry Properties: the same three values (DIAG on %s%s)
  HTTP(S) for a client that speaks it: %s

  Ctrl-C stops all of it.

`, *sid, ip, *instance, ip, rfcAddr, ip, diagAddr, backend)

	select {
	case <-ctx.Done():
	case err := <-exited:
		say("the workbench exited: %v", err)
		stop()
	}
	say("stopping")
	if cmd != nil {
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
		}
	}
	say("down")
}

// startWorkbench runs open-steamgate's test/run.mjs under Node, its lines
// relayed with a prefix, SIGTERM on cancel so it commits its database first.
func startWorkbench(ctx context.Context, node, root string, port int, sid string, dev bool, database, packages string, say func(string, ...any)) *exec.Cmd {
	cmd := exec.CommandContext(ctx, node, "test/run.mjs")
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("STG_PORT=%d", port),
		"STG_ADT_SID="+sid,
	)
	if dev {
		cmd.Env = append(cmd.Env, "STG_DEV=1")
	}
	if database != "" {
		cmd.Env = append(cmd.Env, "STG_DB_PATH="+database)
	}
	if packages != "" {
		cmd.Env = append(cmd.Env, "OSD_LOCAL_PACKAGES="+packages)
	}
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 8 * time.Second
	out, _ := cmd.StdoutPipe()
	errp, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		say("starting the workbench: %v", err)
		os.Exit(1)
	}
	go relay("osd  | ", out)
	go relay("osd  | ", errp)
	return cmd
}

// waitForDiscovery is how a workbench says it is up: its discovery document.
func waitForDiscovery(ctx context.Context, backend string, exited <-chan error, say func(string, ...any), stop func()) {
	discovery := backend + "/sap/bc/adt/core/discovery"
	say("waiting for %s", discovery)
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(3 * time.Minute)
	for {
		select {
		case err := <-exited:
			say("the workbench exited before it was up: %v", err)
			os.Exit(1)
		case <-ctx.Done():
			os.Exit(0)
		default:
		}
		if resp, err := client.Get(discovery); err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return
			}
		}
		if time.Now().After(deadline) {
			say("the workbench did not come up in three minutes")
			stop()
			os.Exit(1)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func relay(prefix string, r io.Reader) {
	if r == nil {
		return
	}
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 1<<20), 1<<20)
	for s.Scan() {
		fmt.Fprintf(os.Stderr, "%s%s\n", prefix, s.Text())
	}
}
