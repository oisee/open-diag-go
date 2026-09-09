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
		return fr, len(fr) > 0
	}
	return nil, false
}

var rowidRe = regexp.MustCompile(`^0{6}\d{4}$`)

func tokens(b []byte) []string {
	var toks []string
	cur := []byte{}
	flush := func() {
		if s := strings.TrimSpace(string(cur)); s != "" {
			toks = append(toks, s)
		}
		cur = cur[:0]
	}
	for _, c := range b {
		if c >= 0x21 && c < 0x7f {
			cur = append(cur, c)
		} else if c == 0x20 {
			if len(cur) > 0 {
				cur = append(cur, c)
			}
		} else {
			flush()
		}
	}
	flush()
	return toks
}

func main() {
	rowsByFrame := map[int]int{}
	total := 0
	sampleShown := 0
	for conn := 1; conn <= 15; conn++ {
		c, err := replay.Load("captures/probe.jsonl", conn)
		if err != nil {
			continue
		}
		for _, f := range c.Server {
			m, err := diag.ParseMessage(f.Data, false)
			if err != nil {
				continue
			}
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
					for _, x := range lzhHeaders(outer) {
						xe := len(outer)
						for _, y := range lzhHeaders(outer) {
							if y > x && y < xe {
								xe = y
							}
						}
						nd, ok := dec(outer[x:xe])
						if !ok {
							continue
						}
						tk := tokens(nd)
						// a data row packet: token[0] is a 10-digit rowid, and
						// somewhere a message-class-like token + 3-digit + text
						if len(tk) >= 5 && rowidRe.MatchString(tk[0]) {
							// find SPRSL(1 upper), ARBGB, MSGNR(3 digit), TEXT
							var sprsl, arbgb, msgnr, text string
							for i := 1; i < len(tk); i++ {
								t := tk[i]
								if sprsl == "" && len(t) == 1 && t[0] >= 'A' && t[0] <= 'Z' {
									sprsl = t
									continue
								}
								if sprsl != "" && arbgb == "" && len(t) >= 2 && isClass(t) {
									arbgb = t
									continue
								}
								if arbgb != "" && msgnr == "" && len(t) == 3 && allDigit(t) {
									msgnr = t
									continue
								}
								if msgnr != "" && text == "" && len(t) > 3 {
									text = t
									break
								}
							}
							if sprsl != "" && arbgb != "" && msgnr != "" {
								total++
								rowsByFrame[f.Index]++
								if sampleShown < 12 {
									if len(text) > 66 {
										text = text[:66]
									}
									fmt.Printf("  row %s | SPRSL=%-2s ARBGB=%-20s MSGNR=%s | %s\n", tk[0], sprsl, arbgb, msgnr, text)
									sampleShown++
								}
							}
						}
					}
				}
			}
		}
	}
	fmt.Printf("\nTOTAL data-row packets decoded across capture: %d\n", total)
	fmt.Printf("frames carrying rows: ")
	for k, v := range rowsByFrame {
		fmt.Printf("#%d(%d) ", k, v)
	}
	fmt.Println()
}

func isClass(t string) bool {
	for _, c := range t {
		if !((c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '/') {
			return false
		}
	}
	return len(t) >= 2
}
func allDigit(t string) bool {
	for _, c := range t {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
