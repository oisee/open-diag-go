// Package cfw decodes the SAP DIAG Control Framework's OLE-automation
// exchange and models the state a client must keep to ANSWER it, instead of
// replaying captured answers.
//
// Each server RFC_TR.00 (an OLE_FLUSH_CALL into program SAPLOLEA) carries
// three fixed-width ASCII streams, SAP-LZH-compressed, inside its
// XML_DATA_STREAM (recover them with Streams):
//
//   - VERBS: the script. 53-byte records, one per automation verb —
//     objId right-aligned in [0:9], verb name in [10:52], a flag byte at [52]
//     (C = call a method, S = set a property, G = get a property).
//   - SVARS descriptor: 44-byte records — a slot/group/type prefix in [0:8]
//     and a name at [8:] that is either "#N" (a positional input the server
//     fills) or "_RESULT" (an output slot the FRONTEND fills).
//   - SVARS value pool: 337-byte records holding the values, output slots
//     carrying the frontend-minted object handles as "000000000O<n>".
//
// The crux (see the cfw-ole-automation-model memory / KNOWLEDGE §8): OLE
// object handles "O<n>" are minted by the frontend and returned in the
// _RESULT slots; the server stores them and feeds them back as inputs in
// later calls. Replaying a fixed answer hands back a foreign session's
// handles, so the first call whose layout diverges dereferences a handle this
// session never minted and SAPLOLEA dumps (MESSAGE X373 '-1'). The Engine here
// mints this session's own handles as it walks the verbs.
//
// This file is the DECODER and state model. Byte-exact answer emission needs
// the value-pool sub-layout and the inter-string tag grammar pinned first; it
// is deliberately not attempted here.
package cfw

import (
	"fmt"
	"strings"

	"github.com/oisee/open-diag-go-pro/pkg/alv"
	"github.com/oisee/vibing-steampunk/pkg/sapcompress"
)

// stream record widths, pinned on captures/probe.jsonl frame S->C #6.
const (
	verbRecLen  = 53
	descRecLen  = 44
	valueRecLen = 337
	verbNameOff = 10 // verb name column start within a 53-byte record
	verbFlagOff = 52 // the C/S/G flag byte
	descNameOff = 8  // the #N / _RESULT name column within a 44-byte record
)

// Verb is one automation instruction from the VERBS stream.
type Verb struct {
	ObjID string // the OLE object the verb runs against
	Name  string // CreateObject, CreateControl, SetProperty, GetContainer, …
	Flag  byte   // 'C' call method, 'S' set property, 'G' get property
}

// Creates reports whether the verb makes a new OLE object, and so has a
// _RESULT slot the frontend must fill with a freshly minted handle.
func (v Verb) Creates() bool {
	switch v.Name {
	case "CreateObject", "CreateControl", "CreateControl2":
		return true
	}
	return false
}

// SvarsRec is one record of the SVARS descriptor table.
type SvarsRec struct {
	Fields  []string // the record's whitespace-separated columns
	Name    string   // "#N" (server-filled input) or "_RESULT" (frontend output)
	Pointer string   // for a _RESULT slot, the value-table pointer that follows it
}

// IsResult reports whether this descriptor slot is a frontend output slot.
func (r SvarsRec) IsResult() bool { return r.Name == "_RESULT" }

// Streams recovers and decompresses the inner streams of an RFC_TR OLE call
// (the raw APPL 0x08 item value) and returns them by role. A call usually
// carries three (VERBS, SVARS descriptor, value pool) but some carry only one
// or two (a data-only leg, or a call with no results), so each is classified
// by content rather than by position. A missing role comes back nil.
func Streams(rfctrValue []byte) (verbs, desc, values []byte, err error) {
	raw := alv.ExtractLZHStreams(rfctrValue)
	if len(raw) == 0 {
		return nil, nil, nil, fmt.Errorf("cfw: no inner streams")
	}
	for _, r := range raw {
		dec, e := sapcompress.Decompress(r)
		if e != nil {
			continue
		}
		switch classifyStream(dec) {
		case streamVerbs:
			verbs = dec
		case streamValues:
			values = dec
		default: // descriptor
			desc = dec
		}
	}
	return verbs, desc, values, nil
}

type streamKind int

const (
	streamDesc streamKind = iota
	streamVerbs
	streamValues
)

// classifyStream identifies an inner stream by content: a verb name marks the
// VERBS script; the 9-zero value prefix marks the value pool (which also holds
// _RESULT, so check it first); otherwise it is the descriptor.
func classifyStream(b []byte) streamKind {
	s := string(b)
	if strings.Contains(s, "CreateObject") || strings.Contains(s, "CreateControl") ||
		strings.Contains(s, "SetProperty") || strings.Contains(s, "FreeObject") ||
		strings.Contains(s, "GetContainer") {
		return streamVerbs
	}
	if strings.Contains(s, "000000000") {
		return streamValues
	}
	return streamDesc
}

