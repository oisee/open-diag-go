package main

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
	"io"
	"strings"

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

// extract ordered non-space ASCII tokens (keep single chars)
func tokens(b []byte) []string {
	var toks []string
	cur := []byte{}
	for _, c := range b {
		if c >= 0x21 && c < 0x7f {
			cur = append(cur, c)
		} else if c == 0x20 {
			// space: word boundary within a field; keep building only if we
			// already have content and next is also printable — simpler: treat
			// space as separator but merge multi-word text later
			if len(cur) > 0 {
				cur = append(cur, c)
			}
		} else {
			if s := strings.TrimSpace(string(cur)); s != "" {
				toks = append(toks, s)
			}
			cur = cur[:0]
		}
	}
	if s := strings.TrimSpace(string(cur)); s != "" {
		toks = append(toks, s)
	}
	return toks
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
					toks := tokens(nd)
					// data blobs: contain a message text (has spaces / long)
					hasText := false
					for _, t := range toks {
						if len(t) > 25 {
							hasText = true
						}
					}
					if hasText {
						fmt.Printf("\n===== DATA packet nested %d (%d bytes): %d tokens =====\n", ni, len(nd), len(toks))
						for i, t := range toks {
							fmt.Printf("  [%2d] %q\n", i, t)
							if i >= 30 {
								fmt.Printf("  ...(%d more)\n", len(toks)-30)
								break
							}
						}
					}
				}
			}
		}
	}
}
