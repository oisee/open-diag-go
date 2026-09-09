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

var runRe = regexp.MustCompile(`[ -~]{6,}`)

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

// decompressTolerant accepts the +1 guard byte overrun.
func decompressTolerant(region []byte) ([]byte, bool) {
	out, err := sapcompress.Decompress(region)
	if err == nil {
		return out, true
	}
	out2, err2 := sapcompress.Decompress(dechunkStream(region))
	if err2 == nil {
		return out2, true
	}
	// manual inflate ignoring length mismatch
	if len(region) >= 8 && bytes.Equal(region[5:7], []byte{0x1f, 0x9d}) {
		if md, ok := manualInflate(region); ok {
			return md, true
		}
	}
	return nil, false
}

func manualInflate(data []byte) ([]byte, bool) {
	length := int(binary.LittleEndian.Uint32(data[:4]))
	body := data[8:]
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
	return fr, len(fr) > 0
}

func main() {
	path := "captures/probe.jsonl"
	needles := []string{"SPRSL", "ARBGB", "MSGNR", "T100", "T100 "}
	c, _ := replay.Load(path, 1)
	for _, f := range c.Server {
		m, err := diag.ParseMessage(f.Data, false)
		if err != nil {
			continue
		}
		for _, it := range diag.ParseItems(m.Body) {
			if !(it.Type == diag.ItemAPPL && it.ID == 0x08 && it.SID == 0x00) {
				continue
			}
			hdrs := lzhHeaders(it.Value)
			for _, h := range hdrs {
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
				// scan outer and nested for needles
				scan(f.Index, "outer", outer, needles)
				for _, nh := range lzhHeaders(outer) {
					nend := len(outer)
					for _, x := range lzhHeaders(outer) {
						if x > nh && x < nend {
							nend = x
						}
					}
					nd, ok := decompressTolerant(outer[nh:nend])
					if !ok {
						continue
					}
					scan(f.Index, fmt.Sprintf("nested@%d", nh), nd, needles)
				}
			}
		}
	}
}

func scan(frame int, where string, data []byte, needles []string) {
	for _, n := range needles {
		if bytes.Contains(data, []byte(n)) {
			idx := bytes.Index(data, []byte(n))
			fmt.Printf("\n*** frame #%d %s: found %q at %d (blob %d bytes) ***\n", frame, where, n, idx, len(data))
			lo := idx - 40
			if lo < 0 {
				lo = 0
			}
			hi := idx + 220
			if hi > len(data) {
				hi = len(data)
			}
			runs := runRe.FindAll(data[lo:hi], -1)
			for _, r := range runs {
				fmt.Printf("    | %s\n", strings.TrimRight(string(r), " "))
			}
			return
		}
	}
}
