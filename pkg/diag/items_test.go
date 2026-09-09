package diag

import "testing"

func TestParseItems(t *testing.T) {
	body := []byte{
		ItemAPPL, 0x06, 0x0c, 0x00, 0x03, 'a', 'b', 'c', // APPL 06/0c, 3 bytes
		ItemOKC, 0x01,
		ItemAPPL4, 0x12, 0x09, 0x00, 0x00, 0x00, 0x02, 'x', 'y',
		ItemEOM,
		0x7f, 0xde, 0xad, // unknown: the rest is raw
	}
	items := ParseItems(body)
	if len(items) != 5 {
		t.Fatalf("%d items: %+v", len(items), items)
	}
	if items[0].Key() != "APPL ST_R3INFO.0c" || string(items[0].Value) != "abc" {
		t.Errorf("appl: %+v", items[0])
	}
	if items[2].Key() != "APPL4 ACC_LIST.09" || string(items[2].Value) != "xy" {
		t.Errorf("appl4: %+v", items[2])
	}
	if items[3].TypeName() != "EOM" || len(items[3].Value) != 0 {
		t.Errorf("eom: %+v", items[3])
	}
	if items[4].Status != Raw || len(items[4].Value) != 3 {
		t.Errorf("raw tail: %+v", items[4])
	}
	h, _ := ParseHeader([]byte{0, 0x11, 0, 0, 0, 0, 0, 2})
	if h.Compress != 2 || h.ComFlag&FlagTermINI == 0 {
		t.Errorf("header: %+v", h)
	}
}

func TestNIControl(t *testing.T) {
	if n, ok := NIControl([]byte("NI_PONG\x00")); !ok || n != "NI_PONG" {
		t.Errorf("pong: %q %v", n, ok)
	}
	if _, ok := NIControl([]byte{0, 0, 0, 0, 0, 0, 0, 1}); ok {
		t.Error("a header taken for a ping")
	}
}
