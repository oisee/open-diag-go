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
