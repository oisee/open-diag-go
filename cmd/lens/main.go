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

	"github.com/oisee/open-diag-go-pro/pkg/diag"
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
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "lens <capture.jsonl>")
		os.Exit(2)
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
