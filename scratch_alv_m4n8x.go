package main

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/oisee/open-diag-go-pro/pkg/diag"
	"github.com/oisee/open-diag-go-pro/pkg/replay"
	"github.com/oisee/vibing-steampunk/pkg/sapcompress"
)

var runRe = regexp.MustCompile(`[ -~]{4,}`)

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

func decompressTolerant(region []byte) ([]byte, bool) {
	if out, err := sapcompress.Decompress(region); err == nil {
		return out, true
	}
	if out, err := sapcompress.Decompress(dechunkStream(region)); err == nil {
		return out, true
	}
	if len(region) >= 8 && bytes.Equal(region[5:7], []byte{0x1f, 0x9d}) {
		length := int(binary.LittleEndian.Uint32(region[:4]))
		body := region[8:]
		if len(body) == 0 {
			return nil, false
		}
		prefix := uint(2 + body[0]&0x03)
		shifted := make([]byte, len(body))
		for i := range body {
			v := body[i] >> prefix
			if i+1 < len(body) {
				v |= body[i+1] << (8 - prefix)
			}
			shifted[i] = v
		}
		r := flate.NewReader(bytes.NewReader(shifted))
		defer r.Close()
		fr, _ := io.ReadAll(io.LimitReader(r, int64(length)+16))
		if len(fr) >= length {
			return fr[:length], true
		}
		if len(fr) > 0 {
			return fr, true
		}
	}
	return nil, false
}

func main() {
	path := "captures/probe.jsonl"
	c, _ := replay.Load(path, 1)
	for _, f := range c.Server {
		if f.Index != 316 {
			continue
		}
		m, _ := diag.ParseMessage(f.Data, false)
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
				outer, ok := decompressTolerant(it.Value[h:end])
				if !ok {
					continue
				}
				nested := lzhHeaders(outer)
				fmt.Printf("=== outer stream %d: %d bytes, %d nested ===\n", hi, len(outer), len(nested))
				for ni, nh := range nested {
					nend := len(outer)
					for _, x := range nested {
						if x > nh && x < nend {
							nend = x
						}
					}
					nd, ok := decompressTolerant(outer[nh:nend])
					if !ok {
						continue
					}
					// classify: field catalog vs data
					head := string(nd[:min(24, len(nd))])
					fmt.Printf("\n  --- nested %d @%d: %d bytes, head=%q ---\n", ni, nh, len(nd), strings.TrimRight(head, " "))
					dumpRecords(nd)
				}
			}
		}
	}
}

func dumpRecords(nd []byte) {
	runs := runRe.FindAll(nd, -1)
	shown := 0
	for _, r := range runs {
		s := strings.TrimRight(string(r), " ")
		if s == "" {
			continue
		}
		fmt.Printf("     %q\n", s)
		shown++
		if shown >= 40 {
			fmt.Printf("     ...(%d total runs)\n", len(runs))
			return
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
