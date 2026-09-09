package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"

	"github.com/oisee/open-diag-go-pro/pkg/diag"
	"github.com/oisee/open-diag-go-pro/pkg/replay"
	"github.com/oisee/vibing-steampunk/pkg/sapcompress"
)

func mn(a, b int) int {
	if a < b {
		return a
	}
	return b
}

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
	fmt.Printf("value %d bytes\n", len(p))

	// Find all 1f 9d, header starts 5 before
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
	for _, h := range hdrs {
		hdr := p[h : h+8]
		length := binary.LittleEndian.Uint32(p[h : h+4])
		fmt.Printf("\n########## header @ %d: % x  (declared len=%d, alg byte %02x)\n", h, hdr, length, p[h+4])
		out, err := sapcompress.Decompress(p[h:])
		if err != nil {
			fmt.Printf("  contiguous decompress FAILED: %v\n", err)
			// Try de-chunking: strip 03 05 03 05 00 fa markers within this stream region
			end := len(p)
			for _, h2 := range hdrs {
				if h2 > h && h2 < end {
					end = h2
				}
			}
			region := p[h:end]
			dec := dechunkStream(region)
			fmt.Printf("  region %d..%d = %d bytes; dechunked to %d bytes\n", h, end, len(region), len(dec))
			out2, err2 := sapcompress.Decompress(dec)
			if err2 != nil {
				fmt.Printf("  dechunked decompress FAILED: %v\n", err2)
				continue
			}
			out = out2
		}
		fmt.Printf("  DECOMPRESSED OK: %d bytes\n", len(out))
		show(out)
	}
}

// dechunkStream: within a region that begins with an 8-byte SAP header, the
// compressed body may be fragmented into 250-byte RFC table rows each prefixed
// by 03 05 03 05 00 fa. Keep the 8-byte header, then for the remainder strip
// any 6-byte 03 05 03 05 00 XX row markers and concatenate payloads.
func dechunkStream(region []byte) []byte {
	if len(region) < 8 {
		return region
	}
	out := append([]byte{}, region[:8]...) // SAP header
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

func show(out []byte) {
	fmt.Printf("  --- first 400 bytes as text ---\n")
	txt := make([]byte, 0, 400)
	for _, c := range out[:mn(400, len(out))] {
		if c == 0 {
			txt = append(txt, '.')
		} else {
			txt = append(txt, c)
		}
	}
	fmt.Printf("%s\n", string(txt))
	fmt.Printf("  --- hexdump first 128 ---\n%s\n", hex.Dump(out[:mn(128, len(out))]))
}
