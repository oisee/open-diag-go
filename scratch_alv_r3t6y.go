package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"regexp"
	"strings"

	"github.com/oisee/open-diag-go-pro/pkg/diag"
	"github.com/oisee/open-diag-go-pro/pkg/replay"
	"github.com/oisee/vibing-steampunk/pkg/sapcompress"
)

func main() {
	path := "captures/probe.jsonl"
	var best diag.Item
	for conn := 1; conn <= 15; conn++ {
		c, err := replay.Load(path, conn)
		if err != nil {
			continue
		}
		for _, f := range c.Server {
			m, err := diag.ParseMessage(f.Data, false)
			if err != nil {
				continue
			}
			for _, it := range diag.ParseItems(m.Body) {
				if it.Type == diag.ItemAPPL && it.ID == 0x08 && it.SID == 0x00 && len(it.Value) > len(best.Value) {
					best = it
				}
			}
		}
	}
	p := best.Value

	var hdrs []int
	for base := 0; ; {
		k := bytes.Index(p[base:], []byte{0x1f, 0x9d})
		if k < 0 {
			break
		}
		abs := base + k
		if abs >= 5 {
			hdrs = append(hdrs, abs-5)
		}
		base = abs + 1
	}

	tokRe := regexp.MustCompile(`[!-~][ -~]{2,}[!-~]`)
	for si, h := range hdrs {
		end := len(p)
		for _, h2 := range hdrs {
			if h2 > h && h2 < end {
				end = h2
			}
		}
		dec := dechunkStream(p[h:end])
		out, err := sapcompress.Decompress(dec)
		if err != nil {
			fmt.Printf("stream %d @%d: FAIL %v\n", si, h, err)
			continue
		}
		fmt.Printf("\n=========== STREAM %d @%d, %d bytes ===========\n", si, h, len(out))
		// extract tokens (trim runs of >=3 printable, collapse spaces)
		toks := tokRe.FindAll(out, -1)
		// dedupe-ish: print unique trimmed tokens, up to 120
		seen := map[string]bool{}
		var uniq []string
		for _, t := range toks {
			s := strings.TrimSpace(string(t))
			// collapse multiple spaces
			s = strings.Join(strings.Fields(s), " ")
			if s == "" || seen[s] {
				continue
			}
			seen[s] = true
			uniq = append(uniq, s)
		}
		fmt.Printf("  %d unique tokens; first 120:\n", len(uniq))
		for i, s := range uniq {
			if i >= 120 {
				fmt.Printf("  ... (%d more)\n", len(uniq)-120)
				break
			}
			fmt.Printf("   | %s\n", s)
		}
		// Specifically search for T100 signals
		for _, needle := range []string{"SPRSL", "ARBGB", "MSGNR", "T100", "SABAP", "message", "Message"} {
			if bytes.Contains(out, []byte(needle)) {
				idx := bytes.Index(out, []byte(needle))
				fmt.Printf("  >> found %q at %d\n", needle, idx)
			}
		}
	}
}

func dechunkStream(region []byte) []byte {
	if len(region) < 8 {
		return region
	}
	out := append([]byte{}, region[:8]...)
	i := 8
	for i < len(region) {
		if i+6 <= len(region) && region[i] == 0x03 && region[i+1] == 0x05 && region[i+2] == 0x03 && region[i+3] == 0x05 {
			n := int(binary.BigEndian.Uint16(region[i+4:]))
			s := i + 6
			if s+n > len(region) {
				n = len(region) - s
			}
			out = append(out, region[s:s+n]...)
			i = s + n
			continue
		}
		out = append(out, region[i])
		i++
	}
	return out
}
