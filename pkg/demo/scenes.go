// Package demo is the animation engine shared by the DIAG server (cmd/server,
// which pushes the scenes to a real SAP GUI) and the terminal (cmd/tui, which
// draws them locally). Keeping the scenes here means there is one set of acts,
// not a copy per front end. A scene draws either a dynpro (a frame.Screen of
// widgets, the DIAG dynpro channel) or a list (positioned coloured runs, the
// classic-list channel); exactly one of Dynpro and List is set. Every scene is
// keyed to ts, the seconds elapsed inside it, so the motion runs at a fixed
// wall-clock speed whatever the frame cadence and a dropped frame never stutters.
package demo

import (
	"fmt"
	"math"
	"time"

	"github.com/oisee/open-diag-go-pro/pkg/diag"
	"github.com/oisee/open-diag-go-pro/pkg/frame"
)

// Scene is one act of the demo.
type Scene struct {
	Name, Approach string
	// Dur is the scene's length; 0 means use the caller's default. The login
	// opener needs longer than a beat, so it sets its own.
	Dur time.Duration
	// Bare drops the caption/footer for this scene, so the login opener can
	// look like a real logon screen and nothing else.
	Bare bool
	// Dynpro draws the scene as a dynpro; nil for a list scene.
	Dynpro func(ts float64, scr *frame.Screen)
	// List draws the scene as a classic-list frame; nil for a dynpro scene.
	List func(ts float64) []diag.ListSegment
}

// Scenes are the acts, ordered from the lightest frame to the heaviest so the
// contrast in bytes-per-frame is easy to feel. Nine draw dynpros; the three LED
// scenes draw in the list channel.
func Scenes() []Scene {
	led := func(eff int) func(float64) []diag.ListSegment {
		return func(ts float64) []diag.ListSegment { return LEDSegmentsEff(eff, ts) }
	}
	return []Scene{
		{Name: "login", Approach: "a login form that sits, drifts a square, orbits, then multiplies", Dur: 26 * time.Second, Bare: true, Dynpro: sceneLogin},
		{Name: "orbit", Approach: "3 widgets moved by coordinate, sized by depth", Dynpro: sceneOrbit},
		{Name: "equalizer", Approach: "a row of buttons whose Height is the graphics — bars", Dynpro: sceneEqualizer},
		{Name: "snake", Approach: "a label snake on a Lissajous path, with a fading trail", Dynpro: sceneSnake},
		{Name: "matrix", Approach: "sparse falling columns — the grid used lightly", Dynpro: sceneMatrix},
		{Name: "fireworks", Approach: "rockets that rise and burst into gravity-fed sparks", Dur: 9 * time.Second, Dynpro: sceneFireworks},
		{Name: "helix", Approach: "a double helix twisting in place, strands and rungs", Dynpro: sceneHelix},
		{Name: "plasma", Approach: "LED plasma in the list channel — colour + letters", List: led(0)},
		{Name: "rings", Approach: "LED rings in the list channel", List: led(1)},
		{Name: "ball", Approach: "a bright ball bouncing on the LED field", List: led(2)},
		{Name: "starfield", Approach: "the whole character grid redrawn every frame", Dynpro: sceneStars},
		{Name: "icons", Approach: "a grid of real SAP icons, drawn via output fields", Dynpro: sceneIcons},
	}
}

// LEDEffectIndex is the effect index of an LED scene by name (0 plasma, 1 rings,
// 2 ball), or -1 for a non-LED scene.
func LEDEffectIndex(name string) int {
	switch name {
	case "plasma":
		return 0
	case "rings":
		return 1
	case "ball":
		return 2
	}
	return -1
}

// Centre pads text into width, centred; text at least as wide is returned as is.
func Centre(text string, width int) string {
	if len(text) >= width {
		return text
	}
	left := (width - len(text)) / 2
	right := width - len(text) - left
	return fmt.Sprintf("%*s%s%*s", left, "", text, right, "")
}

// Triangle bounces a value between 0 and span: it rises, hits the wall, and
// comes back, forever.
func Triangle(x float64, span int) int {
	if span <= 0 {
		return 0
	}
	p := math.Mod(x, float64(2*span))
	if p < 0 {
		p += float64(2 * span)
	}
	if p > float64(span) {
		p = float64(2*span) - p
	}
	return int(p)
}

