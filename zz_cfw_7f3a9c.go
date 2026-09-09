package main

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/oisee/open-diag-go-pro/pkg/diag"
)

type line struct {
	Dir   string `json:"dir"`
	Conn  int    `json:"conn"`
	Index int    `json:"index"`
	Hex   string `json:"hex"`
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

func main() {
	want := os.Args[1] // substring of Key to collect
	full := len(os.Args) > 2 && os.Args[2] == "full"
	f, _ := os.Open("captures/probe.jsonl")
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	seenClient := map[int]bool{}
	count := 0
	for sc.Scan() {
		var l line
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		payload, err := hex.DecodeString(l.Hex)
		if err != nil {
			continue
		}
		if _, ok := diag.NIControl(payload); ok {
			continue
		}
		firstFromClient := l.Dir == "C->S" && !seenClient[l.Conn]
		if l.Dir == "C->S" {
			seenClient[l.Conn] = true
		}
		m, err := diag.ParseMessage(payload, firstFromClient)
		if m == nil || err != nil {
			continue
		}
		for _, it := range diag.ParseItems(m.Body) {
			key := it.Key()
			if !strings.Contains(key, want) {
				continue
			}
			count++
			v := it.Value
			max := 64
			if full {
				max = len(v)
			}
			if len(v) > max {
				v = v[:max]
			}
			fmt.Printf("%-4s c%d#%-3d %-34s len=%-4d %s  %q\n", l.Dir, l.Conn, l.Index, key, len(it.Value), hex.EncodeToString(v), printable(v))
		}
	}
	fmt.Fprintf(os.Stderr, "\n%d matches for %q\n", count, want)
}
