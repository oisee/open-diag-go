package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/signal"
	"time"

	"github.com/oisee/open-diag-go-pro/pkg/frame"
	"github.com/oisee/open-diag-go-pro/pkg/tui"
)

// runDemo animates, non-interactively, entirely locally: no socket, no SAP, no
// handshake. It runs the same kind of scenes cmd/server pushes to a real GUI —
// built as frame.Screen dynpros — but draws them straight through the styled
// TUI (chrome + colour) on a wall-clock timer, so the animation can be watched
// in any terminal. It is the offline counterpart to pointing the tui at the
// demo server, without that server's live-handshake fragility. Ctrl-C or q
// quits; it is read-only and sends nothing anywhere.
func runDemo(sceneMS int, plain bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go watchQuit(ctx, stop)

	ch := chrome{
		title:   "SAP GUI as a display — odgp demo",
		menus:   []string{"Menu", "Edit", "Goto", "System", "Help"},
		toolbar: []string{"@0V@ Play", "@0W@ Stop"},
		sysid:   "ODGP",
		program: "ZODGP_DEMO",
		dynpro:  "0100",
	}
	scenes := demoScenes()
	sceneDur := time.Duration(sceneMS) * time.Millisecond
	const fps = 20
	tick := time.NewTicker(time.Second / fps)
	defer tick.Stop()

	start := time.Now()
	sceneStart := start
	idx := 0
	for {
		select {
		case <-ctx.Done():
			fmt.Print("\x1b[2J\x1b[H")
			return nil
		case now := <-tick.C:
			if now.Sub(sceneStart) >= sceneDur {
				idx = (idx + 1) % len(scenes)
				sceneStart = now
			}
			sc := scenes[idx]
			ts := now.Sub(sceneStart).Seconds()
			scr := frame.New(22, 100)
			sc.draw(ts, scr)
			canvas := tui.Render(scr.Atoms(), 22, 100)
			rows, cols := terminalSize()
			note := fmt.Sprintf("scene %d/%d  %s", idx+1, len(scenes), sc.name)
			if plain {
				if rows > 1 {
					canvas = canvas.Clip(rows-1, cols)
				}
				printClear(canvas.String() + "\n\x1b[7m " + note + "  |  q quits \x1b[0m")
			} else {
				g := ch.compose(canvas, byte('S'), sc.name, note+"  q quits", rows, cols)
				printClear(g.ANSI())
			}
		}
	}
}

// scene is one act of the demo: a name and a draw keyed to seconds elapsed.
type scene struct {
	name string
	draw func(ts float64, scr *frame.Screen)
}

// demoScenes are a few self-contained acts that move from their first frame,
// each a different use of the dynpro canvas — the same ideas cmd/server pushes
// to a live GUI, reduced to what draws through frame.Screen alone.
func demoScenes() []scene {
	return []scene{
		{"bounce — a label ricocheting off the walls", sceneBounce},
		{"equalizer — pushbutton height is the graphics", sceneEqualizer},
		{"snake — a marker on a Lissajous path", sceneSnake},
		{"starfield — the whole grid redrawn each frame", sceneStars},
	}
}

func sceneBounce(ts float64, scr *frame.Screen) {
	const w, h = 92, 20
	logo := "[ Go > DIAG ]"
	px := int(triangle(ts*22.0, float64(w-len(logo))))
	py := int(triangle(ts*9.0, float64(h-1)))
	scr.Text(1+py, 3+px, logo)
	scr.Text(2+py, 3+px, "  no ABAP  ")
}

func sceneEqualizer(ts float64, scr *frame.Screen) {
	const bars, baseRow = 12, 20
	for i := 0; i < bars; i++ {
		amp := (math.Sin(ts*3.0+float64(i)*0.5) + 1.0) / 2.0
		bh := 1 + int(amp*15.0)
		col := 6 + i*7
		scr.ButtonH(baseRow-bh, col, 5, bh, "", fmt.Sprintf("=EQ%d", i))
	}
	for c := 4; c < 6+bars*7; c++ {
		scr.Text(baseRow, c, "─")
	}
}

func sceneSnake(ts float64, scr *frame.Screen) {
	const cx, cy, rx, ry = 49.0, 10.0, 40.0, 9.0
	const seg = 18
	for k := 0; k < seg; k++ {
		tt := ts - float64(k)*0.05
		col := int(cx + rx*math.Sin(tt*1.7))
		row := int(cy + ry*math.Sin(tt*2.3))
		ch := "O"
		if k > 4 {
			ch = "o"
		}
		if k > 10 {
			ch = "."
		}
		scr.Text(1+row, 3+col, ch)
	}
}

func sceneStars(ts float64, scr *frame.Screen) {
	const w, h, n = 96, 20, 90
	for i := 0; i < n; i++ {
		speed := 0.4 + float64(i%7)*0.25
		x := math.Mod(float64(i)*17.0+ts*speed*22.0, float64(w))
		y := (i * 7) % h
		glyph := "."
		if i%5 == 0 {
			glyph = "*"
		}
		if i%11 == 0 {
			glyph = "+"
		}
		scr.Text(1+y, 2+int(x), glyph)
	}
}

// triangle bounces a value between 0 and span: it rises, hits the wall, and
// comes back, forever.
func triangle(t, span float64) float64 {
	if span <= 0 {
		return 0
	}
	p := math.Mod(t, 2*span)
	if p < span {
		return p
	}
	return 2*span - p
}
