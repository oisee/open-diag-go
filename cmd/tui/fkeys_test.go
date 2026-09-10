package main

import "testing"

func TestParseFKeys(t *testing.T) {
	m := parseFKeys("8=STRT,3==BACK,12=/n, 5 = ONLI ,bad,99=X,0=Y,7=")
	cases := map[int]string{
		8:  "=STRT", // bare code gets a leading =
		3:  "=BACK", // already has =, kept
		12: "/n",    // a system command is left as-is
		5:  "=ONLI", // spaces around the pair and code are trimmed
	}
	for k, want := range cases {
		if m[k] != want {
			t.Errorf("F%d = %q, want %q", k, m[k], want)
		}
	}
	if _, ok := m[99]; ok {
		t.Errorf("F99 should be rejected (out of 1..24)")
	}
	if _, ok := m[0]; ok {
		t.Errorf("F0 should be rejected")
	}
	if _, ok := m[7]; ok {
		t.Errorf("F7 with an empty code should be left unbound")
	}
	if len(m) != 4 {
		t.Errorf("got %d bindings, want 4: %v", len(m), m)
	}
}
