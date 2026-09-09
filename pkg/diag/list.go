package diag

// A classic ABAP list — the output of WRITE statements in a report — does not
// travel over DIAG as a DYNT_ATOM screen. It is painted as a stream of
// positioned text runs. Each run is a short group of items in this order:
//
//	SBA  (item 0x0b, two bytes)   the cursor position: byte 0 row, byte 1 col
//	SFE  (item 0x0a, three bytes) the display attribute: byte 1 is the SAP
//	                              list colour, byte 0 tells a text run from a
//	                              ruled ULINE run
//	SLC  (item 0x13, two bytes)   the run's length, big-endian
//	VARINFO.0b (APPL id 0x0c sid 0x0b)  the run's characters, plain text
//
// The three attribute items always precede the VARINFO.0b they describe, so a
// segment binds to the most recent SBA, SFE and SLC seen before it. This was
// read off a captured report list whose content was known in full: a header
// line, ULINE separators and twenty numbered rows. Rows and columns lined up
// one to one with the known layout, and SLC equalled the following run's byte
// count on every one of its segments, so the positions and lengths are
// confirmed. The colour reading is confirmed too (see the colour constants).
// The remaining SFE bytes distinguish a ruled line from text but their exact
// bit meaning is inferred.

// SAP list colours. The value is SFE byte 1, the same numbering an ABAP report
// gives FORMAT COLOR. Off, Heading and Key were seen in the capture — Off on
// the body text, Heading on a column header, Key on the first (idx) column of
// every data row — and are confirmed. The rest are the standard SAP list
// palette, kept here for completeness and marked inferred by absence.
const (
	ColOff      = 0x00 // normal body text, no FORMAT COLOR
	ColHeading  = 0x01 // COL_HEADING
	ColNormal   = 0x02 // COL_NORMAL, inferred
	ColTotal    = 0x03 // COL_TOTAL, inferred
	ColKey      = 0x04 // COL_KEY
	ColPositive = 0x05 // COL_POSITIVE, inferred
	ColNegative = 0x06 // COL_NEGATIVE, inferred
	ColGroup    = 0x07 // COL_GROUP, inferred
)

// sfeRuled is SFE byte 0 on a ruled run — the horizontal line a ULINE draws,
// which arrives as a run of the character the SAP font renders as a line.
// A text run carries 0x0a there instead. Inferred.
const sfeRuled = 0x08

// ListSegment is one positioned, coloured run of text from a classic list.
type ListSegment struct {
	// Row and Col are 0-based, taken from the SBA item.
	Row, Col int
	// Length is the run length the SLC item declared; it equals len(Text) on
	// every segment of the capture.
	Length int
	// Color is the SAP list colour, SFE byte 1 (ColOff, ColKey, ...).
	Color byte
	// Attr is the three raw SFE bytes, for the display state Color does not
	// carry (a ruled run versus a text run).
	Attr [3]byte
	// Text is the run's characters, exactly as they arrived.
	Text string
	// Status says how well the segment's position and length are known: a run
	// with its own SBA is Confirmed, one that had to inherit an earlier
	// position is Inferred.
	Status Status
}

// Ruled reports whether the run is a ULINE ruled line rather than text.
// Inferred: SFE byte 0 was 0x08 on every ULINE run and 0x0a on every text run.
func (s ListSegment) Ruled() bool {
	return s.Attr[0] == sfeRuled
}

// ListLine is the segments that share one row, in the order they arrived.
type ListLine struct {
	Row      int
	Segments []ListSegment
}

// isListText reports whether an item is a VARINFO.0b list text run.
func isListText(it Item) bool {
	return it.Type == ItemAPPL && it.ID == 0x0c && it.SID == 0x0b
}

// HasListSegments reports whether a frame carries at least one list text run,
// which is what tells a classic list apart from a plain dynpro screen.
func HasListSegments(items []Item) bool {
	for _, it := range items {
		if isListText(it) {
			return true
		}
	}
	return false
}

// ParseListItems turns the SBA/SFE/SLC/VARINFO.0b stream of a frame into
// positioned, coloured text segments, one per VARINFO.0b run. Items that are
// not part of the list stream are ignored, so it is safe to hand it a whole
// frame's items.
func ParseListItems(items []Item) []ListSegment {
	var segs []ListSegment
	var sba, sfe, slc []byte
	for _, it := range items {
		switch {
		case it.Type == ItemSBA:
			sba = it.Value
		case it.Type == ItemSFE:
			sfe = it.Value
		case it.Type == ItemSLC:
			slc = it.Value
		case isListText(it):
			seg := ListSegment{Text: string(it.Value), Status: Confirmed}
			if len(sba) == 2 {
				seg.Row = int(sba[0])
				seg.Col = int(sba[1])
			} else {
				seg.Status = Inferred
			}
			if len(sfe) == 3 {
				seg.Attr = [3]byte{sfe[0], sfe[1], sfe[2]}
				seg.Color = sfe[1]
			}
			if len(slc) == 2 {
				seg.Length = int(slc[0])<<8 | int(slc[1])
			} else {
				seg.Length = len(it.Value)
			}
			segs = append(segs, seg)
			// Each run carries its own SBA/SFE/SLC, so consume them: a later run
			// that arrives without a fresh position is then marked Inferred
			// rather than silently reusing this one's.
			sba, sfe, slc = nil, nil, nil
		}
	}
	return segs
}

// GroupListLines gathers segments into lines by row, keeping the row order in
// which the rows first appear and each line's segments in arrival order.
func GroupListLines(segs []ListSegment) []ListLine {
	var lines []ListLine
	index := map[int]int{}
	for _, s := range segs {
		i, ok := index[s.Row]
		if !ok {
			index[s.Row] = len(lines)
			lines = append(lines, ListLine{Row: s.Row})
			i = len(lines) - 1
		}
		lines[i].Segments = append(lines[i].Segments, s)
	}
	return lines
}
