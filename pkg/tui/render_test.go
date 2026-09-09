package tui

import (
	"strings"
	"testing"

	"github.com/oisee/open-diag-go-pro/pkg/diag"
)

// at reads the string of length n starting at row, col off the grid.
func at(g *Grid, row, col, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteRune(g.At(row, col+i))
	}
	return b.String()
}

// A label lands on the row and column it was given, one-to-one, with nothing
// spilling into the cells before it.
func TestRenderLabelPlacement(t *testing.T) {
	atoms := []diag.Atom{diag.Label(2, 5, "Hello")}
	g := Render(atoms, DefaultRows, DefaultCols)
	if g.Rows != DefaultRows || g.Cols != DefaultCols {
		t.Fatalf("grid size = %dx%d, want %dx%d", g.Rows, g.Cols, DefaultRows, DefaultCols)
	}
	if got := at(g, 2, 5, 5); got != "Hello" {
		t.Errorf("label at (2,5) = %q, want %q", got, "Hello")
	}
	if got := g.At(2, 4); got != ' ' {
		t.Errorf("cell before the label = %q, want blank", string(got))
	}
	if line := g.Line(0); strings.TrimSpace(line) != "" {
		t.Errorf("row 0 should be blank, got %q", line)
	}
}

// Several elements each sit where they were placed and do not disturb each
// other: a label, an output value, an input field, a checkbox and a button.
func TestRenderMixedElements(t *testing.T) {
	atoms := []diag.Atom{
		diag.Label(0, 0, "User"),
		diag.OutputField(0, 10, 8, "42", true),
		diag.InputField(1, 0, 6, "AB"),
		{EType: diag.AtomCheckbox, Row: 2, Col: 0, State: 'X', Text: "On"},
		{EType: diag.AtomRadioButton, Row: 3, Col: 0, State: ' ', Text: "Off"},
		{EType: diag.AtomPushbutton, Row: 4, Col: 0, Text: "Save", Function: "=SAVE"},
	}
	g := Render(atoms, DefaultRows, DefaultCols)

	if got := at(g, 0, 0, 4); got != "User" {
		t.Errorf("label = %q, want %q", got, "User")
	}
	if got := at(g, 0, 10, 2); got != "42" {
		t.Errorf("output value = %q, want %q", got, "42")
	}
	// The input shows its value then underscores out to its width of six.
	if got := at(g, 1, 0, 6); got != "AB____" {
		t.Errorf("input field = %q, want %q", got, "AB____")
	}
	if got := at(g, 2, 0, 6); got != "[X] On" {
		t.Errorf("checkbox = %q, want %q", got, "[X] On")
	}
	if got := at(g, 3, 0, 7); got != "( ) Off" {
		t.Errorf("radio = %q, want %q", got, "( ) Off")
	}
	if got := at(g, 4, 0, 6); got != "[Save]" {
		t.Errorf("button = %q, want %q", got, "[Save]")
	}
}

// A field-name atom is metadata, not something on screen, so it draws nothing.
func TestRenderSkipsFieldName(t *testing.T) {
	atoms := []diag.Atom{diag.FieldName(0, 0, "GV_X", 0)}
	g := Render(atoms, DefaultRows, DefaultCols)
	if strings.TrimSpace(g.String()) != "" {
		t.Errorf("field-name atom drew something: %q", g.String())
	}
}

// The grid grows past the requested minimum to hold an atom that would fall
// off the bottom-right, so nothing placed is ever lost.
func TestRenderGrowsToFitAtom(t *testing.T) {
	atoms := []diag.Atom{diag.Label(30, 100, "Edge")}
	g := Render(atoms, DefaultRows, DefaultCols)
	if g.Rows < 31 {
		t.Errorf("rows = %d, want at least 31", g.Rows)
	}
	if g.Cols < 104 {
		t.Errorf("cols = %d, want at least 104", g.Cols)
	}
	if got := at(g, 30, 100, 4); got != "Edge" {
		t.Errorf("grown-into label = %q, want %q", got, "Edge")
	}
}

// A classic list renders its runs at the row and column the list stream gave
// them: a coloured header cell, a ruled line, and a data row whose key and
// value columns land where they were placed and overprint in arrival order.
func TestRenderListPlacement(t *testing.T) {
	segs := []diag.ListSegment{
		{Row: 4, Col: 0, Color: diag.ColHeading, Text: "idx"},
		{Row: 5, Col: 0, Attr: [3]byte{0x08, 0x00, 0x08}, Text: "----"},
		{Row: 6, Col: 0, Color: diag.ColKey, Text: "  1"},
		{Row: 6, Col: 29, Color: diag.ColOff, Text: "1"},
	}
	g := RenderList(segs, DefaultRows, DefaultCols)
	if got := at(g, 4, 0, 3); got != "idx" {
		t.Errorf("header = %q, want %q", got, "idx")
	}
	if got := at(g, 5, 0, 4); got != "----" {
		t.Errorf("ruled line = %q, want %q", got, "----")
	}
	if got := at(g, 6, 0, 3); got != "  1" {
		t.Errorf("key column = %q, want %q", got, "  1")
	}
	if got := g.At(6, 29); got != '1' {
		t.Errorf("value column = %q, want %q", string(got), "1")
	}
	// Rows above the first run stay blank.
	if line := g.Line(0); strings.TrimSpace(line) != "" {
		t.Errorf("row 0 should be blank, got %q", line)
	}
}

// Later runs overprint earlier ones at a shared cell, the way SAP's idx column
// and its "row" label overlap on the wire.
func TestRenderListOverprint(t *testing.T) {
	segs := []diag.ListSegment{
		{Row: 6, Col: 0, Text: "        1"}, // nine-wide idx cell, "10" clipped to one digit here
		{Row: 6, Col: 9, Text: "row"},
	}
	g := RenderList(segs, DefaultRows, DefaultCols)
	if got := at(g, 6, 9, 3); got != "row" {
		t.Errorf("overprinted cell = %q, want %q", got, "row")
	}
}

// A list grows the grid past the requested minimum so a run near the bottom
// right is not lost.
func TestRenderListGrows(t *testing.T) {
	segs := []diag.ListSegment{{Row: 40, Col: 100, Text: "end of list"}}
	g := RenderList(segs, DefaultRows, DefaultCols)
	if g.Rows < 41 || g.Cols < 111 {
		t.Fatalf("grid = %dx%d, want at least 41x111", g.Rows, g.Cols)
	}
	if got := at(g, 40, 100, 11); got != "end of list" {
		t.Errorf("grown-into run = %q, want %q", got, "end of list")
	}
}

// Clip drops the overflow of a screen larger than the terminal instead of
// wrapping it.
func TestClip(t *testing.T) {
	atoms := []diag.Atom{diag.Label(0, 0, "Left edge and more")}
	g := Render(atoms, DefaultRows, DefaultCols).Clip(10, 4)
	if g.Rows != 10 || g.Cols != 4 {
		t.Fatalf("clipped size = %dx%d, want 10x4", g.Rows, g.Cols)
	}
	if got := g.Line(0); got != "Left" {
		t.Errorf("clipped row 0 = %q, want %q", got, "Left")
	}
}
