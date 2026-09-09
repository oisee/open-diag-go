// Package tui renders a DIAG screen onto a plain character grid, the way a
// terminal shows it. A dynpro screen is a chain of diag.Atom read from a
// DYNT_ATOM item (Render); a classic ABAP list is a stream of positioned text
// runs read from the SBA/SFE/SLC/VARINFO.0b items (RenderList). It is
// read-only: it turns items into text and never encodes anything back. The
// network side lives in cmd/tui; the drawing lives here so it can be tested
// against synthetic atoms and segments with no capture and no connection.
package tui

import (
	"strings"

	"github.com/oisee/open-diag-go-pro/pkg/diag"
)

// DefaultRows and DefaultCols are the classic dynpro size used when the
// screen's own size is not known.
const (
	DefaultRows = 24
	DefaultCols = 80
)

// Grid is a rectangle of character cells, addressed by 0-based row and
// column, the atoms already placed on it.
type Grid struct {
	Rows, Cols int
	cells      [][]rune
}

// newGrid is a rows-by-cols grid filled with blanks.
func newGrid(rows, cols int) *Grid {
	if rows < 1 {
		rows = 1
	}
	if cols < 1 {
		cols = 1
	}
	cells := make([][]rune, rows)
	for i := range cells {
		row := make([]rune, cols)
		for j := range row {
			row[j] = ' '
		}
		cells[i] = row
	}
	return &Grid{Rows: rows, Cols: cols, cells: cells}
}

// put writes s starting at row, col, dropping any part that falls off the
// grid. Placement is clipping only; it never grows the grid.
func (g *Grid) put(row, col int, s string) {
	if row < 0 || row >= g.Rows {
		return
	}
	for i, r := range []rune(s) {
		c := col + i
		if c < 0 || c >= g.Cols {
			continue
		}
		g.cells[row][c] = r
	}
}

// Render places every drawable atom onto a grid at least minRows by minCols.
// The grid is grown, never shrunk, so that no atom is clipped off the bottom
// or the right; a caller with a smaller terminal clips at draw time with
// Clip. A minRows or minCols below one falls back to the default dynpro size.
func Render(atoms []diag.Atom, minRows, minCols int) *Grid {
	rows, cols := minRows, minCols
	if rows < 1 {
		rows = DefaultRows
	}
	if cols < 1 {
		cols = DefaultCols
	}
	for _, a := range atoms {
		s := atomText(a)
		if s == "" {
			continue
		}
		if a.Row+1 > rows {
			rows = a.Row + 1
		}
		if end := a.Col + len([]rune(s)); end > cols {
			cols = end
		}
	}
	g := newGrid(rows, cols)
	for _, a := range atoms {
		s := atomText(a)
		if s == "" {
			continue
		}
		g.put(a.Row, a.Col, s)
	}
	return g
}

// RenderList places the text runs of a classic ABAP list onto a grid at least
// minRows by minCols. Each segment is drawn at its own row and column, the way
// the list stream positioned it; the grid grows so no run is clipped off the
// bottom or the right. Runs are drawn in arrival order, so where the list
// overprints a cell the later run wins, as it does in the GUI. Colour lives on
// the segment for a colour-aware front end; this plain character grid shows the
// text only.
func RenderList(segs []diag.ListSegment, minRows, minCols int) *Grid {
	rows, cols := minRows, minCols
	if rows < 1 {
		rows = DefaultRows
	}
	if cols < 1 {
		cols = DefaultCols
	}
	for _, s := range segs {
		if s.Text == "" {
			continue
		}
		if s.Row+1 > rows {
			rows = s.Row + 1
		}
		if end := s.Col + len([]rune(s.Text)); end > cols {
			cols = end
		}
	}
	g := newGrid(rows, cols)
	for _, s := range segs {
		if s.Text == "" {
			continue
		}
		g.put(s.Row, s.Col, s.Text)
	}
	return g
}

// atomText is how one atom shows on the grid. Labels, output fields and the
// title of a frame are their trimmed value. An input field shows its value
// over a run of underscores that mark how wide the field is. A checkbox or
// radio button shows its state in a box before its label, a pushbutton its
// caption in brackets. Field-name and XML-property atoms carry no visible
// text and draw nothing.
func atomText(a diag.Atom) string {
	switch a.EType {
	case diag.AtomLabel, diag.AtomOutputField, diag.AtomFrame:
		return a.Value()
	case diag.AtomInputField:
		return inputText(a)
	case diag.AtomCheckbox:
		return checkBox(a.State) + " " + a.Value()
	case diag.AtomRadioButton:
		return radioBox(a.State) + " " + a.Value()
	case diag.AtomPushbutton:
		return "[" + a.Value() + "]"
	default:
		return ""
	}
}

// inputText draws an input field as its value left-justified over underscores
// that show the field's on-screen width, so an empty field is still visible.
func inputText(a diag.Atom) string {
	w := a.VisibleLength
	if w <= 0 {
		w = a.MaxChars
	}
	if w <= 0 {
		w = a.Length
	}
	v := []rune(a.Value())
	if w <= 0 {
		w = len(v)
	}
	if len(v) > w {
		v = v[:w]
	}
	out := make([]rune, 0, w)
	out = append(out, v...)
	for len(out) < w {
		out = append(out, '_')
	}
	return string(out)
}

// checkBox is [X] when the state byte is 'X', else [ ].
func checkBox(state byte) string {
	if state == 'X' || state == 'x' {
		return "[X]"
	}
	return "[ ]"
}

// radioBox is (X) when the state byte is 'X', else ( ).
func radioBox(state byte) string {
	if state == 'X' || state == 'x' {
		return "(X)"
	}
	return "( )"
}

// Line is one row of the grid as a string, blanks included.
func (g *Grid) Line(row int) string {
	if row < 0 || row >= g.Rows {
		return ""
	}
	return string(g.cells[row])
}

// At is the rune at row, col, or a blank when out of range.
func (g *Grid) At(row, col int) rune {
	if row < 0 || row >= g.Rows || col < 0 || col >= g.Cols {
		return ' '
	}
	return g.cells[row][col]
}

// Clip returns a copy no larger than maxRows by maxCols, so a screen wider or
// taller than the terminal loses its overflow rather than wrapping. A maximum
// of zero or less on either axis leaves that axis unclipped.
func (g *Grid) Clip(maxRows, maxCols int) *Grid {
	rows, cols := g.Rows, g.Cols
	if maxRows > 0 && maxRows < rows {
		rows = maxRows
	}
	if maxCols > 0 && maxCols < cols {
		cols = maxCols
	}
	out := newGrid(rows, cols)
	for r := 0; r < rows; r++ {
		copy(out.cells[r], g.cells[r][:cols])
	}
	return out
}

// String is the whole grid, one row per line, each row's trailing blanks
// trimmed. It is what a plain terminal prints.
func (g *Grid) String() string {
	var b strings.Builder
	for i, row := range g.cells {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strings.TrimRight(string(row), " "))
	}
	return b.String()
}
