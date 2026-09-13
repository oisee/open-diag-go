// lens reads a tap capture and says what went by: per frame the DIAG
// header, whether it was compressed and to what, and the items — with the
// diff against the previous frame in the same direction, since animation
// is difference. What it cannot explain it prints raw.
//
//	lens captures/probe.jsonl              # every frame
//	lens -only S->C -grep 0c captures/x    # server frames, items whose key mentions 0c
package main

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"unicode/utf16"

	"github.com/oisee/open-diag-go/pkg/alv"
	"github.com/oisee/open-diag-go/pkg/diag"
	"github.com/oisee/vibing-steampunk/pkg/sapcompress"
)

type line struct {
	At    time.Time `json:"at"`
	Dir   string    `json:"dir"`
	Conn  int       `json:"conn"`
	Index int       `json:"index"`
	Len   int       `json:"len"`
	Hex   string    `json:"hex"`
}

func main() {
	only := flag.String("only", "", "C->S or S->C")
	grep := flag.String("grep", "", "show only items whose key contains this")
	values := flag.Bool("values", false, "print item values (hex and text)")
	maxVal := flag.Int("max", 48, "bytes of a value to print")
	src := flag.Bool("src", false, "extract ABAP editor source: RFC_TR.01 (0x08/0x01) frames with SUBTYPE=ABAP_SOURCE, DATABIN LZH decoded as UTF-16LE")
	ole := flag.Bool("ole", false, "decode RFC_TR (0x08) OLE_FLUSH_CALL frames: SplitRFCTR strings + the inner VERBS/SVARS SAP-LZH streams")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "lens <capture.jsonl>")
		os.Exit(2)
	}
	if *ole {
		if err := runOLE(flag.Arg(0), *only, *maxVal); err != nil {
			fmt.Fprintln(os.Stderr, "lens: ole:", err)
			os.Exit(1)
		}
		return
	}
	if *src {
		if err := runSrc(flag.Arg(0)); err != nil {
			fmt.Fprintln(os.Stderr, "lens: src:", err)
			os.Exit(1)
		}
		return
	}
	f, err := os.Open(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "lens:", err)
		os.Exit(1)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	var first time.Time
	seenClient := map[int]bool{}
	previous := map[string]map[string][]byte{} // dir -> key -> value
	for sc.Scan() {
		var l line
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			continue
		}
		if first.IsZero() {
			first = l.At
		}
		if *only != "" && l.Dir != *only {
			continue
		}
		payload, _ := hex.DecodeString(l.Hex)
		firstFromClient := l.Dir == "C->S" && !seenClient[l.Conn]
		if l.Dir == "C->S" {
			seenClient[l.Conn] = true
		}
		fmt.Printf("== +%7.3fs %s conn %d #%d  %d bytes\n", l.At.Sub(first).Seconds(), l.Dir, l.Conn, l.Index, l.Len)
		if name, ok := diag.NIControl(payload); ok {
			fmt.Printf("   %s (NI keepalive, no DIAG header)\n", name)
			continue
		}
		m, err := diag.ParseMessage(payload, firstFromClient)
		if m == nil {
			fmt.Printf("   not diag: %v\n   %s\n", err, hex.EncodeToString(head(payload, 32)))
			continue
		}
		fmt.Printf("   %s", m.Header)
		if m.DP != nil {
			fmt.Printf("  dp=%d bytes", len(m.DP))
		}
		if m.Compressed {
			fmt.Printf("  compressed -> %d bytes", len(m.Body))
		}
		if m.Note != "" {
			fmt.Printf("  (%s)", m.Note)
		}
		fmt.Println()
		if err != nil {
			fmt.Printf("   %v\n   %s\n", err, hex.EncodeToString(head(payload, 64)))
			continue
		}
		items := diag.ParseItems(m.Body)
		now := map[string][]byte{}
		for _, it := range items {
			key := it.Key()
			now[key] = it.Value
			if *grep != "" && !strings.Contains(key, *grep) {
				continue
			}
			mark := " "
			if prev, ok := previous[l.Dir][key]; !ok {
				mark = "+"
			} else if string(prev) != string(it.Value) {
				mark = "~"
			}
			fmt.Printf("   %s %-32s %5d  %s", mark, key, len(it.Value), it.Status)
			if *values || it.Status == diag.Raw {
				v := head(it.Value, *maxVal)
				fmt.Printf("  %s  %q", hex.EncodeToString(v), printable(v))
			}
			fmt.Println()
			if it.Type == diag.ItemAPPL4 && it.ID == 0x09 && it.SID == 0x02 {
				printAtoms(it.Value)
			}
		}
		for key := range previous[l.Dir] {
			if _, ok := now[key]; !ok && (*grep == "" || strings.Contains(key, *grep)) {
				fmt.Printf("   - %-32s\n", key)
			}
		}
		previous[l.Dir] = now
	}
}

// printAtoms prints a DYNT_ATOM item as its atoms, one per line, with the
// screen painter's 1-based line and column.
func printAtoms(value []byte) {
	atoms, err := diag.ParseDyntAtoms(value)
	for _, a := range atoms {
		text := a.Value()
		switch a.EType {
		case diag.AtomPushbutton:
			text = fmt.Sprintf("%s -> %s", text, a.Function)
		case diag.AtomCheckbox, diag.AtomRadioButton:
			text = fmt.Sprintf("[%c] %s", a.State, text)
		}
		fmt.Printf("       @%-4d line %2d col %3d len %3d  %-8s attr=%02x  %s  %q\n", a.Offset, a.Row+1, a.Col+1, a.Length, a.TypeName(), a.Attr, a.Status, text)
	}
	if err != nil {
		fmt.Printf("       %v\n", err)
	}
}

func head(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}

