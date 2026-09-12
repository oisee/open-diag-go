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
	"sort"
	"strings"
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
	// DurMul, when > 0, makes this scene run that multiple of the default
	// length (2 = twice as long), tracking the -scene-ms flag instead of a
	// fixed Dur. Ignored when Dur is set.
	DurMul float64
	// Bare drops the caption/footer for this scene, so the login opener can
	// look like a real logon screen and nothing else.
	Bare bool
	// Dynpro draws the scene as a dynpro; nil for a list scene.
	Dynpro func(ts float64, scr *frame.Screen)
	// List draws the scene as a classic-list frame; nil for a dynpro scene.
	List func(ts float64) []diag.ListSegment
	// Sound, when set, drives event-based audio: given the time span of one
	// frame (prevTs..ts within this scene), it returns the status-message type
	// whose GUI beep should sound (0 for silence) — a boom on a firework burst,
	// say. Natural and sparse: the beep marks a thing that happened, not a
	// metronome.
	Sound func(prevTs, ts float64) byte
}

// crossed reports whether an event that recurs every period seconds at the
// given offset landed inside (prevTs, ts].
func crossed(prevTs, ts, period, at float64) bool {
	return math.Floor((ts-at)/period) > math.Floor((prevTs-at)/period)
}

// fireworksSound sounds each rocket exactly on three visible moments, using the
// three distinct GUI beeps: a launch (S) at phase 0 as it leaves the ground, a
// boom (E) at phase 0.5 as it bursts, and a crackle (W) at phase 0.75 as the
// sparks spread and fall — for every one of the four rockets. No delay: the
// beep lands on the drawn frame, so it reads as tight sync. Rocket i's phase is
// mod(ts + i*0.9, period)/period.
func fireworksSound(prevTs, ts float64) byte {
	for i := 0; i < 4; i++ {
		period := 2.2 + float64(i)*0.5
		off := float64(i) * 0.9
		if crossed(prevTs, ts, period, -off) {
			return 'S' // launch, phase 0
		}
		if crossed(prevTs, ts, period, period/2-off) {
			return 'E' // burst, phase 0.5
		}
		if crossed(prevTs, ts, period, period*0.75-off) {
			return 'W' // crackle, phase 0.75
		}
	}
	return 0
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
		{Name: "orbit", Approach: "3 widgets moved by coordinate, sized by depth", DurMul: 2, Dynpro: sceneOrbit},
		{Name: "tetra", Approach: "a wireframe tetrahedron tumbling fast", Dynpro: sceneTetra},
		{Name: "octa", Approach: "a wireframe octahedron spinning fast", Dynpro: sceneOcta},
		{Name: "solid", Approach: "a spinning cube whose edges are z-sorted BUTTONs — solid filled rectangles", Dynpro: sceneSolid},
		{Name: "tornado", Approach: "a funnel of mixed widgets — buttons, icons, inputs, labels — spiralling like a tornado", Dynpro: sceneTornado},
		{Name: "equalizer", Approach: "a row of buttons whose Height is the graphics — bars", DurMul: 2, Dynpro: sceneEqualizer},
		{Name: "snake", Approach: "a label snake on a Lissajous path, with a fading trail", Dynpro: sceneSnake},
		{Name: "matrix", Approach: "sparse falling columns — the grid used lightly", Dynpro: sceneMatrix},
		{Name: "fireworks", Approach: "rockets that rise and burst into gravity-fed sparks", Dur: 9 * time.Second, Dynpro: sceneFireworks, Sound: fireworksSound},
		{Name: "helix", Approach: "a double helix twisting in place, strands and rungs", Dynpro: sceneHelix},
		{Name: "plasma", Approach: "LED plasma in the list channel — colour + letters", List: led(0)},
		{Name: "rings", Approach: "LED rings in the list channel", List: led(1)},
		{Name: "ball", Approach: "a bright ball bouncing on the LED field", List: led(2)},
		{Name: "starfield", Approach: "the whole character grid redrawn every frame", Dynpro: sceneStars},
		{Name: "icons", Approach: "a grid of real SAP icons, drawn via output fields", Dynpro: sceneIcons},
		{Name: "greetings", Approach: "a revolving drum of greets — names swing in, zoom at the front, turn away", Dynpro: sceneGreetings},
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

// spinWireframe rotates a solid's vertices on two axes, projects them in
// perspective, draws the edges as sparse dots and the vertices as glyphs — near
// vertices bright (O), far dim (o). Few objects (like orbit) so the frame stays
// light and the shape can spin fast without overrunning a real GUI. scale sizes
// the solid; sx/sy are the per-axis spin rates.
func spinWireframe(scr *frame.Screen, ts float64, verts [][3]float64, edges [][2]int, scale, sx, sy float64) {
	const cx, cy, d = 59.0, 12.0, 3.2
	rx, ry := ts*sx, ts*sy
	px := make([]int, len(verts))
	py := make([]int, len(verts))
	pz := make([]float64, len(verts))
	for i, v := range verts {
		x, y, z := v[0]*scale, v[1]*scale, v[2]*scale
		x, z = x*math.Cos(ry)-z*math.Sin(ry), x*math.Sin(ry)+z*math.Cos(ry) // yaw
		y, z = y*math.Cos(rx)-z*math.Sin(rx), y*math.Sin(rx)+z*math.Cos(rx) // pitch
		p := d / (z + d)
		px[i] = int(cx + x*p*20)
		py[i] = int(cy + y*p*9)
		pz[i] = z
	}
	for _, e := range edges { // edges as sparse dots (few atoms, keeps it light)
		a, b := e[0], e[1]
		for s := 1; s < 5; s++ {
			f := float64(s) / 5
			scr.Text(py[a]+int(float64(py[b]-py[a])*f), px[a]+int(float64(px[b]-px[a])*f), "·")
		}
	}
	for i := range verts {
		ch := "o"
		if pz[i] < 0 {
			ch = "O"
		}
		scr.Text(py[i], px[i], ch)
	}
}

// The wireframe solids: vertices and the edges that join them.
var (
	cubeVerts = [][3]float64{
		{-1, -1, -1}, {1, -1, -1}, {1, 1, -1}, {-1, 1, -1},
		{-1, -1, 1}, {1, -1, 1}, {1, 1, 1}, {-1, 1, 1},
	}
	cubeEdges = [][2]int{
		{0, 1}, {1, 2}, {2, 3}, {3, 0}, {4, 5}, {5, 6}, {6, 7}, {7, 4},
		{0, 4}, {1, 5}, {2, 6}, {3, 7},
	}
	tetraVerts = [][3]float64{{1, 1, 1}, {1, -1, -1}, {-1, 1, -1}, {-1, -1, 1}}
	tetraEdges = [][2]int{{0, 1}, {0, 2}, {0, 3}, {1, 2}, {1, 3}, {2, 3}}
	octaVerts  = [][3]float64{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}}
	octaEdges  = [][2]int{{0, 2}, {0, 3}, {0, 4}, {0, 5}, {1, 2}, {1, 3}, {1, 4}, {1, 5}, {2, 4}, {2, 5}, {3, 4}, {3, 5}}
)

