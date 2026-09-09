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

	// 1. Search whole value for 1f 9d
	var m19 []int
	for base := 0; ; {
		k := bytes.Index(p[base:], []byte{0x1f, 0x9d})
		if k < 0 {
			break
		}
		m19 = append(m19, base+k)
		base += k + 1
	}
	fmt.Printf("1f 9d occurrences in RFC_TR value: %v\n", m19)
	for _, o := range m19 {
		lo := o - 5
		if lo < 0 {
			lo = 0
		}
		fmt.Printf("  around %d: % x\n", o, p[lo:mn(o+8, len(p))])
	}

	// 2. Dump region before the blob (offset 1052)
	fmt.Printf("\n=== bytes 1000..1060 (just before blob) ===\n%s\n", hex.Dump(p[1000:1060]))

	// 3. Re-examine chunk framing precisely. Count consecutive 03 05 03 05 chunks.
	// Reassemble ALL 250-byte payloads that carry the 03 05 03 05 00 fa header.
	fmt.Printf("\n=== full chunk walk ===\n")
	i := 1052
	var reasm []byte
	nfull := 0
	for i+6 <= len(p) {
		tag := p[i : i+4]
		n := int(binary.BigEndian.Uint16(p[i+4:]))
		if bytes.Equal(tag, []byte{0x03, 0x05, 0x03, 0x05}) && n == 250 && i+6+250 <= len(p) {
			reasm = append(reasm, p[i+6:i+6+250]...)
			i += 256
			nfull++
			continue
		}
		fmt.Printf("chunk walk stopped at %d after %d full 250-rows; here: % x\n", i, nfull, p[i:mn(i+16, len(p))])
		break
	}
	fmt.Printf("reassembled %d bytes from %d full rows\n", len(reasm), nfull)
	fmt.Printf("%s\n", hex.Dump(reasm[:mn(64, len(reasm))]))

	// 4. Try SAP-LZH-style bit-shift inflate on reassembled (prefix = 2 + low2bits)
	tryLZHBody(reasm)

	// 5. Also: maybe there's a leading 4-byte length in reassembled = uncompressed size
	if len(reasm) >= 8 {
		l0 := binary.LittleEndian.Uint32(reasm[:4])
		l0b := binary.BigEndian.Uint32(reasm[:4])
		fmt.Printf("reasm[0:4] LE=%d BE=%d ; reasm[0:8]=% x\n", l0, l0b, reasm[:8])
	}

	// 6. brute raw-flate at every byte offset in first 300 of reasm, report any that yields printable
	fmt.Printf("\n=== brute raw-flate scan on reasm (offset 0..300) ===\n")
	for off := 0; off < 300 && off < len(reasm); off++ {
		if out, ok := flateTry(reasm[off:]); ok {
			printable := printablePct(out)
			if printable > 0.5 || len(out) > 200 {
				fmt.Printf("off %d -> %d bytes, printable %.0f%%, sample %q\n", off, len(out), printable*100, samp(out))
			}
		}
	}
	fmt.Println("(scan done)")
}

func tryLZHBody(body []byte) {
	fmt.Printf("\n=== SAP-LZH bit-shift inflate on reassembled ===\n")
	if len(body) == 0 {
		return
	}
	for pfx := uint(0); pfx <= 9; pfx++ {
		shifted := make([]byte, len(body))
		for i := range body {
			v := body[i] >> pfx
			if i+1 < len(body) {
				v |= body[i+1] << (8 - pfx)
			}
			shifted[i] = v
		}
		r := flate.NewReader(bytes.NewReader(shifted))
		out, err := io.ReadAll(io.LimitReader(r, 1<<20))
		r.Close()
		fmt.Printf("prefix %d: %d bytes out, err=%v, sample=%q\n", pfx, len(out), errbrief(err), samp(out))
	}
}

func flateTry(d []byte) ([]byte, bool) {
	r := flate.NewReader(bytes.NewReader(d))
	defer r.Close()
	out, _ := io.ReadAll(io.LimitReader(r, 1<<20))
	if len(out) < 64 {
		return nil, false
	}
	return out, true
}

func printablePct(b []byte) float64 {
	if len(b) == 0 {
		return 0
	}
	p := 0
	for _, c := range b {
		if (c >= 0x20 && c < 0x7f) || c == 0x0a || c == 0x0d || c == 0x09 {
			p++
		}
	}
	return float64(p) / float64(len(b))
}

func errbrief(e error) string {
	if e == nil {
		return "nil"
	}
	s := e.Error()
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

func samp(b []byte) string {
	if len(b) > 80 {
		b = b[:80]
	}
	return string(b)
}
