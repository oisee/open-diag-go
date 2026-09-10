package main

import (
	"fmt"

	"github.com/oisee/open-diag-go-pro/pkg/diag"
	"github.com/oisee/open-diag-go-pro/pkg/tui"
)

// The command palette: the whole command set a screen offers, read straight
// from its GUI status — the application toolbar (MNUENTRY.03), the function
// keys (.04) and the dropdown menus (.02). It is a reference overlay: it shows
// what the screen can do and how each command is labelled. Firing a command
// still needs its function code, which the status does not carry for menu and
// F-key entries (see --fkeys and KNOWLEDGE.md §8); pushbuttons on the canvas
// carry theirs and are activated directly.

// commandList builds the palette lines from a screen's MNUENTRY items.
func commandList(items []diag.Item) []string {
	var out []string
	add := func(sid byte, label string) {
		for _, it := range items {
			if it.Type != diag.ItemAPPL4 || it.ID != 0x0b || it.SID != sid {
				continue
			}
			for _, e := range diag.ParseMenuEntries(it.Value) {
				if e.Separator() || e.Text == "" {
					continue
				}
				line := fmt.Sprintf("%-9s %s", label, e.Text)
				if e.Accel != 0 {
					line += fmt.Sprintf("  (Alt+%c)", e.Accel)
				}
				if e.Tooltip != "" && e.Tooltip != e.Text {
					line += "  — " + e.Tooltip
				}
				out = append(out, line)
			}
		}
	}
	add(0x03, "[toolbar]")
	add(0x04, "[key]")
	add(0x02, "[menu]")
	if len(out) == 0 {
		out = []string{"(this screen carries no GUI status)"}
	}
	return out
}

// drawPalette overlays the command list on the composed grid g, scrolled by
// s.palScroll. The box is centred and sized to the terminal.
func (s *session) drawPalette(g *tui.Grid) {
	lines := s.palLines
	w := 72
	if w > g.Cols-2 {
		w = g.Cols - 2
	}
	h := len(lines) + 2
	if h > g.Rows-4 {
		h = g.Rows - 4
	}
	if h < 3 {
		h = 3
	}
	visible := h - 2
	if s.palScroll > len(lines)-visible {
		s.palScroll = len(lines) - visible
	}
	if s.palScroll < 0 {
		s.palScroll = 0
	}
	end := s.palScroll + visible
	if end > len(lines) {
		end = len(lines)
	}
	shown := lines[s.palScroll:end]
	title := fmt.Sprintf("Commands (%d) — Ctrl+P/Esc close, up/down scroll", len(lines))
	row := (g.Rows - h) / 2
	col := (g.Cols - w) / 2
	if row < 0 {
		row = 0
	}
	if col < 0 {
		col = 0
	}
	g.Overlay(row, col, w, h, title, shown, tui.StyleMenu)
}