func sceneCube(ts float64, scr *frame.Screen)  { spinWireframe(scr, ts, cubeVerts, cubeEdges, 1.0, 0.9, 1.3) }

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// edgeChar picks the line glyph for an edge by its screen slope: - flat, |
// steep, \ and / for the two diagonals.
func edgeChar(x1, y1, x2, y2 int) string {
	dx, dy := x2-x1, y2-y1
	ax, ay := abs(dx), abs(dy)
	switch {
	case ay*2 < ax:
		return "-"
	case ax*2 < ay:
		return "|"
	case (dx > 0) == (dy > 0):
		return "\\"
	default:
		return "/"
	}
}

// spinSolid rotates a solid and draws its corners as depth-scaled BUTTONs — a
// near corner a big 7x3 button, a far one a 1x1 dot, z-sorted so near sits on
// top — with the edges as directional / | \ - glyphs. Weight and depth without
// the muddy overlap of filled edge-boxes. scale sizes the solid; sx/sy the spin.
func spinSolid(scr *frame.Screen, ts float64, verts [][3]float64, edges [][2]int, scale, sx, sy float64) {
	const cx, cy, d = 59.0, 12.0, 3.2
	rx, ry := ts*sx, ts*sy
	n := len(verts)
	px := make([]int, n)
	py := make([]int, n)
	pz := make([]float64, n)
	for i, v := range verts {
		x, y, z := v[0]*scale, v[1]*scale, v[2]*scale
		x, z = x*math.Cos(ry)-z*math.Sin(ry), x*math.Sin(ry)+z*math.Cos(ry)
		y, z = y*math.Cos(rx)-z*math.Sin(rx), y*math.Sin(rx)+z*math.Cos(rx)
		p := d / (z + d)
		px[i] = int(cx + x*p*20)
		py[i] = int(cy + y*p*9)
		pz[i] = z
	}
	for _, e := range edges { // edges: sparse directional glyphs
		a, b := e[0], e[1]
		ch := edgeChar(px[a], py[a], px[b], py[b])
		for s := 1; s < 6; s++ {
			f := float64(s) / 6
			scr.Text(py[a]+int(float64(py[b]-py[a])*f), px[a]+int(float64(px[b]-px[a])*f), ch)
		}
	}
	order := make([]int, n) // corners: depth-scaled buttons, far-to-near
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool { return pz[order[i]] > pz[order[j]] })
	for _, i := range order {
		depth := Clampi(int((1-(pz[i]+2)/4)*100), 0, 100)
		w := 1 + depth*6/100 // 1..7
		h := 1 + depth*2/100 // 1..3
		scr.ButtonH(py[i]-h/2, px[i]-w/2, w, h, "", fmt.Sprintf("=S%d", i))
	}
}

