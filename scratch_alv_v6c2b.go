package main

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/oisee/open-diag-go-pro/pkg/diag"
	"github.com/oisee/open-diag-go-pro/pkg/replay"
	"github.com/oisee/vibing-steampunk/pkg/sapcompress"
)

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
func dec(region []byte) ([]byte, bool) {
	if out, err := sapcompress.Decompress(region); err == nil {
		return out, true
	}
	if out, err := sapcompress.Decompress(dechunkStream(region)); err == nil {
		return out, true
	}
	if len(region) >= 8 && bytes.Equal(region[5:7], []byte{0x1f, 0x9d}) {
		length := int(binary.LittleEndian.Uint32(region[:4]))
		body := region[8:]
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
		return fr, len(fr) > 0
	}
	return nil, false
}

func main() {
	c, _ := replay.Load("captures/probe.jsonl", 1)
	for _, f := range c.Server {
		if f.Index != 316 {
			continue
		}
		m, _ := diag.ParseMessage(f.Data, false)
		for _, it := range diag.ParseItems(m.Body) {
			if !(it.Type == diag.ItemAPPL && it.ID == 0x08 && it.SID == 0x00) {
				continue
			}
			for _, h := range lzhHeaders(it.Value) {
				end := len(it.Value)
				for _, h2 := range lzhHeaders(it.Value) {
					if h2 > h && h2 < end {
						end = h2
					}
				}
				outer, ok := dec(it.Value[h:end])
				if !ok {
					continue
				}
				nh := lzhHeaders(outer)
				for ni, x := range nh {
					if ni != 0 && ni != 4 {
						continue
					}
					xe := len(outer)
					for _, y := range nh {
						if y > x && y < xe {
							xe = y
						}
					}
					nd, ok := dec(outer[x:xe])
					if !ok {
						continue
					}
					if ni == 0 {
						fmt.Printf("\n##### FIELD CATALOG blob (nested 0), %d bytes #####\n", len(nd))
						fmt.Printf("%s\n", hex.Dump(nd[:min(320, len(nd))]))
					}
					if ni == 4 {
						fmt.Printf("\n##### DATA blob (nested 4), %d bytes #####\n", len(nd))
						fmt.Printf("%s\n", hex.Dump(nd[:min(900, len(nd))]))
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
