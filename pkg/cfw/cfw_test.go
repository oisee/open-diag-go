package cfw

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/oisee/open-diag-go-pro/pkg/diag"
)

// rfctr00 returns the value of the RFC_TR.00 item in the nth S->C frame of a
// capture, or skips the test when the (gitignored) capture is absent.
func rfctr00(t *testing.T, path string, nth int) []byte {
	f, err := os.Open(path)
	if err != nil {
		t.Skipf("capture %s absent: %v", path, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	si := -1
	for sc.Scan() {
		var l struct{ Dir, Hex string }
		if json.Unmarshal(sc.Bytes(), &l) != nil || l.Dir != "S->C" {
			continue
		}
		si++
		if si != nth {
			continue
		}
		d, _ := hex.DecodeString(l.Hex)
		m, err := diag.ParseMessage(d, false)
		if err != nil {
			t.Fatalf("parse S->C #%d: %v", nth, err)
		}
		for _, it := range diag.ParseItems(m.Body) {
			if it.ID == 0x08 && it.SID == 0x00 {
				return it.Value
			}
		}
	}
	t.Fatalf("no RFC_TR.00 in S->C #%d", nth)
	return nil
}

// The Easy Access CFW-build call (S->C #6) decodes to a known verb sequence
// and its object-creating verbs each mint a handle.
func TestDecodeEasyAccessCall(t *testing.T) {
	val := rfctr00(t, "../../captures/probe.jsonl", 6)

	verbs, desc, values, err := Streams(val)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) == 0 {
		t.Fatal("empty value pool")
	}

	vs := ParseVerbs(verbs)
	if len(vs) != 71 {
		t.Errorf("verb count = %d, want 71", len(vs))
	}
	if len(vs) < 3 || vs[0].Name != "CreateObject" || vs[0].Flag != 'C' {
		t.Errorf("verb[0] = %+v, want CreateObject/C", vs[0])
	}
	if vs[1].Name != "CreateControl" {
		t.Errorf("verb[1] = %q, want CreateControl", vs[1].Name)
	}
	// The call creates the Easy Access container/tree controls, so several
	// verbs are object-creating.
	creates := 0
	for _, v := range vs {
		if v.Creates() {
			creates++
		}
	}
	if creates == 0 {
		t.Error("no object-creating verbs found")
	}

	ds := ParseSvarsDesc(desc)
	results := 0
	for _, r := range ds {
		if r.IsResult() {
			results++
		}
	}
	if results == 0 {
		t.Error("no _RESULT descriptor slots found")
	}

	e := NewEngine()
	minted := e.Run(vs)
	if len(minted) != creates {
		t.Errorf("minted %d handles, want %d (one per creating verb)", len(minted), creates)
	}
	if len(minted) > 0 && minted[0] != "O1" {
		t.Errorf("first handle = %q, want O1", minted[0])
	}
}