func sceneSolid(ts float64, scr *frame.Screen) {
	spinSolid(scr, ts, cubeVerts, cubeEdges, 1.0, 0.9, 1.3)
}
func sceneTetra(ts float64, scr *frame.Screen) { spinSolid(scr, ts, tetraVerts, tetraEdges, 1.3, 1.4, 1.8) }
func sceneOcta(ts float64, scr *frame.Screen)  { spinSolid(scr, ts, octaVerts, octaEdges, 1.6, 1.7, 1.1) }

// sceneTornado swirls a funnel of mixed SAP widgets — buttons, icons, input
// fields and labels — around a vertical axis: the radius is wide at the top and
// narrows toward the bottom, each row is twisted a little more than the one
// below and the whole column sways, so the widgets spiral like a tornado. Items
// are drawn back-to-front and the near ones are drawn wider.
func sceneTornado(ts float64, scr *frame.Screen) {
	const (
		cx, centerRow = 59.0, 13.0
		rows, perRing = 17, 2
		vAspect       = 0.40 // a character cell is ~2.5x taller than wide
		camDist       = 120.0
		tiltMax       = 1.15 // ~66°: the highest the camera pitches up
	)
	icons := []string{"@0S@", "@0Y@", "@0Z@", "@10@", "@08@", "@09@", "@0A@"}
	labels := []string{"Go", "DIAG", "SAP", "no ABAP", "odgp", "R/3"}

	// The camera pitches from a pure side view (tilt 0) up to a high top-side
	// view and back, period ~20s. So the swirl reads first as widgets running
	// left-right, then — as the camera rises — as them travelling on perspective
	// ellipses around the axis, the way you'd see a real vortex from above.
	tilt := tiltMax * 0.5 * (1 - math.Cos(ts*0.32))
	sinP, cosP := math.Sin(tilt), math.Cos(tilt)

	type item struct {
		row, col, kind, k int
		depth             float64
	}
	var items []item
	for r := 0; r < rows; r++ {
		hf := float64(r) / float64(rows)            // 0 top .. 1 bottom
		radius := 6.0 + (1-hf)*30.0                 // wide at top, tight at the base
		sway := math.Sin(ts*2.6+float64(r)*0.4) * 6 // the column leans and whips
		// Increasing angle spins counter-clockwise seen from above — the way a
		// northern-hemisphere cyclone turns.
		base := ts*4.8 + float64(r)*0.75
		py := (8.0 - float64(r)) * 2.4 // ring height on the axis: top +, base -
		for k := 0; k < perRing; k++ {
			a := base + float64(k)*math.Pi
			px := radius * math.Cos(a) // across the ring
			pz := radius * math.Sin(a) // depth within the ring's own plane
			// Pitch the whole ring about the horizontal axis by the camera tilt.
			yUp := py*cosP + pz*sinP
			zDepth := pz*cosP - py*sinP        // + is toward the viewer
			p := camDist / (camDist - zDepth)  // perspective: near is bigger
			col := Clampi(int(cx+sway+px*p), 1, 116)
			row := Clampi(int(centerRow-yUp*p*vAspect), 1, 25)
			dN := zDepth / 45.0
			if dN > 1 {
				dN = 1
			} else if dN < -1 {
				dN = -1
			}
			items = append(items, item{row, col, (r + k) % 4, r*perRing + k, dN})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].depth < items[j].depth }) // back first
	for _, it := range items {
		switch it.kind {
		case 0: // button, wider up close
			w := 4
			if it.depth > 0 {
				w = 8
			}
			scr.ButtonH(it.row, Clampi(it.col-w/2, 1, 116), w, 1, "", fmt.Sprintf("=T%d", it.k))
		case 1: // an icon
			scr.Output(it.row, it.col, 4, fmt.Sprintf("TI%d", it.k), icons[it.k%len(icons)], false)
		case 2: // an input field — a white bar whose length grows up close and
			// shrinks into the distance, so the field width is the graphics. A
			// sharp specular term flashes it wider right as it swings to the
			// front (depth -> 1): the white fields become the glints.
			w := 3 + int((it.depth+1)*3.5) // depth -1..1 -> width 3..10
			if it.depth > 0 {
				w += int(math.Pow(it.depth, 6) * 6) // a glint at the near face only
			}
			scr.Input(it.row, Clampi(it.col-w/2, 1, 116), w, fmt.Sprintf("TF%d", it.k), "")
		default: // a label
			scr.Text(it.row, it.col, labels[it.k%len(labels)])
		}
	}
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

