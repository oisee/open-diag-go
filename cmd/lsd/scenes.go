package main

// The light-show scene engine, copied from cmd/server's demo mode (same owner,
// no attribution needed). Every scene is driven by wall-clock time, so motion is
// the same speed whatever the frame cadence. The only change from the server
// copy: the sound insert (withSound) is dropped, and the wrapper frames come
// from the embedded asset instead of a capture file.

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/oisee/open-diag-go-pro/pkg/diag"
	"github.com/oisee/open-diag-go-pro/pkg/frame"
	"github.com/oisee/open-diag-go-pro/pkg/replay"
)

// demoSceneMS is how long each scene runs (wall clock), settable via -scene-ms.
var demoSceneMS = 3000

// A scene is one act of the show: a name, the approach it shows off, and a draw
// that lays it onto the screen given ts — the seconds elapsed inside this scene.
type scene struct {
	name     string
	approach string
	draw     func(ts float64, scr *frame.Screen)
	dur      time.Duration // this scene's length; 0 = the demo's default beat
	bare     bool          // drop the caption/footer (the login opener)
}

// demoScenes are the acts, lightest frame to heaviest.
func demoScenes() []scene {
	return []scene{
		{"login", "a login form that sits, drifts a square, orbits, then multiplies", sceneLogin, 26 * time.Second, true},
		{"orbit", "3 widgets moved by coordinate, sized by depth", sceneOrbit, 0, false},
		{"equalizer", "a row of buttons whose Height is the graphics — bars", sceneEqualizer, 0, false},
		{"snake", "a label snake on a Lissajous path, with a fading trail", sceneSnake, 0, false},
		{"matrix", "sparse falling columns — the grid used lightly", sceneMatrix, 0, false},
		{"plasma", "LED plasma in the list channel — colour + letters", ledFallback, 0, false},
		{"rings", "LED rings in the list channel", ledFallback, 0, false},
		{"ball", "a bright ball bouncing on the LED field", ledFallback, 0, false},
		{"starfield", "the whole character grid redrawn every frame (~80 labels)", sceneStars, 0, false},
		{"icons", "a grid of real SAP icons, drawn by us via output fields", sceneIcons, 0, false},
	}
}

func centre(text string, width int) string {
	if len(text) >= width {
		return text
	}
	left := (width - len(text)) / 2
	right := width - len(text) - left
	return fmt.Sprintf("%*s%s%*s", left, "", text, right, "")
}

