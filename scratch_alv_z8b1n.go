package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"regexp"

	"github.com/oisee/open-diag-go-pro/pkg/diag"
	"github.com/oisee/open-diag-go-pro/pkg/replay"
	"github.com/oisee/vibing-steampunk/pkg/sapcompress"
)

// readable runs of >= 24 chars, allowing spaces
var runRe = regexp.MustCompile(`[ -~]{24,}`)

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

func lzhHeaders(p []byte) []int {
	var hdrs []int
	for base := 0; ; {
		k := bytes.Index(p[base:], []byte{0x1f, 0x9d})
		if k < 0 {
			break
		}
		abs := base + k
		if abs >= 5 && p[abs-1] == 0x12 { // alg byte 0x12 = LZH
			hdrs = append(hdrs, abs-5)
		}
		base = abs + 1
	}
	return hdrs
}

func main() {
	path := "captures/probe.jsonl"
	type frameRef struct {
		conn, idx, sid int
		val            []byte
	}
	var rfctrs []frameRef
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
				if it.Type == diag.ItemAPPL && it.ID == 0x08 {
					rfctrs = append(rfctrs, frameRef{conn, f.Index, int(it.SID), it.Value})
				}
			}
		}
	}
	fmt.Printf("total S->C RFC_TR items: %d\n", len(rfctrs))

	// For each, find LZH streams, decompress, and look for T100-ish readable content.
	interesting := regexp.MustCompile(`(?i)messag|SPRSL|ARBGB|MSGNR|ABAP|Function|error|number`)
	for _, fr := range rfctrs {
		hdrs := lzhHeaders(fr.val)
		if len(hdrs) == 0 {
			continue
		}
		for hi, h := range hdrs {
			end := len(fr.val)
			for _, h2 := range hdrs {
				if h2 > h && h2 < end {
					end = h2
				}
			}
			dec, err := sapcompress.Decompress(dechunkStream(fr.val[h:end]))
			if err != nil {
				continue
			}
			// Check for nested 1f 9d
			nested := lzhHeaders(dec)
			// Gather readable runs
			runs := runRe.FindAll(dec, -1)
			var hits []string
			for _, r := range runs {
				if interesting.Match(r) {
					hits = append(hits, string(r))
				}
			}
			if len(nested) > 0 || len(hits) > 0 {
				fmt.Printf("\n[conn %d #%d sid %02x] stream %d: %d bytes, nested-LZH=%d, %d readable runs, %d interesting\n",
					fr.conn, fr.idx, fr.sid, hi, len(dec), len(nested), len(runs), len(hits))
				for i, hstr := range hits {
					if i >= 12 {
						fmt.Printf("    ...(%d more)\n", len(hits)-12)
						break
					}
					if len(hstr) > 140 {
						hstr = hstr[:140]
					}
					fmt.Printf("    ~ %s\n", hstr)
				}
			}
		}
	}
}
