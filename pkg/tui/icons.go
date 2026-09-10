package tui

import "strings"

// An icon on the wire is the four ASCII bytes @XX@ inside a text; the GUI
// substitutes the bitmap, which takes about two character cells, and shifts
// the rest of the text left to close the gap. This does the same on the
// terminal: each token becomes a coloured glyph and a space, and the text
// after it moves two cells left. Codes not in the table draw as a neutral
// square so the layout stays the same whatever the picture was.

type icon struct {
	glyph rune
	style Style
}

var icons = map[string]icon{
	"08": {'●', Style{Fg: 34}},  // green light
	"09": {'●', Style{Fg: 220}}, // yellow light
	"0A": {'●', Style{Fg: 196}}, // red light
	"5B": {'■', Style{Fg: 40}},  // LED green
	"01": {'✔', Style{Fg: 28}},  // checked
	"0V": {'✔', Style{Fg: 28}},  // okay
	"0W": {'✖', Style{Fg: 160}}, // cancel
	"0S": {'ℹ', Style{Fg: 24}},  // information
	"0Y": {'＋', Style{Fg: 24}},  // create
	"0Z": {'✎', Style{Fg: 24}},  // change
	"10": {'▤', Style{Fg: 24}},  // display
	"6C": {'☰', Style{Fg: 240}}, // menu
	"6A": {'☰', Style{Fg: 240}}, // menu
	"2L": {'▲', Style{Fg: 24}},
	"2M": {'▼', Style{Fg: 24}},
}

var iconDefault = icon{'▣', Style{Fg: 240}}

// iconAt reports whether s[i:] starts with an @XX@ icon token (two
// alphanumeric characters between the @s) and which one.
func iconAt(s []rune, i int) (icon, bool) {
	if i+4 > len(s) || s[i] != '@' || s[i+3] != '@' {
		return icon{}, false
	}
	if !isIconChar(s[i+1]) || !isIconChar(s[i+2]) {
		return icon{}, false
	}
	// An icon-with-tooltip form (@XX\Qtip@caption) is not handled; a bare
	// backslash after the code is the tell and we leave that text alone.
	code := strings.ToUpper(string(s[i+1 : i+3]))
	if ic, ok := icons[code]; ok {
		return ic, true
	}
	return iconDefault, true
}

func isIconChar(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
}

// expandIcons turns a text into cells, substituting each @XX@ with its
// glyph and a space in the given base style. Text without icons is returned
// unchanged except for the styling.
func expandIcons(text string, base Style) []Cell {
	rs := []rune(text)
	out := make([]Cell, 0, len(rs))
	for i := 0; i < len(rs); {
		if ic, ok := iconAt(rs, i); ok {
			st := base
			st.Fg = ic.style.Fg
			out = append(out, Cell{ic.glyph, st}, Cell{' ', base})
			i += 4
			continue
		}
		out = append(out, Cell{rs[i], base})
		i++
	}
	return out
}

// hasIcon reports whether the text carries an @XX@ token.
func hasIcon(text string) bool {
	rs := []rune(text)
	for i := range rs {
		if _, ok := iconAt(rs, i); ok {
			return true
		}
	}
	return false
}
