package main

// The light-show scene engine, copied from cmd/server's demo mode (same owner,
// no attribution needed). Every scene is driven by wall-clock time, so motion is
// the same speed whatever the frame cadence. The only change from the server
// copy: the sound insert (withSound) is dropped, and the wrapper frames come
// from the embedded asset instead of a capture file.

import (
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/oisee/open-diag-go/pkg/demo"
	"github.com/oisee/open-diag-go/pkg/diag"
	"github.com/oisee/open-diag-go/pkg/frame"
	"github.com/oisee/open-diag-go/pkg/replay"
)

// demoSceneMS is how long each scene runs (wall clock), settable via -scene-ms.
var demoSceneMS = 3000

// ---- LED list-channel scenes -------------------------------------------------

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

	scenes := demo.Scenes()
	def := time.Duration(demoSceneMS) * time.Millisecond
	if def <= 0 {
		def = 3 * time.Second
	}
	durs := make([]time.Duration, len(scenes))
	var total time.Duration
	for i, s := range scenes {
		d := s.Dur
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
			log("scene %d/%d: %s (%s)", idx+1, len(scenes), scenes[idx].Name, scenes[idx].Approach)
			lastScene = idx
		}
		if eff := demo.LEDEffectIndex(scenes[idx].Name); eff >= 0 && haveList {
			t := float64(int(ts/0.18)) * 0.15
			mine := diag.EncodeListItems(demo.LEDSegmentsEff(eff, t))
			out := append(append(append([]diag.Item{}, listKeep[:listInsertAt]...), mine...), listKeep[listInsertAt:]...)
			msg, err := diag.EncodeMessage(listHdr, out, false)
			if err != nil {
				return nil
			}
			return msg
		}
		scr := frame.New(27, 120)
		if scenes[idx].Dynpro != nil {
			scenes[idx].Dynpro(ts, scr)
		} else {
			scr.Text(2, 2, "LED effect needs the embedded list frame")
		}
		if !scenes[idx].Bare {
			scr.Text(24, 1, fmt.Sprintf("scene %d/%d  %-9s  approach: %s", idx+1, len(scenes), scenes[idx].Name, scenes[idx].Approach))
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

// isExit is how a session really ends, which is not what isClose knew.
// Closing the embedded SAP GUI tab in Eclipse sends the OK-code "/NEX" —
// leave every session, now — and answering that with another screen is how
// sapguiserver.exe ends up reporting a broken command pipe: it had said
// goodbye and we handed it a dynpro. Measured on our own capture
// (.local/capture/osd-f8, the 51-byte closing frame). The other exit codes
// are here for the same reason, since a GUI may send any of them.
func isExit(items []diag.Item) bool {
	for _, it := range items {
		if it.Type == diag.ItemAPPL && it.ID == 0x0c && it.SID == 0x04 {
			switch strings.ToUpper(strings.TrimSpace(string(it.Value))) {
			case "/I", "/NEX", "/NEND", "/N/EX":
				return true
			}
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

// ---- the stub screens ---------------------------------------------------------
//
// A stub is what this dispatcher shows when a system with no dialog programs
// is asked for one: a still screen, the same on every round trip. Four of
// them, and they are a little chronology of machines that stop and tell you
// so — Spectrum boot, Spectrum failed load, C64 ready, Amiga guru.
//
// They are deliberately NOT unified. Each is its own machine's wording,
// verbatim. What cannot be honoured is the palette: a DIAG dynpro places
// text on the GUI's canvas and does not set a PAPER, a border or a font, so
// the Spectrum's white-on-black, the C64's blue and the PETSCII glyphs are
// out of reach here. The words are the part that carries, and they do.

// stubNames is the chronology, oldest first, and stubWeights is how often
// each one comes up under "rotate". Halving: the tape error is the house
// screen and the guru is the one you are pleased to see.
var stubNames = []string{"tape", "boot", "c64", "guru"}
var stubWeights = []int{8, 4, 2, 1} // 0.53 / 0.27 / 0.13 / 0.07

// pickStub draws a screen at random by weight. It is a draw per connection,
// not a cycle, so two F8s in a row can land on the same screen — which is
// what a probability means and what makes the rare one worth something.
func pickStub() string {
	total := 0
	for _, w := range stubWeights {
		total += w
	}
	n := rand.Intn(total)
	for i, w := range stubWeights {
		if n < w {
			return stubNames[i]
		}
		n -= w
	}
	return stubNames[0]
}

// stubScreen draws the still screen named by -stub. "rotate" walks the
// chronology, one screen per connection, so n is the connection's number.
// An unknown name is the tape error, which is the default and the one that
// says the most: a machine that was asked to load something and could not.
func stubScreen(name string, n int) *frame.Screen {
	if name == "rotate" {
		name = pickStub()
	}
	switch name {
	case "boot":
		return stubSpectrumBoot()
	case "c64":
		return stubC64()
	case "guru":
		return stubGuru()
	default:
		return stubTape()
	}
}

// stubTape: the Spectrum's canonical failure. A tape that would not load,
// reported from the bottom line where the Spectrum reported everything.
// Nothing else on the screen, because the Spectrum said nothing else.
//
// It is the default on purpose, and not only for the joke: it is the true
// sentence. Something asked this system to load and run a program, and it
// could not. A later version could mean it literally — report a module or
// artefact that failed to load with the error the 1982 machine already had
// the right words for.
func stubTape() *frame.Screen {
	scr := frame.New(27, 120)
	scr.Text(22, 1, "R Tape loading error, 0:1")
	return scr
}

// stubSpectrumBoot: a 48K that has just been switched on. The copyright
// line at the bottom, and the K cursor waiting for a keyword. Inverse video
// is not ours to send, so the cursor is written the way it is written down.
func stubSpectrumBoot() *frame.Screen {
	scr := frame.New(27, 120)
	scr.Text(23, (120-27)/2, "(c) 1982 Oisee Research Ltd")
	return scr
}

// stubC64: the machine that greeted everyone with how much memory it had
// left. Forty columns of it, laid out as the C64 laid it out.
func stubC64() *frame.Screen {
	scr := frame.New(27, 120)
	scr.Text(2, 5, "**** COMMODORE 64 BASIC V2 ****")
	scr.Text(4, 2, "64K RAM SYSTEM  38911 BASIC BYTES FREE")
	scr.Text(6, 1, "READY.")
	return scr
}

// stubGuru: the Amiga's Guru Meditation. The number is the tradition's shape
// with our own words in it: 4F5344 spells OSD, and F8 is the key that brought
// the user here.
func stubGuru() *frame.Screen {
	const w = 100
	scr := frame.New(27, 120)
	scr.Frame(2, 8, w, 5, "")
	scr.Text(3, 8+(w-56)/2, "Software Failure.   Press left mouse button to continue.")
	scr.Text(5, 8+(w-36)/2, "Guru Meditation #4F534400.000000F8")
	scr.Text(9, 10, "This is open-steamgate: an ABAP system with no dialog layer.")
	scr.Text(10, 10, "It serves ADT, OData and RFC; a program run from Eclipse lands here,")
	scr.Text(11, 10, "on a dispatcher that draws one screen and holds the line.")
	scr.Text(13, 10, "Close the window to go back to Eclipse.")
	return scr
}

// ---- the stub screens, in the list channel ------------------------------------
//
// A classic list is a character grid the GUI draws in its fixed-pitch list
// font, and every run carries a colour that is a whole band — foreground and
// background together. So the list channel gives the stubs what a dynpro
// could not: a monospace face, a filled screen, and a border, all by writing
// runs of spaces in a colour.
//
// What it still does not give is a palette. The eight SAP list colours are
// pastel bands with dark text — there is no black paper, no saturated C64
// blue, no red-on-black. So these are not reproductions; they are the four
// machines rendered in the colours a SAP report has. The Spectrum comes off
// best, because black on white is what a Spectrum actually showed.

// ascii keeps a run to one byte per column, which is not a style rule but a
// crash fix. A list run declares its length in the SLC item, and both
// ListText and the dynpro atoms take that length from len(text) — the BYTE
// count. A multi-byte character therefore declares more columns than it
// paints, and SAP GUI does not survive the difference: a block cursor
// (U+2588, three bytes) took sapguiserver.exe down with a Sapfewdbg
// exception and a broken command pipe. Until a length is counted in runes,
// everything these screens send is 7-bit, and this is where that is enforced
// rather than remembered.
func ascii(s string) string {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r >= 0x20 && r < 0x7f {
			out = append(out, byte(r))
		} else {
			out = append(out, '?')
		}
	}
	return string(out)
}

// The list page, and it is not a choice: the embedded wrapper's CHL item
// declares the geometry and the GUI honours it. Measured off our own capture
// — CHL carries 0000001a (26 rows) and 00000078 (120 columns), twice.
//
// Painting a smaller page than the one declared is what made a second F8
// glitch while the first was clean: the columns past our width and the rows
// past our height keep whatever the previous session left there, and a fresh
// GUI has nothing there to show. So the grid is the whole page.
const listCols, listRows = 120, 26

// A classic list is written a line at a time, left to right, and never
// painted over: a report emits each row's runs once, in order, and they do
// not overlap. The first version of these screens ignored that and laid
// coloured rectangles on top of one another, which is not a shape any ABAP
// program produces — and SAP GUI met it by closing the connection, which
// Eclipse reported as a broken command pipe.
//
// So the screens draw into a grid of cells, and the grid is encoded per row
// with equal neighbours merged, exactly the way the LED scenes reach the
// list channel. Drawing stays as easy to write; what leaves is the overlap,
// and with it most of the runs.

type cell struct{ ch, colour byte }

type grid struct {
	rows, cols int
	cells      []cell
}

func newGrid() *grid {
	g := &grid{rows: listRows, cols: listCols}
	g.cells = make([]cell, g.rows*g.cols)
	for i := range g.cells {
		g.cells[i] = cell{ch: ' ', colour: diag.ColOff}
	}
	return g
}

// fill paints a rectangle in one colour, leaving the characters blank.
func (g *grid) fill(row, col, w, h int, colour byte) *grid {
	for r := row; r < row+h; r++ {
		for c := col; c < col+w; c++ {
			if r >= 0 && r < g.rows && c >= 0 && c < g.cols {
				g.cells[r*g.cols+c] = cell{ch: ' ', colour: colour}
			}
		}
	}
	return g
}

// text writes a string, keeping the colour the cells already have, so a line
// drawn onto a filled area stays on that area's band.
func (g *grid) text(row, col int, s string) *grid {
	for i, ch := range []byte(ascii(s)) {
		if c := col + i; row >= 0 && row < g.rows && c >= 0 && c < g.cols {
			g.cells[row*g.cols+c].ch = ch
		}
	}
	return g
}

// colourText writes a string and sets its cells' colour, for a run with a
// band of its own — a block cursor, say.
func (g *grid) colourText(row, col int, colour byte, s string) *grid {
	for i, ch := range []byte(ascii(s)) {
		if c := col + i; row >= 0 && row < g.rows && c >= 0 && c < g.cols {
			g.cells[row*g.cols+c] = cell{ch: ch, colour: colour}
		}
	}
	return g
}

// centre is the column at which text of this width starts if it is to sit in
// the middle of the page.
func (g *grid) centre(width int) int {
	if c := (g.cols - width) / 2; c > 0 {
		return c
	}
	return 0
}

// segments encodes the grid the way a report would have written it: per row,
// left to right, equal neighbours merged.
//
// Every row is written to its full width, and that is not waste. A list is
// cumulative — the GUI keeps the cells an earlier frame painted — so a row
// that stops short leaves whatever was there before it, and switching
// screens left the previous one's border down the right-hand side. Trimming
// the trailing blanks was an optimisation that cost the only property that
// matters here: a frame is a whole page, not a patch.
func (g *grid) segments() []diag.ListSegment {
	var out []diag.ListSegment
	for r := 0; r < g.rows; r++ {
		row := g.cells[r*g.cols : (r+1)*g.cols]
		end := g.cols
		for c := 0; c < end; {
			start, colour := c, row[c].colour
			var text []byte
			for c < end && row[c].colour == colour {
				text = append(text, row[c].ch)
				c++
			}
			out = append(out, diag.ListText(r, start, colour, string(text)))
		}
	}
	return out
}

// stubListSegments draws the named stub on the list grid.
func stubListSegments(name string, n int) []diag.ListSegment {
	if name == "rotate" {
		name = pickStub()
	}
	switch name {
	case "boot":
		return listSpectrumBoot()
	case "c64":
		return listC64()
	case "guru":
		return listGuru()
	default:
		return listTape()
	}
}

// spectrumStripes is the loading border every Spectrum owner watched: bands
// of colour down both edges and across the top and bottom. The real thing
// striped while the tape ran and stopped when it failed; this is the picture
// the error belongs to, in the four pastels nearest the original.
func spectrumStripes(g *grid) {
	bands := []byte{diag.ColNegative, diag.ColKey, diag.ColTotal, diag.ColPositive}
	for r := 0; r < g.rows; r++ {
		c := bands[(r/2)%len(bands)]
		g.fill(r, 0, 4, 1, c)
		g.fill(r, g.cols-4, 4, 1, c)
	}
	for i, r := range []int{0, 1, g.rows - 2, g.rows - 1} {
		g.fill(r, 4, g.cols-8, 1, bands[i%len(bands)])
	}
}

// listTape: the Spectrum's canonical failure, inside the striped border,
// printed from the left where the Spectrum printed its system messages. The
// centred line is the credit on the boot screen; they are not the same kind
// of line.
func listTape() []diag.ListSegment {
	g := newGrid()
	spectrumStripes(g)
	g.fill(2, 4, g.cols-8, g.rows-4, diag.ColOff)
	g.text(g.rows-4, 5, "R Tape loading error, 0:1")
	return g.segments()
}

// listSpectrumBoot: a 48K just switched on. A Spectrum's border surrounded
// the paper on all four sides, so it goes down first and the paper is inset
// into it; at boot the real border was the same white as the paper, and this
// is the palest band we have, which shows the shape without shouting.
func listSpectrumBoot() []diag.ListSegment {
	const credit = "(c) 1982 Oisee Research Ltd"
	g := newGrid()
	g.fill(0, 0, g.cols, g.rows, diag.ColNormal)
	g.fill(2, 4, g.cols-8, g.rows-4, diag.ColOff)
	g.text(g.rows-4, g.centre(len(credit)), credit)
	return g.segments()
}

// listC64: forty-odd columns of blue inside a darker frame, and the greeting
// that told you how much memory was left. The cursor is a one-character run
// in another band — a list colour carries foreground and background
// together, so that IS an inverse block, one ASCII byte wide.
func listC64() []diag.ListSegment {
	const w, h = 44, 18
	g := newGrid()
	left, top := (g.cols-w)/2, 2
	g.fill(top-2, left-4, w+8, h+4, diag.ColHeading)
	g.fill(top, left, w, h, diag.ColKey)
	g.text(top+2, left+6, "**** COMMODORE 64 BASIC V2 ****")
	g.text(top+4, left+2, "64K RAM SYSTEM  38911 BASIC BYTES FREE")
	g.text(top+6, left+1, "READY.")
	g.colourText(top+7, left+1, diag.ColHeading, " ")
	return g.segments()
}

// listGuru: the alert box. The Amiga's was red on black and blinked; a list
// has neither black nor a blink, so the red band carries it.
func listGuru() []diag.ListSegment {
	g := newGrid()
	const head = "Software Failure.   Press left mouse button to continue."
	const code = "Guru Meditation #4F534400.000000F8"
	g.fill(2, 4, g.cols-8, 5, diag.ColNegative)
	g.text(3, g.centre(len(head)), head)
	g.text(5, g.centre(len(code)), code)
	g.text(10, 6, "This is open-steamgate: an ABAP system with no dialog layer.")
	g.text(11, 6, "It serves ADT, OData and RFC; a program run from Eclipse lands here,")
	g.text(12, 6, "on a dispatcher that draws one screen and holds the line.")
	g.text(14, 6, "Close the window to go back to Eclipse.")
	return g.segments()
}

// listStubFrame splices the stub's runs into the embedded list wrapper, the
// same way the LED scenes reach the list channel: keep the wrapper's own
// items, drop its list runs, put ours where they were.
func listStubFrame(listWrap []byte, segs []diag.ListSegment) []byte {
	if listWrap == nil {
		return nil
	}
	lm, err := diag.ParseMessage(listWrap, false)
	if err != nil {
		return nil
	}
	var keep []diag.Item
	insertAt := -1
	for _, it := range diag.ParseItems(lm.Body) {
		isList := it.Type == diag.ItemSBA || it.Type == diag.ItemSFE || it.Type == diag.ItemSLC ||
			(it.Type == diag.ItemAPPL && it.ID == 0x0c && it.SID == 0x0b)
		if isList {
			if insertAt < 0 {
				insertAt = len(keep)
			}
			continue
		}
		keep = append(keep, it)
	}
	if insertAt < 0 {
		insertAt = len(keep)
	}
	hdr := lm.Header
	hdr.Compress = 0
	mine := diag.EncodeListItems(segs)
	out := append(append(append([]diag.Item{}, keep[:insertAt]...), mine...), keep[insertAt:]...)
	msg, err := diag.EncodeMessage(hdr, out, false)
	if err != nil {
		return nil
	}
	return msg
}
