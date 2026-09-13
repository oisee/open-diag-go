package main

// The light-show scene engine, copied from cmd/server's demo mode (same owner,
// no attribution needed). Every scene is driven by wall-clock time, so motion is
// the same speed whatever the frame cadence. The only change from the server
// copy: the sound insert (withSound) is dropped, and the wrapper frames come
// from the embedded asset instead of a capture file.

import (
	"fmt"
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
