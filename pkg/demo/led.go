package demo

import (
	"fmt"
	"math"
	"strings"

	"github.com/oisee/open-diag-go-pro/pkg/diag"
)

// The LED display: a small logical grid of coloured cells drawn in the classic
// list channel, each cell a block of characters whose colour and density glyph
// come from an effect field. It is run-length encoded per row so the big grid
// stays a few list runs (the list channel stalls on hundreds).

// ledRamp is the density ramp, light to dark, made of letters (plus a space and
// two dots for the lightest steps): a denser letter darkens the cell.
const ledRamp = " .:iclosnuaewmyqpdbkhOQMWNB"

// ledSpectrum is the vivid list colours ordered as a spectrum.
var ledSpectrum = []byte{diag.ColKey, diag.ColHeading, diag.ColPositive, diag.ColTotal, diag.ColGroup, diag.ColNegative}

// The LED grid is logical: LEDRows x LEDCols cells, each drawn as a bw x bh
// block of character cells, so the display is big and chunky while the run
// count stays tied to this logical resolution.
const LEDRows, LEDCols = 10, 22

// ledEffectNames names the effects, indexed by effect number.
var ledEffectNames = []string{"plasma", "rings", "ball", "neoncity", "mountains", "doom"}

// doomMap is a tiny walled maze the raycaster walks through (1 = wall).
var doomMap = [][]byte{
	{1, 1, 1, 1, 1, 1, 1, 1},
	{1, 0, 0, 0, 0, 0, 0, 1},
	{1, 0, 1, 1, 0, 1, 0, 1},
	{1, 0, 0, 0, 0, 0, 0, 1},
	{1, 0, 1, 0, 1, 1, 0, 1},
	{1, 0, 0, 0, 0, 0, 0, 1},
	{1, 0, 0, 1, 0, 0, 0, 1},
	{1, 1, 1, 1, 1, 1, 1, 1},
}

// castRay steps a ray from (px,py) at angle ang through doomMap and returns the
// distance to the first wall and which side was hit (0 or 1, for shading).
func castRay(px, py, ang float64) (float64, int) {
	dx, dy := math.Cos(ang), math.Sin(ang)
	for s := 0.05; s < 12; s += 0.06 {
		x, y := px+dx*s, py+dy*s
		mx, my := int(x), int(y)
		if my < 0 || my >= len(doomMap) || mx < 0 || mx >= len(doomMap[0]) {
			return s, 0
		}
		if doomMap[my][mx] == 1 {
			side := 0
			if math.Abs(x-math.Round(x)) > math.Abs(y-math.Round(y)) {
				side = 1
			}
			return s, side
		}
	}
	return 12, 0
}

// doomCell renders one raycast LED: the camera circles the maze while turning,
// and each column casts a ray whose wall slice is tall and bright up close,
// short and dim far away — a first-person 3D corridor from a few dozen rays.
func doomCell(lr, lc int, t float64) (byte, byte) {
	// Walk an open corridor (map row 3 is clear, cols 1..6) and keep turning,
	// so walls sweep past without the camera ever ending up inside a wall.
	px := 3.5 + math.Sin(t*0.4)*1.3
	py := 3.5
	dir := t*0.6
	const fov = 0.9
	ang := dir + (float64(lc)/float64(LEDCols)-0.5)*fov
	dist, side := castRay(px, py, ang)
	wallH := int(float64(LEDRows) / (dist*0.5 + 0.35))
	if wallH > LEDRows {
		wallH = LEDRows
	}
	top := (LEDRows - wallH) / 2
	bot := top + wallH
	switch {
	case lr < top: // ceiling
		return diag.ColKey, ledRamp[1]
	case lr >= bot: // floor
		return diag.ColGroup, ledRamp[2]
	default: // wall: brighter and denser the closer it is; darker on side 1
		lit := 1.0 - dist/12
		di := Clampi(int(lit*float64(len(ledRamp))), 3, len(ledRamp)-1)
		var col byte = diag.ColNegative
		if side == 1 {
			col = diag.ColTotal
		}
		if dist > 6 {
			col = diag.ColHeading
		}
		return col, ledRamp[di]
	}
}

// mountainCell renders one Mountains LED: parallax ridge lines — a far range
// (high, light, slow) behind a near range (low, dark, fast) — each a sum of
// sines scrolling past, the ridge crest picked out brighter like a snow cap.
// Sky above is left light.
func mountainCell(lr, lc int, t float64) (byte, byte) {
	var col, ch byte = diag.ColKey, ledRamp[1] // pale sky
	type layer struct {
		speed, freq, amp float64
		base             int
		body, crest      byte
	}
	for _, L := range []layer{
		{0.5, 0.45, 4.0, 4, diag.ColHeading, diag.ColNormal}, // far range: high, light
		{1.3, 0.7, 4.5, 2, diag.ColGroup, diag.ColTotal},     // near range: jagged, dark
	} {
		w := float64(lc) + t*L.speed
		ridge := (math.Sin(w*L.freq) + math.Sin(w*L.freq*2.3+1.3)*0.5 + 1.5) / 3 // 0..1, jagged
		h := L.base + int(L.amp*ridge)
		top := LEDRows - h
		if lr < top {
			continue
		}
		if lr == top {
			col, ch = L.crest, ledRamp[len(ledRamp)-1] // crest / snow
		} else {
			col, ch = L.body, ledRamp[len(ledRamp)/2+3]
		}
	}
	return col, ch
}

