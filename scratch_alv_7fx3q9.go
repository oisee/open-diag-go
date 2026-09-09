package main

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/oisee/open-diag-go-pro/pkg/diag"
	"github.com/oisee/open-diag-go-pro/pkg/replay"
	"github.com/oisee/vibing-steampunk/pkg/sapcompress"
)

func main() {
	path := "captures/probe.jsonl"
	var best diag.Item
	var bestConn, bestIdx int
	var bestBody []byte
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
				if it.Type == diag.ItemAPPL && it.ID == 0x08 && it.SID == 0x00 {
					if len(it.Value) > len(best.Value) {
						best = it
						bestConn = conn
						bestIdx = f.Index
						bestBody = m.Body
					}
				}
			}
		}
	}
	fmt.Printf("BIGGEST RFC_TR.00: conn %d frame #%d, value %d bytes (full body %d)\n", bestConn, bestIdx, len(best.Value), len(bestBody))
	p := best.Value
	fmt.Printf("\n=== First 96 bytes of RFC_TR.00 value ===\n%s\n", hex.Dump(p[:min(96, len(p))]))

	// Walk RFC_TR framing
	r := diag.SplitRFCTR(p)
	fmt.Printf("\n=== SplitRFCTR ===\nHeader %d bytes\nDestination(redacted len=%d)\nStrings (%d):\n", len(r.Header), len(r.Destination), len(r.Strings))
	for i, s := range r.Strings {
		// redact anything that could be a host/dest: only print short protocol tokens
		fmt.Printf("  [%2d] %q\n", i, s)
	}

	// Find XML_DATA_STREAM param and locate its value bytes.
	// Strategy: find the literal "XML_DATA_STREAM" occurrence in p, then the
	// inner blob starting with 03 05 03 05.
	needle := []byte("XML_DATA_STREAM")
	xi := bytes.Index(p, needle)
	fmt.Printf("\n'XML_DATA_STREAM' at offset %d\n", xi)
	// Look for the 03 05 03 05 signature
	sig := []byte{0x03, 0x05, 0x03, 0x05}
	si := bytes.Index(p, sig)
	fmt.Printf("First 03 05 03 05 chunk sig at offset %d\n", si)
	// find all occurrences
	var offs []int
	for base := 0; ; {
		k := bytes.Index(p[base:], sig)
		if k < 0 {
			break
		}
		offs = append(offs, base+k)
		base = base + k + 1
	}
	fmt.Printf("Occurrences of 03 05 03 05: %d -> first few: %v\n", len(offs), offs[:min(8, len(offs))])

	if si < 0 {
		fmt.Println("no chunk signature; abort")
		return
	}
	blob := p[si:]
	fmt.Printf("\n=== Inner blob starts at %d, length %d ===\n%s\n", si, len(blob), hex.Dump(blob[:min(256, len(blob))]))

	// Entropy of blob
	fmt.Printf("blob entropy: %.3f bits/byte\n", entropy(blob))

	// Interpret as chunk framing: [xx xx][len BE u16][data...]? The pattern is
	// 03 05 03 05 00 fa. Let's test: bytes 0-1 = 03 05, 2-3 = 03 05, 4-5 = 00 fa (250).
	// Hypothesis A: records of "03 05 <2-byte BE len> <payload>"? Let's scan.
	analyzeChunks(blob)

	// Try decompression attempts on the blob and de-chunked stream.
	tryAll("raw blob", blob)

	// De-chunk: assume 6-byte header 03 05 03 05 00 fa then 250 bytes, repeat.
	dechunked := dechunk(blob)
	fmt.Printf("\nDe-chunked stream length: %d\n", len(dechunked))
	if len(dechunked) > 0 {
		fmt.Printf("%s\n", hex.Dump(dechunked[:min(128, len(dechunked))]))
		fmt.Printf("dechunked entropy: %.3f\n", entropy(dechunked))
		tryAll("dechunked", dechunked)
	}
}

func analyzeChunks(blob []byte) {
	fmt.Printf("\n=== Chunk structure scan (first 12 records) ===\n")
	i := 0
	for rec := 0; rec < 12 && i+6 <= len(blob); rec++ {
		h := blob[i : i+6]
		l16_45 := binary.BigEndian.Uint16(blob[i+4:])
		fmt.Printf("rec %2d @ %5d: hdr % x  (bytes4-5 BE=%d)\n", rec, i, h, l16_45)
		// advance by 6 + 250? Test constant stride
		i += int(l16_45) + 6
	}
}

func dechunk(blob []byte) []byte {
	var out []byte
	i := 0
	for i+6 <= len(blob) {
		// header: 2 bytes tag, 2 bytes tag2, 2 bytes BE length
		n := int(binary.BigEndian.Uint16(blob[i+4:]))
		start := i + 6
		if start+n > len(blob) {
			// take remainder
			out = append(out, blob[start:]...)
			break
		}
		out = append(out, blob[start:start+n]...)
		i = start + n
	}
	return out
}

func tryAll(label string, data []byte) {
	fmt.Printf("\n--- decompression attempts on %s (%d bytes) ---\n", label, len(data))
	// sapcompress (expects 1f 9d at [5:7])
	if _, err := sapcompress.Decompress(data); err != nil {
		fmt.Printf("sapcompress.Decompress: %v\n", err)
	} else {
		fmt.Printf("sapcompress.Decompress: SUCCESS\n")
	}
	// try skipping leading bytes to find 1f 9d
	if k := bytes.Index(data[:min(512, len(data))], []byte{0x1f, 0x9d}); k >= 0 {
		fmt.Printf("found 1f 9d at offset %d\n", k)
	} else {
		fmt.Printf("no 1f 9d magic in first 512 bytes\n")
	}
	// raw flate at several starting offsets
	for _, off := range []int{0, 2, 4, 6, 8} {
		if off >= len(data) {
			continue
		}
		if out, ok := tryFlate(data[off:]); ok {
			fmt.Printf("raw flate @off %d: got %d bytes, sample: %q\n", off, len(out), sample(out))
		}
	}
	// zlib
	if out, ok := tryZlib(data); ok {
		fmt.Printf("zlib: got %d bytes, sample: %q\n", len(out), sample(out))
	}
}

func tryFlate(d []byte) ([]byte, bool) {
	r := flate.NewReader(bytes.NewReader(d))
	defer r.Close()
	out, err := io.ReadAll(io.LimitReader(r, 1<<20))
	if err != nil && len(out) < 32 {
		return nil, false
	}
	if len(out) < 32 {
		return nil, false
	}
	return out, true
}

func tryZlib(d []byte) ([]byte, bool) {
	r, err := zlib.NewReader(bytes.NewReader(d))
	if err != nil {
		return nil, false
	}
	defer r.Close()
	out, err := io.ReadAll(io.LimitReader(r, 1<<20))
	if err != nil && len(out) < 32 {
		return nil, false
	}
	if len(out) < 32 {
		return nil, false
	}
	return out, true
}

func entropy(d []byte) float64 {
	var freq [256]int
	for _, b := range d {
		freq[b]++
	}
	var e float64
	n := float64(len(d))
	for _, f := range freq {
		if f == 0 {
			continue
		}
		p := float64(f) / n
		e -= p * (log2(p))
	}
	return e
}

func log2(x float64) float64 {
	return math.Log2(x)
}

func sample(b []byte) string {
	if len(b) > 120 {
		b = b[:120]
	}
	return string(b)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var _ = os.Stdout