func printable(b []byte) string {
	out := make([]rune, 0, len(b))
	for _, c := range b {
		if c >= 0x20 && c < 0x7f {
			out = append(out, rune(c))
		} else {
			out = append(out, '.')
		}
	}
	return string(out)
}

// oleText renders a decoded stream as text with non-printables dotted, capped.
func oleText(b []byte, max int) string {
	if len(b) > max {
		b = b[:max]
	}
	out := make([]rune, 0, len(b))
	for _, c := range b {
		if c >= 32 && c < 127 {
			out = append(out, rune(c))
		} else {
			out = append(out, '.')
		}
	}
	return string(out)
}

// labelStream guesses which of the three inner streams this is from its content.
func labelStream(b []byte) string {
	s := string(b)
	switch {
	case strings.Contains(s, "CreateObject") || strings.Contains(s, "CreateControl") || strings.Contains(s, "FreeObject"):
		return "VERBS"
	case strings.Contains(s, "_RESULT") || strings.Contains(s, "#"):
		return "SVARS-desc"
	default:
		return "SVARS-values"
	}
}

// runOLE decodes every RFC_TR (APPL 0x08) frame's OLE payload: the SplitRFCTR
// length-prefixed strings and each inner SAP-LZH stream (VERBS + the two SVARS
// tables), decompressed. It is the offline tool for the control-answer RE.
func runOLE(path, only string, maxVal int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	ci, si := -1, -1
	for sc.Scan() {
		var l line
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		if only != "" && l.Dir != only {
			// still advance counters below
		}
		d, err := hex.DecodeString(l.Hex)
		if err != nil || len(d) == 0 {
			continue
		}
		if _, ok := diag.NIControl(d); ok {
			continue
		}
		idx := 0
		firstClient := false
		if l.Dir == "C->S" {
			ci++
			idx = ci
			firstClient = ci == 0 && len(d) > diag.DPHeaderLen
		} else {
			si++
			idx = si
		}
		if only != "" && l.Dir != only {
			continue
		}
		m, err := diag.ParseMessage(d, firstClient)
		if err != nil {
			continue
		}
		for _, it := range diag.ParseItems(m.Body) {
			if (it.Type != diag.ItemAPPL && it.Type != diag.ItemAPPL4) || it.ID != 0x08 {
				continue
			}
			r := diag.SplitRFCTR(it.Value)
			fmt.Printf("== %s #%d  RFC_TR.%02x  %d bytes  dest=%q  %d strings\n", l.Dir, idx, it.SID, len(it.Value), r.Destination, len(r.Strings))
			for i, str := range r.Strings {
				if i >= 16 {
					fmt.Printf("     … +%d more strings\n", len(r.Strings)-16)
					break
				}
				fmt.Printf("     s[%d] %s\n", i, oleText([]byte(str), maxVal))
			}
			streams := alv.ExtractLZHStreams(it.Value)
			for i, st := range streams {
				dec, derr := sapcompress.Decompress(st)
				if derr != nil {
					fmt.Printf("   stream[%d] %d bytes: decompress error: %v\n", i, len(st), derr)
					continue
				}
				fmt.Printf("   stream[%d] -> %d bytes [%s]: %s\n", i, len(dec), labelStream(dec), oleText(dec, maxVal))
			}
		}
	}
	return sc.Err()
}

// u16le decodes little-endian UTF-16 bytes to a string.
func u16le(b []byte) string {
	if len(b)%2 == 1 {
		b = b[:len(b)-1]
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return string(utf16.Decode(u))
}

// runSrc extracts ABAP editor source from a capture. On Save, the TextEdit
// control returns its buffer in a C->S RFC_TR.01 (APPL 0x08/0x01) as a SAPLCNDP
// data-transfer envelope; the DATABIN value holds one SAP-LZH stream whose
// decompressed bytes are the source as UTF-16LE, lines delimited by CR. It
// prints each ABAP_SOURCE buffer it finds.
func runSrc(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	ci := -1
	found := 0
	for sc.Scan() {
		var l line
		if json.Unmarshal(sc.Bytes(), &l) != nil || l.Dir != "C->S" {
			continue
		}
		d, err := hex.DecodeString(l.Hex)
		if err != nil || len(d) == 0 {
			continue
		}
		if _, ok := diag.NIControl(d); ok {
			continue
		}
		ci++
		m, err := diag.ParseMessage(d, ci == 0 && len(d) > diag.DPHeaderLen)
		if err != nil {
			continue
		}
		for _, it := range diag.ParseItems(m.Body) {
			if !(it.Type == diag.ItemAPPL || it.Type == diag.ItemAPPL4) || it.ID != 0x08 || it.SID != 0x01 {
				continue
			}
			if !strings.Contains(string(it.Value), "ABAP_SOURCE") {
				continue
			}
			for _, st := range alv.ExtractLZHStreams(it.Value) {
				dec, derr := sapcompress.Decompress(st)
				if derr != nil {
					continue
				}
				txt := u16le(dec)
				if !strings.Contains(strings.ToUpper(txt), "REPORT") && !strings.Contains(txt, "FUNCTION") && !strings.Contains(txt, "CLASS") {
					continue
				}
				txt = strings.ReplaceAll(strings.ReplaceAll(txt, "\r\n", "\n"), "\r", "\n")
				fmt.Printf("== ABAP source, C->S #%d (SUBTYPE=ABAP_SOURCE, %d chars):\n", ci, len(txt))
				for _, ln := range strings.Split(txt, "\n") {
					fmt.Println(strings.TrimRight(ln, " \x00"))
				}
				found++
			}
		}
	}
	if found == 0 {
		fmt.Fprintln(os.Stderr, "lens: no ABAP_SOURCE buffer found (source rides C->S Save legs)")
	}
	return sc.Err()
}