// cityHash is a cheap deterministic pseudo-random for a building index, so a
// skyline scrolls without any state.
func cityHash(n int) uint32 {
	x := uint32(n)*2654435761 + 1013904223
	x ^= x >> 15
	x *= 2246822519
	x ^= x >> 13
	return x
}

// cityCell renders one Neon City LED: two parallax layers of buildings (the far
// one short, dim and slow; the near one tall, bright and fast — the near
// occludes the far, which reads as depth), each building lit by flickering
// windows. Sky is left dark.
func cityCell(lr, lc int, t float64) (byte, byte) {
	var col, ch byte = diag.ColKey, ledRamp[0] // dark sky
	type layer struct {
		speed    float64
		bw       int
		minH, mH int
		body     byte
		win      byte
	}
	for _, L := range []layer{
		{0.9, 3, 2, 4, diag.ColGroup, diag.ColHeading},  // far
		{2.2, 4, 4, 8, diag.ColTotal, diag.ColPositive}, // near
	} {
		worldC := lc + int(t*L.speed)
		b := worldC / L.bw
		h := L.minH + int(cityHash(b)%uint32(L.mH-L.minH+1))
		if lr < LEDRows-h {
			continue // sky for this layer
		}
		col, ch = L.body, ledRamp[len(ledRamp)/2]
		// Windows: a sparse lit grid inside the building, flickering.
		w := cityHash(b*131 + lr*17 + (worldC%L.bw)*7)
		if (w>>8)%3 == 0 && (int(t*4)+int(w))%29 != 0 {
			col, ch = L.win, ledRamp[len(ledRamp)-1]
		}
	}
	return col, ch
}

// ledCell is the colour and density glyph for one logical LED, for the current
// effect at time t.
func ledCell(lr, lc int, t float64, eff int) (byte, byte) {
	switch eff {
	case 3: // neon city: parallax skyline with flickering windows
		return cityCell(lr, lc, t)
	case 4: // mountains: parallax ridge lines with bright crests
		return mountainCell(lr, lc, t)
	case 5: // doom: a first-person raycast corridor
		return doomCell(lr, lc, t)
	case 1: // concentric rings breathing out from the centre
		cx, cy := float64(LEDCols)/2, float64(LEDRows)/2
		d := math.Hypot(float64(lc)-cx, (float64(lr)-cy)*2)
		v := (math.Sin(d/2.2-t*2.0) + 1.0) / 2.0
		gi := Clampi(int(v*float64(len(ledSpectrum))), 0, len(ledSpectrum)-1)
		di := Clampi(int(v*float64(len(ledRamp))), 0, len(ledRamp)-1)
		return ledSpectrum[gi], ledRamp[di]
	case 2: // a bright ball bouncing on a dark field
		bx := Triangle(t*9.0, LEDCols-1)
		by := Triangle(t*5.0, LEDRows-1)
		d := math.Hypot(float64(lc-bx), float64(lr-by)*2)
		if d < 2.5 {
			return diag.ColNegative, ledRamp[len(ledRamp)-1]
		}
		if d < 5.0 {
			return diag.ColTotal, ledRamp[len(ledRamp)/2]
		}
		return diag.ColKey, ledRamp[0]
	default: // plasma: hue and luminance from two sine fields
		fr, fc := float64(lr), float64(lc)
		hv := (math.Sin(fc/3.0+t) + math.Sin(fr/2.0-t) + math.Sin((fc+fr)/4.0+t*1.3) + 3.0) / 6.0
		lv := (math.Sin(fc/2.5-t*0.7) + math.Cos(fr/3.0+t*0.9) + 2.0) / 4.0
		gi := Clampi(int(hv*float64(len(ledSpectrum))), 0, len(ledSpectrum)-1)
		di := Clampi(int(lv*float64(len(ledRamp))), 0, len(ledRamp)-1)
		return ledSpectrum[gi], ledRamp[di]
	}
}

// LEDSegments is one frame of the LED display cycling through the effects,
// keyed to the frame counter n (the server's push cadence).
func LEDSegments(n int) []diag.ListSegment {
	return LEDSegmentsEff((n/45)%len(ledEffectNames), float64(n)*0.15)
}

// LEDSegmentsEff renders one specific effect at time t, run-length encoded per
// row so the big grid stays a handful of list runs.
func LEDSegmentsEff(eff int, t float64) []diag.ListSegment {
	if eff < 0 || eff >= len(ledEffectNames) {
		eff = 0
	}
	const bw, bh = 4, 2
	segs := []diag.ListSegment{
		diag.ListText(0, 2, diag.ColHeading, "OPEN-DIAG-GO-PRO  --  LED display: colour + letters (RLE)"),
	}
	for lr := 0; lr < LEDRows; lr++ {
		type run struct {
			startLC, wLC int
			col, ch      byte
		}
		var runs []run
		for lc := 0; lc < LEDCols; lc++ {
			col, ch := ledCell(lr, lc, t, eff)
			if k := len(runs) - 1; k >= 0 && runs[k].col == col && runs[k].ch == ch {
				runs[k].wLC++
			} else {
				runs = append(runs, run{lc, 1, col, ch})
			}
		}
		for b := 0; b < bh; b++ {
			sr := 2 + lr*bh + b
			for _, rn := range runs {
				segs = append(segs, diag.ListText(sr, 2+rn.startLC*bw, rn.col,
					strings.Repeat(string(rn.ch), rn.wLC*bw)))
			}
		}
	}
	segs = append(segs, diag.ListText(2+LEDRows*bh+1, 2, diag.ColNormal,
		fmt.Sprintf("%s   %dx%d LEDs   F3/Back stops", ledEffectNames[eff], LEDRows, LEDCols)))
	return segs
}