// Clampi clamps v into [lo, hi].
func Clampi(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// LoginFields places the four logon fields as loose elements at (top,left): the
// labels and inputs the way the real screen has them, so a copy that moves or
// multiplies is fields on the canvas. idx keeps each copy's field names distinct.
func LoginFields(scr *frame.Screen, top, left, idx int) {
	if top < 0 {
		top = 0
	}
	if left < 0 {
		left = 0
	}
	scr.Text(top+0, left+0, "Client")
	scr.Input(top+0, left+19, 3, fmt.Sprintf("MANDT%d", idx), "001")
	scr.Text(top+2, left+0, "User")
	scr.Input(top+2, left+19, 12, fmt.Sprintf("BNAME%d", idx), "")
	scr.Text(top+3, left+0, "Password")
	scr.InputHidden(top+3, left+19, 12, fmt.Sprintf("BCODE%d", idx), "")
	scr.Text(top+5, left+0, "Logon Language")
	scr.Input(top+5, left+19, 2, fmt.Sprintf("LANGU%d", idx), "EN")
}

// NativeLogon draws the logon screen the way the capture shows it: the four
// fields at their real rows and columns and the Information box to the right,
// its welcome lines output fields led by the @0S@ info icon. Placeholder values.
func NativeLogon(scr *frame.Screen) {
	LoginFields(scr, 0, 1, 0)
	scr.Frame(0, 35, 56, 19, "Information")
	scr.Output(1, 37, 53, "INFO0", "@0S@ ABAP Cloud Developer Trial 2023 initial shipment", false)
	scr.Output(3, 37, 53, "INFO1", "@0S@ Since ABAP Cloud Developer Trial is a free", false)
	scr.Output(4, 37, 53, "INFO2", "offering for education and demo purposes only,", false)
	scr.Output(5, 37, 53, "INFO3", "we offer it with SAP Community support. That", false)
	scr.Output(6, 37, 53, "INFO4", "means that no primary support is available", false)
	scr.Output(7, 37, 53, "INFO5", "for this product.", false)
}

// OrbitLogins draws count logon forms orbiting a centre at the given angle.
func OrbitLogins(scr *frame.Screen, ang float64, count int) {
	const cx, cy, rx, ry = 38.0, 9.0, 30.0, 6.0
	for i := 0; i < count; i++ {
		a := ang + float64(i)*(2.0*math.Pi/float64(count))
		left := int(cx + rx*math.Cos(a))
		top := int(cy + ry*math.Sin(a))
		LoginFields(scr, top, left, i)
	}
}

func sceneLogin(ts float64, scr *frame.Screen) {
	const homeTop, homeLeft = 4, 10
	const dx, dy = 44.0, 11.0
	switch {
	case ts < 6:
		NativeLogon(scr)
	case ts < 12:
		f := (ts - 6) / 6 * 4
		seg := int(f)
		fr := f - float64(seg)
		top, left := float64(homeTop), float64(homeLeft)
		switch seg {
		case 0:
			left = homeLeft + fr*dx
		case 1:
			left = homeLeft + dx
			top = homeTop + fr*dy
		case 2:
			left = homeLeft + (1-fr)*dx
			top = homeTop + dy
		default:
			top = homeTop + (1-fr)*dy
		}
		LoginFields(scr, int(top), int(left), 0)
	case ts < 18:
		OrbitLogins(scr, (ts-12)*1.4, 1)
	case ts < 22:
		OrbitLogins(scr, (ts-12)*1.4, 2)
	default:
		OrbitLogins(scr, (ts-12)*1.4, 3)
	}
}

func sceneOrbit(ts float64, scr *frame.Screen) {
	const cx, cy, rx, ry = 39.0, 11.0, 28.0, 8.0
	const angSpeed = 1.1
	labels := []string{"Go", "DIAG", "no ABAP"}
	for i, lab := range labels {
		ang := -ts*angSpeed + float64(i)*(2.0*math.Pi/3.0)
		col := int(cx + rx*math.Cos(ang))
		row := int(cy + ry*math.Sin(ang))
		depth := (math.Sin(ang) + 1.0) / 2.0
		w := 6 + int(depth*12.0)
		h := 1 + int(depth*2.0+0.5)
		scr.ButtonH(row, col, w, h, Centre(lab, w-2), fmt.Sprintf("=B%d", i))
	}
	scr.Text(int(cy), int(cx)-2, "( o )")
}

func sceneEqualizer(ts float64, scr *frame.Screen) {
	const bars, baseRow = 12, 20
	for i := 0; i < bars; i++ {
		amp := (math.Sin(ts*3.0+float64(i)*0.5) + 1.0) / 2.0
		h := 1 + int(amp*10.0)
		col := 6 + i*6
		scr.ButtonH(baseRow-h, col, 4, h, "", fmt.Sprintf("=EQ%d", i))
	}
	for c := 4; c < 6+bars*6; c++ {
		scr.Text(baseRow, c, "-")
	}
}

func sceneSnake(ts float64, scr *frame.Screen) {
	const cx, cy, rx, ry = 39.0, 10.0, 30.0, 8.0
	const seg = 16
	for k := 0; k < seg; k++ {
		tt := ts - float64(k)*0.05
		col := int(cx + rx*math.Sin(tt*1.7))
		row := int(cy + ry*math.Sin(tt*2.3))
		ch := "O"
		switch {
		case k > 10:
			ch = "."
		case k > 4:
			ch = "o"
		}
		scr.Text(row, col, ch)
	}
}

func sceneMatrix(ts float64, scr *frame.Screen) {
	const w, h = 78, 20
	const glyphs = "01<>[]{}=+*/\\ABCDEF$#@abcdef"
	for x := 0; x < w; x += 3 {
		speed := 6.0 + float64((x*37)%11)
		off := float64((x * 13) % 23)
		head := int(math.Mod(ts*speed+off, float64(h+8)))
		for t := 0; t < 5; t++ {
			y := head - t
			if y < 0 || y >= h {
				continue
			}
			g := glyphs[(x+y*7+int(ts*10.0))%len(glyphs)]
			scr.Text(1+y, 1+x, string(g))
		}
	}
}

func sceneIcons(ts float64, scr *frame.Screen) {
	scr.Output(1, 2, 52, "IHDR", "@0S@ SAP icons drawn by Go via output fields", false)
	const cols, count = 12, 48
	sweep := int(ts*8.0) % count
	for code := 0; code < count; code++ {
		r := 3 + (code/cols)*3
		c := 4 + (code%cols)*8
		scr.Output(r, c, 4, fmt.Sprintf("IC%02X", code), fmt.Sprintf("@%02X@", code), false)
		label := fmt.Sprintf("%02X", code)
		if code == sweep {
			label = ">" + label
		}
		scr.Text(r+1, c, label)
	}
}

func sceneStars(ts float64, scr *frame.Screen) {
	const w, h = 78, 18
	banner := "  OPEN-DIAG-GO-PRO  ***  the whole character grid, redrawn  ***  driven by Go  "
	off := int(ts * 12.0)
	line := make([]byte, w)
	for i := 0; i < w; i++ {
		line[i] = banner[(off+i)%len(banner)]
	}
	scr.Text(1, 1, string(line))
	for x := 0; x < w; x++ {
		y := h/2 + int(float64(h/2-1)*math.Sin(float64(x)/6.0+ts*2.0))
		if y >= 0 && y < h {
			scr.Text(3+y, 1+x, "*")
		}
	}
}

func sceneFireworks(ts float64, scr *frame.Screen) {
	const w, h = 116, 21
	sparks := "*+.o"
	for i := 0; i < 4; i++ {
		period := 2.2 + float64(i)*0.5
		phase := math.Mod(ts+float64(i)*0.9, period) / period
		launchX := 16 + i*28
		peakY := 2 + (i%3)*2
		if phase < 0.5 {
			f := phase / 0.5
			y := h - 1 - int(f*float64(h-1-peakY))
			scr.Text(y, launchX, "|")
			if f > 0.6 {
				scr.Text(Clampi(y-1, 0, h-1), launchX, "^")
			}
		} else {
			f := (phase - 0.5) / 0.5
			const n = 14
			for s := 0; s < n; s++ {
				a := float64(s) / float64(n) * 2 * math.Pi
				rad := f * 11
				col := launchX + int(rad*2*math.Cos(a))
				row := peakY + int(rad*math.Sin(a)+f*f*7)
				if row >= 0 && row < h && col >= 0 && col < w {
					scr.Text(row, col, string(sparks[(s+int(ts*5))%len(sparks)]))
				}
			}
		}
	}
}

func sceneHelix(ts float64, scr *frame.Screen) {
	const rows, cx, amp = 21, 59, 34
	for r := 0; r < rows; r++ {
		a := float64(r)*0.5 + ts*2.2
		x1 := cx + int(float64(amp)*math.Sin(a))
		x2 := cx + int(float64(amp)*math.Sin(a+math.Pi))
		if r%2 == 0 {
			lo, hi := x1, x2
			if lo > hi {
				lo, hi = hi, lo
			}
			for c := lo + 1; c < hi; c++ {
				scr.Text(1+r, c, "-")
			}
		}
		ch1, ch2 := "o", "O"
		if math.Cos(a) >= 0 {
			ch1, ch2 = "O", "o"
		}
		scr.Text(1+r, x1, ch1)
		scr.Text(1+r, x2, ch2)
	}
}