// ParseVerbs splits the VERBS stream into its 53-byte records.
func ParseVerbs(b []byte) []Verb {
	var out []Verb
	for off := 0; off+verbRecLen <= len(b); off += verbRecLen {
		rec := b[off : off+verbRecLen]
		name := strings.TrimSpace(string(rec[verbNameOff:verbFlagOff]))
		if name == "" {
			continue
		}
		out = append(out, Verb{
			ObjID: strings.TrimSpace(string(rec[0:verbNameOff])),
			Name:  name,
			Flag:  rec[verbFlagOff],
		})
	}
	return out
}

// ParseSvarsDesc splits the SVARS descriptor stream into its 44-byte records.
func ParseSvarsDesc(b []byte) []SvarsRec {
	var out []SvarsRec
	for off := 0; off+descRecLen <= len(b); off += descRecLen {
		fields := strings.Fields(string(b[off : off+descRecLen]))
		if len(fields) == 0 {
			continue
		}
		r := SvarsRec{Fields: fields}
		// The name is the "#N" or "_RESULT" column; a _RESULT slot is followed
		// by its value-table pointer.
		for i, f := range fields {
			if f == "_RESULT" || (len(f) > 1 && f[0] == '#') {
				r.Name = f
				if f == "_RESULT" && i+1 < len(fields) {
					r.Pointer = fields[i+1]
				}
				break
			}
		}
		out = append(out, r)
	}
	return out
}

// SvarsVal is one record of the SVARS value pool: the value carried in a slot,
// or a _RESULT output slot the frontend fills with a handle.
type SvarsVal struct {
	IsResult bool   // the slot the frontend writes a minted handle into
	Value    string // the value, with the 9-zero prefix stripped ("101", "O2", …)
}

// ParseSvarsValues splits the SVARS value pool into its 337-byte records. Each
// carries a value at offset 32 as "000000000<value>" (a handle like O2 has no
// separating space; a plain value like 101 does); a _RESULT record marks its
// name at offset 0.
func ParseSvarsValues(b []byte) []SvarsVal {
	var out []SvarsVal
	for off := 0; off+valueRecLen <= len(b); off += valueRecLen {
		rec := b[off : off+valueRecLen]
		name := strings.TrimSpace(string(rec[0:valNameCol]))
		val := strings.TrimSpace(string(rec[valNameCol:]))
		val = strings.TrimSpace(strings.TrimPrefix(val, "000000000"))
		out = append(out, SvarsVal{IsResult: name == "_RESULT", Value: val})
	}
	return out
}

// ResultSlot resolves a descriptor _RESULT pointer (1-based, as ParseSvarsDesc
// returns it) to its index in the value-pool records, or -1.
func ResultSlot(pointer string) int {
	var n int
	if _, err := fmt.Sscanf(pointer, "%d", &n); err != nil || n < 1 {
		return -1
	}
	return n - 1
}

// valNameCol is where a value-pool record's value begins; [0:valNameCol] holds
// the _RESULT marker when present.
const valNameCol = 32

// Engine holds one session's OLE automation state: the monotonic handle
// counter and the objId->handle map. It is what replaces blind answer replay.
type Engine struct {
	next    int
	Handles map[string]string // objId -> minted "O<n>"
	Minted  []string          // handles minted this session, in order
}

// NewEngine starts a fresh session engine.
func NewEngine() *Engine {
	return &Engine{next: 1, Handles: map[string]string{}}
}

// mint returns the next frontend-owned OLE handle ("O1", "O2", …) and records
// it. Whether the kernel accepts a plain counter at face value, or validates
// the numeric handle, is the open go/no-go a live A4H run must answer.
func (e *Engine) mint() string {
	h := fmt.Sprintf("O%d", e.next)
	e.next++
	e.Minted = append(e.Minted, h)
	return h
}

// Run walks a call's verbs and mints a handle for each object-creating verb,
// binding it under the verb's objId so later calls that feed the handle back
// resolve. It returns the handles minted, in verb order. (This is the state
// half; wiring these into the _RESULT descriptor slots of an emitted RFC_TR.01
// is the next phase, once the value-pool layout is pinned.)
func (e *Engine) Run(verbs []Verb) []string {
	var minted []string
	for _, v := range verbs {
		if !v.Creates() {
			continue
		}
		h := e.mint()
		if v.ObjID != "" {
			e.Handles[v.ObjID] = h
		}
		minted = append(minted, h)
	}
	return minted
}
