package tui

import "testing"

// cellsToString drops the styling and returns the glyphs, for asserting layout.
func cellsToString(cs []Cell) string {
	r := make([]rune, len(cs))
	for i, c := range cs {
		r[i] = c.Ch
	}
	return string(r)
}

func TestExpandIconsBare(t *testing.T) {
	// A bare @XX@ becomes a glyph and a space; surrounding text is kept.
	got := cellsToString(expandIcons("a@0Y@b", Style{}))
	if got != "a＋ b" {
		t.Errorf("bare icon = %q, want %q", got, "a＋ b")
	}
}

func TestExpandIconsTooltip(t *testing.T) {
	// A pushbutton caption @XX\Qtooltip@caption: the tooltip is dropped, the
	// icon drawn, the visible caption kept. This is the SE38 "Display" button.
	got := cellsToString(expandIcons("@10\\QDisplay@ Display", Style{}))
	if got != "▤  Display" {
		t.Errorf("tooltip icon = %q, want %q", got, "▤  Display")
	}
	// Create from the same screen (the caption keeps its own leading space, so
	// the glyph's trailing space and it read as two).
	if s := cellsToString(expandIcons("@0Y\\QCreate@ Create", Style{})); s != "＋  Create" {
		t.Errorf("create = %q, want %q", s, "＋  Create")
	}
	// An unknown code still resolves (neutral glyph) and drops its tooltip.
	if s := cellsToString(expandIcons("@ZZ\\QWhatever@ X", Style{})); s != "▣  X" {
		t.Errorf("unknown tooltip icon = %q, want %q", s, "▣  X")
	}
}

// A malformed token (no closing @) is left as literal text, not swallowed.
func TestExpandIconsMalformed(t *testing.T) {
	got := cellsToString(expandIcons("@10\\Qno close", Style{}))
	if got != "@10\\Qno close" {
		t.Errorf("malformed = %q, want it left literal", got)
	}
}
