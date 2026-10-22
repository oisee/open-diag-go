package lsd

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/oisee/open-diag-go/pkg/diag"
	"github.com/oisee/open-rfc-go/ni"
)

func limitServer(t *testing.T, o Options) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	o.Stub = "tape"
	if o.Hold == 0 {
		o.Hold = -1
	}
	o.Log = func(string, ...any) {}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, o) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(2 * time.Second):
			t.Error("Serve did not stop its active connections")
		}
	})
	return ln.Addr().String()
}

func limitDial(t *testing.T, addr string) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.SetDeadline(time.Now().Add(10 * time.Second))
	return c
}

func limitSend(t *testing.T, c net.Conn, payload []byte) {
	t.Helper()
	b, err := ni.EncodeFrame(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Write(b); err != nil {
		t.Fatal(err)
	}
}

func limitRead(t *testing.T, c net.Conn) []byte {
	t.Helper()
	var hdr [4]byte
	if _, err := io.ReadFull(c, hdr[:]); err != nil {
		t.Fatal(err)
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > 2<<20 {
		t.Fatalf("unexpected size %d", n)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(c, b); err != nil {
		t.Fatal(err)
	}
	return b
}

func limitClosed(t *testing.T, c net.Conn) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	var b [1]byte
	n, err := c.Read(b[:])
	if n != 0 || err == nil {
		t.Fatalf("expected close without response: %x, %v", b[:n], err)
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("timed out waiting for close")
	}
}

func limitPing(t *testing.T, c net.Conn) {
	t.Helper()
	limitSend(t, c, []byte("NI_PING\x00"))
	if got := limitRead(t, c); string(got) != "NI_PONG\x00" {
		t.Fatalf("pong = %x", got)
	}
}

func TestLimitDefaults(t *testing.T) {
	o, hold := (Options{}).normalised()
	if o.MaxPayloadLength != 1<<20 || o.MaxConnections != 64 || o.IdleTimeout != 30*time.Second || hold != 12*time.Second {
		t.Fatalf("unexpected defaults: %+v, hold=%v", o, hold)
	}
}

func TestPayloadLimit(t *testing.T) {
	for _, max := range []int{0, 512, 2 << 20} {
		t.Run(stringSize(max), func(t *testing.T) {
			addr := limitServer(t, Options{MaxPayloadLength: max})
			size := max
			if size == 0 {
				size = 1 << 20
			}
			c := limitDial(t, addr)
			limitSend(t, c, wireHello())
			limitRead(t, c)
			// A valid APPL4 fills the payload exactly to the boundary.
			payload := append((diag.Header{}).Bytes(), diag.EncodeItems([]diag.Item{{Type: diag.ItemAPPL4, ID: 4, SID: 0x27, Value: make([]byte, size-diag.HeaderLen-7)}})...)
			if len(payload) != size {
				t.Fatalf("payload = %d, want %d", len(payload), size)
			}
			limitSend(t, c, payload)
			limitRead(t, c)
			// The prefix alone must reject max+1, even when fragmented.
			var hdr [4]byte
			binary.BigEndian.PutUint32(hdr[:], uint32(size+1))
			if _, err := c.Write(hdr[:3]); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Write(hdr[3:]); err != nil {
				t.Fatal(err)
			}
			limitClosed(t, c)
		})
	}
}

func stringSize(n int) string {
	if n == 0 {
		return "default"
	}
	if n == 512 {
		return "smaller"
	}
	return "larger"
}

func TestConnectionLimit(t *testing.T) {
	for _, cap := range []int{0, 1} {
		name := "default"
		if cap != 0 {
			name = "configured"
		}
		t.Run(name, func(t *testing.T) {
			addr := limitServer(t, Options{MaxConnections: cap})
			count := cap
			if count == 0 {
				count = 64
			}
			for i := 0; i < count; i++ {
				limitPing(t, limitDial(t, addr))
			}
			limitClosed(t, limitDial(t, addr))
		})
	}
}

func TestIdleTimeout(t *testing.T) {
	t.Run("before hello", func(t *testing.T) {
		addr := limitServer(t, Options{IdleTimeout: 100 * time.Millisecond})
		limitClosed(t, limitDial(t, addr))
	})
	t.Run("activity refreshes idle", func(t *testing.T) {
		addr := limitServer(t, Options{IdleTimeout: 250 * time.Millisecond})
		c := limitDial(t, addr)
		limitSend(t, c, wireHello())
		limitRead(t, c)
		for i := 0; i < 4; i++ {
			time.Sleep(80 * time.Millisecond)
			limitPing(t, c)
		}
		limitClosed(t, c)
	})
	t.Run("partial bytes refresh initial idle", func(t *testing.T) {
		addr := limitServer(t, Options{IdleTimeout: 250 * time.Millisecond})
		c := limitDial(t, addr)
		wire, _ := ni.EncodeFrame(wireHello())
		for i := 0; i < 4; i++ {
			time.Sleep(80 * time.Millisecond)
			if _, err := c.Write(wire[i : i+1]); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := c.Write(wire[4:]); err != nil {
			t.Fatal(err)
		}
		limitRead(t, c)
		limitClosed(t, c)
	})
	t.Run("disabled", func(t *testing.T) {
		addr := limitServer(t, Options{IdleTimeout: -1})
		c := limitDial(t, addr)
		time.Sleep(150 * time.Millisecond)
		limitPing(t, c)
	})
}

func TestHoldIgnoresKeepalive(t *testing.T) {
	addr := limitServer(t, Options{Hold: 250 * time.Millisecond, IdleTimeout: time.Second})
	c := limitDial(t, addr)
	limitSend(t, c, wireHello())
	limitRead(t, c)
	for i := 0; i < 3; i++ {
		time.Sleep(50 * time.Millisecond)
		limitPing(t, c)
	}
	end := limitRead(t, c)
	if !bytes.Equal(end, []byte{0, 10, 0, 0, 0, 1, 0, 0}) {
		t.Fatalf("EOP = %x", end)
	}
	limitClosed(t, c) // FIN follows EOP immediately, while the read side stays open.
}

func TestAfterEOP(t *testing.T) {
	for _, hold := range []time.Duration{-1, 40 * time.Millisecond} {
		name := "exit"
		if hold > 0 {
			name = "hold"
		}
		t.Run(name, func(t *testing.T) {
			addr := limitServer(t, Options{MaxConnections: 1, Hold: hold, IdleTimeout: 5 * time.Second})
			c := limitDial(t, addr)
			limitSend(t, c, wireHello())
			limitRead(t, c)
			if hold < 0 {
				exit := append((diag.Header{}).Bytes(), diag.EncodeItems([]diag.Item{{Type: diag.ItemAPPL, ID: 0x0c, SID: 4, Value: []byte("/NEX")}})...)
				limitSend(t, c, exit)
			}
			if got := limitRead(t, c); !bytes.Equal(got, []byte{0, 10, 0, 0, 0, 1, 0, 0}) {
				t.Fatalf("EOP = %x", got)
			}
			limitClosed(t, c)
			// Server FIN does not release its slot, even past the former 3 s grace.
			time.Sleep(3200 * time.Millisecond)
			limitClosed(t, limitDial(t, addr))
			c.Close()
			// Wait for the peer EOF to release the slot, then verify a fresh session.
			deadline := time.Now().Add(time.Second)
			for {
				next := limitDial(t, addr)
				wire, _ := ni.EncodeFrame([]byte("NI_PING\x00"))
				_, err := next.Write(wire)
				var hdr [4]byte
				if err == nil {
					_, err = io.ReadFull(next, hdr[:])
				}
				if err == nil {
					b := make([]byte, binary.BigEndian.Uint32(hdr[:]))
					if _, err := io.ReadFull(next, b); err != nil {
						t.Fatal(err)
					}
					if string(b) != "NI_PONG\x00" {
						t.Fatalf("pong = %x", b)
					}
					break
				}
				next.Close()
				if time.Now().After(deadline) {
					t.Fatal("connection slot not released")
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

func TestInvalidLimits(t *testing.T) {
	for _, o := range []Options{{MaxPayloadLength: -1}, {MaxConnections: -1}} {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		if err := Serve(context.Background(), ln, o); err == nil {
			t.Error("invalid limits accepted")
		}
		ln.Close()
	}
}

func TestEOPIdleReleasesSlot(t *testing.T) {
	addr := limitServer(t, Options{MaxConnections: 1, Hold: 30 * time.Millisecond, IdleTimeout: 180 * time.Millisecond})
	c := limitDial(t, addr)
	limitSend(t, c, wireHello())
	limitRead(t, c)
	limitRead(t, c)
	limitClosed(t, c)
	// Data after FIN refreshes the idle timeout without producing a response.
	for i := 0; i < 3; i++ {
		time.Sleep(70 * time.Millisecond)
		limitSend(t, c, []byte("NI_PONG\x00"))
	}
	limitClosed(t, limitDial(t, addr))
	time.Sleep(250 * time.Millisecond)
	limitPing(t, limitDial(t, addr))
}

func TestScreenRearmsHold(t *testing.T) {
	addr := limitServer(t, Options{Hold: 300 * time.Millisecond, IdleTimeout: time.Second})
	c := limitDial(t, addr)
	limitSend(t, c, wireHello())
	screen := limitRead(t, c)
	time.Sleep(180 * time.Millisecond)
	limitSend(t, c, append((diag.Header{}).Bytes(), diag.ItemEOM))
	if got := limitRead(t, c); !bytes.Equal(got, screen) {
		t.Fatal("screen changed")
	}
	time.Sleep(180 * time.Millisecond)
	limitPing(t, c) // The original hold has elapsed; the rearmed hold has not.
	if got := limitRead(t, c); !bytes.Equal(got, []byte{0, 10, 0, 0, 0, 1, 0, 0}) {
		t.Fatalf("EOP = %x", got)
	}
}

func TestListenersHaveIndependentOptions(t *testing.T) {
	first := limitServer(t, Options{SID: "AAA", MaxConnections: 1, MaxPayloadLength: 512})
	second := limitServer(t, Options{SID: "BBB", MaxConnections: 2, MaxPayloadLength: 1024})
	for _, tc := range []struct{ addr, sid string }{{first, "AAA"}, {second, "BBB"}} {
		c := limitDial(t, tc.addr)
		limitSend(t, c, wireHello())
		checkSID(t, limitRead(t, c), tc.sid)
	}
	limitClosed(t, limitDial(t, first))
	limitPing(t, limitDial(t, second))
}
