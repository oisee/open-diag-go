package lsd

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/oisee/open-diag-go/pkg/diag"
	"github.com/oisee/open-rfc-go/ni"
)

// Same capture-free hello as OSG test/protocol-diag.mjs: DP prefix, INI,
// and an APPL 04/27 language item.
func wireHello() []byte {
	b := make([]byte, diag.DPHeaderLen)
	copy(b, []byte{255, 255, 255, 255})
	b = append(b, (diag.Header{ComFlag: diag.FlagTermINI}).Bytes()...)
	return append(b, diag.EncodeItems([]diag.Item{{Type: diag.ItemAPPL, ID: 4, SID: 0x27, Value: []byte("EN")}})...)
}

func checkSID(t *testing.T, data []byte, sid string) {
	t.Helper()
	m, err := diag.ParseMessage(data, false)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, item := range diag.ParseItems(m.Body) {
		if item.Type == diag.ItemAPPL && item.ID == 6 && (item.SID == 0x23 || item.SID == 0x24) {
			count++
			want := make([]byte, len(item.Value))
			for i := range want {
				want[i] = ' '
			}
			copy(want, sid)
			if !bytes.Equal(item.Value, want) {
				t.Errorf("06/%02x = %q, want %q", item.SID, item.Value, want)
			}
		}
	}
	if count != 2 {
		t.Fatalf("got %d identity fields, want 2", count)
	}
	if sid == "QRS" && bytes.Contains(data, []byte("LSD")) {
		t.Error("placeholder SID remains")
	}
}

func TestAssetSID(t *testing.T) {
	for _, sid := range []string{"", "QRS"} {
		o, _ := (Options{SID: sid}).normalised()
		a, err := loadAsset()
		if err != nil {
			t.Fatal(err)
		}
		originals := [][]byte{bytes.Clone(a.cap.Server[0].Data), bytes.Clone(a.cap.Server[1].Data), bytes.Clone(a.listWrap), bytes.Clone(a.popup)}
		a.setSID(o.SID)
		for i, data := range [][]byte{a.cap.Server[0].Data, a.cap.Server[1].Data, a.listWrap, a.popup} {
			checkSID(t, data, o.SID)
			if len(data) != len(originals[i]) {
				t.Error("identity changed frame width")
			}
		}
	}
	// Loading a fresh asset must preserve the default after another SID was used.
	a, err := loadAsset()
	if err != nil {
		t.Fatal(err)
	}
	checkSID(t, a.cap.Server[0].Data, "LSD")
}

func TestServeWire(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ln, Options{SID: "QRS", Stub: "tape", Hold: -1, Log: func(string, ...any) {}})
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	dial := func(t *testing.T) net.Conn {
		t.Helper()
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		if err := c.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}
		return c
	}
	send := func(t *testing.T, c net.Conn, payload []byte) {
		t.Helper()
		wire, err := ni.EncodeFrame(payload)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Write(wire); err != nil {
			t.Fatal(err)
		}
	}
	read := func(t *testing.T, c net.Conn) []byte {
		t.Helper()
		var prefix [4]byte
		if _, err := io.ReadFull(c, prefix[:]); err != nil {
			t.Fatal(err)
		}
		size := binary.BigEndian.Uint32(prefix[:])
		if size > 1<<20 {
			t.Fatalf("unexpected frame length %d", size)
		}
		b := make([]byte, int(size))
		if _, err := io.ReadFull(c, b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	t.Run("configured SID and fragmented keepalive session", func(t *testing.T) {
		c := dial(t)
		ping, _ := ni.EncodeFrame([]byte("NI_PING\x00"))
		hello, _ := ni.EncodeFrame(wireHello())
		wire := append(ping, hello...)
		if _, err := c.Write(wire[:3]); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Write(wire[3:]); err != nil {
			t.Fatal(err)
		}
		if got := string(read(t, c)); got != "NI_PONG\x00" {
			t.Fatalf("pong = %q", got)
		}
		screen := read(t, c)
		checkSID(t, screen, "QRS")
		if !bytes.Contains(screen, []byte("Tape loading error")) {
			t.Error("missing tape text")
		}
		send(t, c, append((diag.Header{}).Bytes(), diag.ItemEOM))
		if !bytes.Equal(read(t, c), screen) {
			t.Error("repeat screen changed")
		}
		send(t, c, append((diag.Header{}).Bytes(), diag.EncodeItems([]diag.Item{{Type: diag.ItemAPPL, ID: 0x0c, SID: 4, Value: []byte("/i")}, {Type: diag.ItemEOM}})...))
		if got := read(t, c); !bytes.Equal(got, []byte{0, 10, 0, 0, 0, 1, 0, 0}) {
			t.Errorf("end = %x", got)
		}
	})
	cases := map[string][]byte{"junk": make([]byte, 208), "short": make([]byte, 207), "missing DP": wireHello()[200:], "empty items": wireHello()[:208]}
	for name, offset := range map[string]int{"bad route": 0, "missing INI": 201, "compressed": 207} {
		b := wireHello()
		b[offset] ^= 0xff
		cases[name] = b
	}
	for name, body := range map[string][]byte{
		"unknown item": {0xff}, "truncated APPL header": {0x10}, "APPL overrun": {0x10, 4, 0x27, 0, 2, 'E'},
		"truncated APPL4 header": {0x12, 4}, "APPL4 overrun": {0x12, 4, 0x27, 0xff, 0xff, 0xff, 0xff},
		"truncated XML header": {0x11}, "XML overrun": {0x11, 0, 0, 0, 1}, "short fixed item": {1},
		"valid item then junk": {0x0c, 0xff},
	} {
		cases[name] = append(wireHello()[:208:208], body...)
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			c := dial(t)
			send(t, c, payload)
			var b [1]byte
			n, err := c.Read(b[:])
			if n != 0 || err == nil {
				t.Fatalf("malformed hello answered: %x, %v", b[:n], err)
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatal("server timed out instead of closing")
			}
		})
	}
	// Keepalive must not consume first-message validation.
	t.Run("junk after ping", func(t *testing.T) {
		c := dial(t)
		send(t, c, []byte("NI_PING\x00"))
		read(t, c)
		send(t, c, make([]byte, 208))
		var b [1]byte
		n, err := c.Read(b[:])
		if n != 0 || err == nil || strings.Contains(err.Error(), "timeout") {
			t.Fatalf("expected immediate close: n=%d err=%v", n, err)
		}
	})
}
