package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"regexp"

	"github.com/oisee/open-diag-go-pro/pkg/diag"
	"github.com/oisee/open-diag-go-pro/pkg/replay"
	"github.com/oisee/vibing-steampunk/pkg/sapcompress"
)

var runRe = regexp.MustCompile(`[ -~]{8,}`)

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
		if abs >= 5 && p[abs-1] == 0x12 {
			hdrs = append(hdrs, abs-5)
		}
		base = abs + 1
	}
	return hdrs
}

func main() {
	path := "captures/probe.jsonl"
	c, _ := replay.Load(path, 1)
	// target frames
	targets := map[int]bool{10: true, 42: true, 316: true}
	for _, f := range c.Server {
		if !targets[f.Index] {
			continue
		}
		m, err := diag.ParseMessage(f.Data, false)
		if err != nil {
			continue
		}
		for _, it := range diag.ParseItems(m.Body) {
			if !(it.Type == diag.ItemAPPL && it.ID == 0x08 && it.SID == 0x00) {
				continue
			}
			hdrs := lzhHeaders(it.Value)
			for hi, h := range hdrs {
				end := len(it.Value)
				for _, h2 := range hdrs {
					if h2 > h && h2 < end {
						end = h2
					}
				}
				outer, err := sapcompress.Decompress(dechunkStream(it.Value[h:end]))
				if err != nil {
					continue
				}
				nested := lzhHeaders(outer)
				if len(nested) == 0 {
					continue
				}
				fmt.Printf("\n############ frame #%d outer-stream %d (%d bytes) has %d nested LZH ############\n", f.Index, hi, len(outer), len(nested))
				for ni, nh := range nested {
					nend := len(outer)
					for _, x := range nested {
						if x > nh && x < nend {
							nend = x
						}
					}
					// nested blobs are NOT chunked (they are inside an already-dechunked stream)
					decl := binary.LittleEndian.Uint32(outer[nh : nh+4])
					nd, err := sapcompress.Decompress(outer[nh:nend])
					if err != nil {
						// try dechunk anyway
						nd, err = sapcompress.Decompress(dechunkStream(outer[nh:nend]))
					}
					if err != nil {
						fmt.Printf("  nested %d @%d (declLen %d): FAIL %v\n", ni, nh, decl, err)
						continue
					}
					runs := runRe.FindAll(nd, -1)
					fmt.Printf("  --- nested %d @%d: %d bytes (%d readable runs) ---\n", ni, nh, len(nd), len(runs))
					// Print first up-to-25 readable runs
					shown := 0
					for _, r := range runs {
						s := string(r)
						fmt.Printf("      %q\n", s)
						shown++
						if shown >= 25 {
							fmt.Printf("      ...(%d more runs)\n", len(runs)-25)
							break
						}
					}
					if len(runs) == 0 {
						fmt.Printf("      [no readable runs] head: % x\n", nd[:min(48, len(nd))])
						fmt.Printf("%s\n", hex.Dump(nd[:min(96, len(nd))]))
					}
				}
			}
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