// triangle bounces a value between 0 and span, forever.
func triangle(x float64, span int) int {
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

func clampi(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// loginFields places the four logon fields as loose elements at (top,left).
func loginFields(scr *frame.Screen, top, left, idx int) {
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

// nativeLogon draws the logon screen the way the capture showed it. Placeholder
// values only, never real credentials.
func nativeLogon(scr *frame.Screen) {
	loginFields(scr, 0, 1, 0)
	scr.Frame(0, 35, 56, 19, "Information")
	scr.Output(1, 37, 53, "INFO0", "@0S@ ABAP Cloud Developer Trial 2023 initial shipment", false)
	scr.Output(3, 37, 53, "INFO1", "@0S@ Since ABAP Cloud Developer Trial is a free", false)
	scr.Output(4, 37, 53, "INFO2", "offering for education and demo purposes only,", false)
	scr.Output(5, 37, 53, "INFO3", "we offer it with SAP Community support. That", false)
	scr.Output(6, 37, 53, "INFO4", "means that no primary support is available", false)
	scr.Output(7, 37, 53, "INFO5", "for this product.", false)
}

func orbitLogins(scr *frame.Screen, ang float64, count int) {
	const cx, cy, rx, ry = 38.0, 9.0, 30.0, 6.0
	for i := 0; i < count; i++ {
		a := ang + float64(i)*(2.0*math.Pi/float64(count))
		left := int(cx + rx*math.Cos(a))
		top := int(cy + ry*math.Sin(a))
		loginFields(scr, top, left, i)
	}
}

// sceneLogin is the demo's opener (used only when there is no logon backdrop;
// with the embedded logon frame the renderer uses loginFieldsScreen instead).
func sceneLogin(ts float64, scr *frame.Screen) {
	const homeTop, homeLeft = 4, 10
	const dx, dy = 44.0, 11.0
	switch {
	case ts < 6:
		nativeLogon(scr)
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
		loginFields(scr, int(top), int(left), 0)
	case ts < 18:
		orbitLogins(scr, (ts-12)*1.4, 1)
	case ts < 22:
		orbitLogins(scr, (ts-12)*1.4, 2)
	default:
		orbitLogins(scr, (ts-12)*1.4, 3)
	}
}

// loginFieldsScreen is the login scene inside the real logon backdrop: only the
// fields move; the menu, status and welcome come from the wrapper.
func loginFieldsScreen(ts float64) *frame.Screen {
	const homeTop, homeLeft = 0, 1
	const dx, dy = 44.0, 11.0
	scr := frame.New(27, 120)
	switch {
	case ts < 6:
		loginFields(scr, homeTop, homeLeft, 0)
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
		loginFields(scr, int(top), int(left), 0)
	case ts < 18:
		orbitLogins(scr, (ts-12)*1.4, 1)
	case ts < 22:
		orbitLogins(scr, (ts-12)*1.4, 2)
	default:
		orbitLogins(scr, (ts-12)*1.4, 3)
	}
	return scr
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
		scr.ButtonH(row, col, w, h, centre(lab, w-2), fmt.Sprintf("=B%d", i))
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

// ---- LED list-channel scenes -------------------------------------------------

const ledRamp = " .:iclosnuaewmyqpdbkhOQMWNB"

var ledSpectrum = []byte{diag.ColKey, diag.ColHeading, diag.ColPositive, diag.ColTotal, diag.ColGroup, diag.ColNegative}

const ledRows, ledCols = 10, 22

var ledEffects = []string{"plasma", "rings", "ball"}

func ledSceneEffect(name string) int {
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

func ledFallback(ts float64, scr *frame.Screen) {
	scr.Text(2, 2, "LED effect needs the list wrapper")
}

func ledCell(lr, lc int, t float64, eff int) (byte, byte) {
	switch eff {
	case 1:
		cx, cy := float64(ledCols)/2, float64(ledRows)/2
		d := math.Hypot(float64(lc)-cx, (float64(lr)-cy)*2)
		v := (math.Sin(d/2.2-t*2.0) + 1.0) / 2.0
		gi := clampi(int(v*float64(len(ledSpectrum))), 0, len(ledSpectrum)-1)
		di := clampi(int(v*float64(len(ledRamp))), 0, len(ledRamp)-1)
		return ledSpectrum[gi], ledRamp[di]
	case 2:
		bx := triangle(t*9.0, ledCols-1)
		by := triangle(t*5.0, ledRows-1)
		d := math.Hypot(float64(lc-bx), float64(lr-by)*2)
		if d < 2.5 {
			return diag.ColNegative, ledRamp[len(ledRamp)-1]
		}
		if d < 5.0 {
			return diag.ColTotal, ledRamp[len(ledRamp)/2]
		}
		return diag.ColKey, ledRamp[0]
	default:
		fr, fc := float64(lr), float64(lc)
		hv := (math.Sin(fc/3.0+t) + math.Sin(fr/2.0-t) + math.Sin((fc+fr)/4.0+t*1.3) + 3.0) / 6.0
		lv := (math.Sin(fc/2.5-t*0.7) + math.Cos(fr/3.0+t*0.9) + 2.0) / 4.0
		gi := clampi(int(hv*float64(len(ledSpectrum))), 0, len(ledSpectrum)-1)
		di := clampi(int(lv*float64(len(ledRamp))), 0, len(ledRamp)-1)
		return ledSpectrum[gi], ledRamp[di]
	}
}

func ledSegmentsEff(eff int, t float64) []diag.ListSegment {
	const bw, bh = 4, 2
	segs := []diag.ListSegment{
		diag.ListText(0, 2, diag.ColHeading, "OPEN-DIAG-GO-PRO  --  LED display: colour + letters (RLE)"),
	}
	for lr := 0; lr < ledRows; lr++ {
		type run struct {
			startLC, wLC int
			col, ch      byte
		}
		var runs []run
		for lc := 0; lc < ledCols; lc++ {
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
	segs = append(segs, diag.ListText(2+ledRows*bh+1, 2, diag.ColNormal,
		fmt.Sprintf("%s   %dx%d LEDs   F3/Back stops", ledEffects[eff], ledRows, ledCols)))
	return segs
}

// ---- the logon backdrop ------------------------------------------------------

type logonWrap struct {
	items      []diag.Item
	header     diag.Header
	fieldIdx   int
	welcomeIdx int
}

func loadLogonWrap(cap *replay.Capture, log func(string, ...any)) (*logonWrap, bool) {
	for _, f := range cap.Server {
		m, err := diag.ParseMessage(f.Data, false)
		if err != nil {
			continue
		}
		items := diag.ParseItems(m.Body)
		fieldIdx, welcomeIdx := -1, -1
		for i, it := range items {
			if it.Type != diag.ItemAPPL4 || it.ID != 0x09 || it.SID != 0x02 {
				continue
			}
			v := string(it.Value)
			if strings.Contains(v, "RSYST-MANDT") {
				fieldIdx = i
			} else if strings.Contains(v, "ABAP Cloud") || strings.Contains(v, "INFO_TAB") {
				welcomeIdx = i
			}
		}
		if fieldIdx < 0 {
			continue
		}
		h := m.Header
		h.Compress = 0
		log("logon wrap located: fields atom #%d, welcome atom #%d, %d items", fieldIdx, welcomeIdx, len(items))
		return &logonWrap{items: items, header: h, fieldIdx: fieldIdx, welcomeIdx: welcomeIdx}, true
	}
	return nil, false
}

// ---- the demo renderer -------------------------------------------------------

// demoRenderer cycles the scenes on a wall clock, exactly as cmd/server's demo
// mode does. wrapFrame is the counter (GV_TICKS) dynpro wrapper; listWrap is the
// classic-list wrapper for the LED scenes.
func demoRenderer(cap *replay.Capture, wrapFrame int, listWrap []byte, log func(string, ...any)) func(n int) []byte {
	f, ok := cap.ServerFrame(wrapFrame)
	if !ok {
		return func(int) []byte { return nil }
	}
	m, err := diag.ParseMessage(f.Data, false)
	if err != nil {
		return func(int) []byte { return nil }
	}
	items := diag.ParseItems(m.Body)
	atomIdx := -1
	for i, it := range items {
		if it.Type == diag.ItemAPPL4 && it.ID == 0x09 && it.SID == 0x02 {
			atomIdx = i
		}
	}
	if atomIdx < 0 {
		return func(int) []byte { return nil }
	}
	h := m.Header
	h.Compress = 0
	base := items
	logon, haveLogon := loadLogonWrap(cap, log)

	var listKeep []diag.Item
	var listHdr diag.Header
	listInsertAt := -1
	haveList := false
	if listWrap != nil {
		if lm, lerr := diag.ParseMessage(listWrap, false); lerr == nil {
			for _, it := range diag.ParseItems(lm.Body) {
				isList := it.Type == diag.ItemSBA || it.Type == diag.ItemSFE || it.Type == diag.ItemSLC ||
					(it.Type == diag.ItemAPPL && it.ID == 0x0c && it.SID == 0x0b)
				if isList {
					if listInsertAt < 0 {
						listInsertAt = len(listKeep)
					}
					continue
				}
				listKeep = append(listKeep, it)
			}
			if listInsertAt < 0 {
				listInsertAt = len(listKeep)
			}
			listHdr = lm.Header
			listHdr.Compress = 0
			haveList = true
			log("list wrapper ready for LED scenes")
		}
	}

	scenes := demoScenes()
	def := time.Duration(demoSceneMS) * time.Millisecond
	if def <= 0 {
		def = 3 * time.Second
	}
	durs := make([]time.Duration, len(scenes))
	var total time.Duration
	for i, s := range scenes {
		d := s.dur
		if d <= 0 {
			d = def
		}
		durs[i] = d
		total += d
	}
	var start time.Time
	lastScene := -1
	return func(n int) []byte {
		if start.IsZero() {
			start = time.Now()
		}
		pos := time.Since(start) % total
		idx, acc := 0, time.Duration(0)
		for i, d := range durs {
			if pos < acc+d {
				idx = i
				break
			}
			acc += d
		}
		ts := (pos - acc).Seconds()
		if idx != lastScene {
			log("scene %d/%d: %s (%s)", idx+1, len(scenes), scenes[idx].name, scenes[idx].approach)
			lastScene = idx
		}
		if eff := ledSceneEffect(scenes[idx].name); eff >= 0 && haveList {
			t := float64(int(ts/0.18)) * 0.15
			mine := diag.EncodeListItems(ledSegmentsEff(eff, t))
			out := append(append(append([]diag.Item{}, listKeep[:listInsertAt]...), mine...), listKeep[listInsertAt:]...)
			msg, err := diag.EncodeMessage(listHdr, out, false)
			if err != nil {
				return nil
			}
			return msg
		}
		if scenes[idx].name == "login" && haveLogon {
			out := append([]diag.Item{}, logon.items...)
			if ts >= 6 {
				out[logon.fieldIdx].Value = loginFieldsScreen(ts).Encode()
				if logon.welcomeIdx >= 0 {
					out[logon.welcomeIdx].Value = nil
				}
			}
			msg, err := diag.EncodeMessage(logon.header, out, false)
			if err != nil {
				return nil
			}
			return msg
		}
		scr := frame.New(27, 120)
		scenes[idx].draw(ts, scr)
		if !scenes[idx].bare {
			scr.Text(24, 1, fmt.Sprintf("scene %d/%d  %-9s  approach: %s", idx+1, len(scenes), scenes[idx].name, scenes[idx].approach))
			scr.Text(25, 1, "F3/Back or close the window to stop")
		}
		out := append([]diag.Item{}, base...)
		out[atomIdx].Value = scr.Encode()
		msg, err := diag.EncodeMessage(h, out, false)
		if err != nil {
			return nil
		}
		return msg
	}
}

// ---- serve-loop helpers ------------------------------------------------------

func isClose(items []diag.Item) bool {
	for _, it := range items {
		if it.Type == diag.ItemAPPL && it.ID == 0x0c && it.SID == 0x04 && strings.TrimSpace(string(it.Value)) == "/i" {
			return true
		}
	}
	return false
}

func isNewWindow(items []diag.Item) bool {
	for _, it := range items {
		if it.Type == diag.ItemAPPL && it.ID == 0x0c && it.SID == 0x04 && strings.HasPrefix(strings.TrimSpace(string(it.Value)), "/o") {
			return true
		}
	}
	return false
}

// staticRespondWrap wraps a screen in the counter dynpro wrapper, for a one-off
// send such as a frozen animation.
func staticRespondWrap(cap *replay.Capture, wrapFrame int, scr *frame.Screen) []byte {
	f, ok := cap.ServerFrame(wrapFrame)
	if !ok {
		return nil
	}
	m, err := diag.ParseMessage(f.Data, false)
	if err != nil {
		return nil
	}
	items := diag.ParseItems(m.Body)
	for i, it := range items {
		if it.Type == diag.ItemAPPL4 && it.ID == 0x09 && it.SID == 0x02 {
			items[i].Value = scr.Encode()
			h := m.Header
			h.Compress = 0
			out, _ := diag.EncodeMessage(h, items, false)
			return out
		}
	}
	return nil
}

// popupWith swaps a captured modal popup's DYNT_ATOM for our own screen, keeping
// it a real modal dialog. Empty when there is no popup to borrow.
func popupWith(popup []byte, scr *frame.Screen) []byte {
	if popup == nil {
		return nil
	}
	m, err := diag.ParseMessage(popup, false)
	if err != nil {
		return nil
	}
	items := diag.ParseItems(m.Body)
	for i, it := range items {
		if it.Type != diag.ItemAPPL4 || it.ID != 0x09 || it.SID != 0x02 {
			continue
		}
		items[i].Value = scr.Encode()
		h := m.Header
		h.Compress = 0
		out, err := diag.EncodeMessage(h, items, false)
		if err != nil {
			return nil
		}
		return out
	}
	return nil
}

func jokePopup1(popup []byte) []byte {
	return popupWith(popup, frame.New(6, 60).
		Text(1, 7, "Where are you going???").
		Text(2, 7, "FIORI???").
		Button(4, 7, 10, "No", "=NO1").
		Button(4, 20, 10, "No", "=NO2"))
}

func jokePopup2(popup []byte) []byte {
	return popupWith(popup, frame.New(6, 60).
		Text(1, 7, "=(").
		Button(3, 7, 10, "ok", "=OK"))
}
