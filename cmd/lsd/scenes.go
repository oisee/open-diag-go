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

// the list grid the stubs draw on: a classic 80x24 list page.
const listCols, listRows = 80, 24

// listClear paints the whole page before anything else. A classic list is
// cumulative — the GUI keeps the cells a previous frame painted — so without
// this a second screen arrives on top of the first and you read both at once
// (seen: the C64 box sitting in the Spectrum's striped border with the tape
// error still legible underneath). Clearing is also the cheap guard against
// the list growing without bound as screens are switched.
func listClear() []diag.ListSegment {
	return listFill(0, 0, listCols, listRows, diag.ColOff)
}

// listCentre is the column at which text of this width starts if it is to sit
// in the middle of the page.
func listCentre(width int) int {
	c := (listCols - width) / 2
	if c < 0 {
		return 0
	}
	return c
}

// listCursor is a block cursor, which is a one-character run in a colour that
// contrasts with what it sits on. That is the whole trick: a list colour is a
// band of foreground and background together, so a single space in another
// band IS an inverse block — pseudographics out of the palette, one ASCII
// byte wide, with none of the column arithmetic a drawing character costs.
// With text in it ("K") it is the Spectrum's inverse-video keyword cursor.
func listCursor(row, col int, text string) diag.ListSegment {
	return listSay(row, col, diag.ColHeading, text)
}

// listFill paints a solid rectangle by writing rows of spaces in one colour.
// listSay is ListText with the ascii guard, and every stub run goes through
// it rather than calling ListText directly.
func listSay(row, col int, colour byte, text string) diag.ListSegment {
	return diag.ListText(row, col, colour, ascii(text))
}

func listFill(row, col, w, h int, colour byte) []diag.ListSegment {
	out := make([]diag.ListSegment, 0, h)
	for r := 0; r < h; r++ {
		out = append(out, diag.ListText(row+r, col, colour, strings.Repeat(" ", w)))
	}
	return out
}

// stubListSegments draws the named stub on the list grid. Fills go down
// first and text on top, because a later run overwrites the cells an earlier
// one painted.
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
func spectrumStripes() []diag.ListSegment {
	bands := []byte{diag.ColNegative, diag.ColKey, diag.ColTotal, diag.ColPositive}
	var out []diag.ListSegment
	for r := 0; r < listRows; r++ {
		c := bands[(r/2)%len(bands)]
		out = append(out, listSay(r, 0, c, strings.Repeat(" ", 4)))
		out = append(out, listSay(r, listCols-4, c, strings.Repeat(" ", 4)))
	}
	for i, r := range []int{0, 1, listRows - 2, listRows - 1} {
		c := bands[i%len(bands)]
		out = append(out, listSay(r, 4, c, strings.Repeat(" ", listCols-8)))
	}
	return out
}

// listTape: the Spectrum's canonical failure, inside the striped border,
// reported from the bottom of the paper the way the Spectrum reported
// everything.
func listTape() []diag.ListSegment {
	const msg = "R Tape loading error, 0:1"
	out := listClear()
	out = append(out, spectrumStripes()...)
	out = append(out, listFill(2, 4, listCols-8, listRows-4, diag.ColOff)...)
	// left, where the Spectrum printed its system messages; the credit on the
	// boot screen is the centred one, and they are not the same kind of line
	out = append(out, listSay(listRows-4, 5, diag.ColOff, msg))
	return out
}

// listSpectrumBoot: a 48K just switched on — white paper, the K cursor
// waiting for a keyword, the copyright at the foot.
func listSpectrumBoot() []diag.ListSegment {
	const credit = "(c) 1982 Oisee Research Ltd"
	out := listClear()
	col := listCentre(len(credit))
	out = append(out, listSay(listRows-4, col, diag.ColOff, credit))
	out = append(out, listCursor(listRows-2, col, "K"))
	return out
}

// listC64: forty columns of blue, a border around them, and the greeting
// that told you how much memory was left.
func listC64() []diag.ListSegment {
	const w, h = 44, 18
	left, top := (listCols-w)/2, 2
	out := listClear()
	out = append(out, listFill(top-2, left-4, w+8, h+4, diag.ColHeading)...) // the border
	out = append(out, listFill(top, left, w, h, diag.ColKey)...)
	out = append(out, listSay(top+2, left+6, diag.ColKey, "**** COMMODORE 64 BASIC V2 ****"))
	out = append(out, listSay(top+4, left+2, diag.ColKey, "64K RAM SYSTEM  38911 BASIC BYTES FREE"))
	out = append(out, listSay(top+6, left+1, diag.ColKey, "READY."))
	out = append(out, listSay(top+7, left+1, diag.ColKey, "█"))
	return out
}

// listGuru: the alert box. The Amiga's was red on black and blinked; a list
// has neither black nor a blink, so it is the red band that carries it.
func listGuru() []diag.ListSegment {
	out := listClear()
	out = append(out, listFill(2, 4, listCols-8, 5, diag.ColNegative)...)
	out = append(out, listSay(3, 12, diag.ColNegative, "Software Failure.   Press left mouse button to continue."))
	out = append(out, listSay(5, 23, diag.ColNegative, "Guru Meditation #4F534400.000000F8"))
	out = append(out, listSay(10, 6, diag.ColOff, "This is open-steamgate: an ABAP system with no dialog layer."))
	out = append(out, listSay(11, 6, diag.ColOff, "It serves ADT, OData and RFC; a program run from Eclipse lands here,"))
	out = append(out, listSay(12, 6, diag.ColOff, "on a dispatcher that draws one screen and holds the line."))
	out = append(out, listSay(14, 6, diag.ColOff, "Close the window to go back to Eclipse."))
	return out
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