// sceneGreetings is the greets drum: a carousel of names revolving around a
// vertical axis (the tornado's projection), near ones big and letter-spaced,
// swinging in from the left, filling the centre, then shrinking away to the
// right and hiding round the back. Each character's spacing scales with depth,
// so a name zooms as it turns to face the viewer.
var greetNames = []string{
	"vivid-vibes", "vsp", "open-rfc-go", "sap-sso-trace",
	"sap-kb", "ABAP demoscene", "SAP GUI benders",
}

func sceneGreetings(ts float64, scr *frame.Screen) {
	const cx, midRow, rvert = 59.0, 11.0, 8.0
	n := len(greetNames)
	base := ts * 0.8 // drum rotation, rad/s
	scr.Text(1, 43, "= = =   O D G P   G R E E T S   = = =")
	scr.Text(22, 40, "respect to everyone who bent a SAP GUI")

	type spun struct {
		i     int
		depth float64
	}
	order := make([]spun, n)
	for i := range order {
		a := base + float64(i)*2*math.Pi/float64(n)
		order[i] = spun{i, math.Cos(a)} // depth: front > 0
	}
	sort.Slice(order, func(a, b int) bool { return order[a].depth < order[b].depth }) // back first
	for _, o := range order {
		if o.depth <= 0.05 { // hidden round the back of the drum
			continue
		}
		a := base + float64(o.i)*2*math.Pi/float64(n)
		// The name rolls vertically over the drum (its own row); front-centre is
		// biggest and letter-spaced, the top/bottom edges tighten with the curve.
		row := midRow - math.Sin(a)*rvert
		spacing := 0.8 + 1.6*o.depth
		name := greetNames[o.i]
		start := cx - float64(len(name)-1)/2*spacing
		for j := 0; j < len(name); j++ {
			col := start + float64(j)*spacing
			if col < 1 || col > 116 {
				continue
			}
			scr.Text(int(row+0.5), int(col+0.5), string(name[j]))
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
			// One run per rung, not one atom per character: a per-char rung made
			// this the heaviest scene (~9 KB/frame) and overran the real GUI
			// (KNOWLEDGE §9); as a single run it is a few hundred bytes.
			if hi-lo > 1 {
				scr.Text(1+r, lo+1, strings.Repeat("-", hi-lo-1))
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
