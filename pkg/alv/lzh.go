// Package alv is a pure-Go codec for the SAP ALV grid data blob that rides in
// an APPL RFC_TR item of a DIAG frame (see KNOWLEDGE.md §12). It both decodes
// the doubly-compressed, RFC-row-chunked container into table rows and encodes
// rows back into a byte-compatible container, so an ALV grid can be synthesized
// without a live SAP GUI.
//
// This file is the SAP-LZH *writer* — the exact inverse of the decoder in the
// vsp module's pkg/sapcompress (github.com/oisee/vibing-steampunk/pkg/sapcompress),
// which offers only Decompress/ParseHeader. sapcompress calls DEFLATE-with-a-
// SAP-wrapper "LZH": an eight-byte header, then a two-bit count of noise bits,
// that many noise bits, then a raw RFC-1951 DEFLATE stream read LSB-first. The
// decoder strips the prefix by right-shifting the whole body by that many bits
// and hands the result to compress/flate. Compress performs the mirror image:
// it produces a raw DEFLATE stream with compress/flate and left-shifts it by a
// fixed two-bit prefix (value 0), so Decompress recovers the stream exactly.
//
// The correctness gate this exists to satisfy: for any x,
//
//	sapcompress.Decompress(Compress(x)) == x
package alv

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
)

// SAP-LZH header layout (8 bytes), matching sapcompress.ParseHeader:
//
//	[0:4] uncompressed length, u32 little-endian
//	[4]   algorithm/version byte: low nibble 2 = LZH, high nibble 1 = version → 0x12
//	[5:7] magic 1F 9D
//	[7]   "extra" byte, unused by LZH
const (
	headerSize = 8
	// algLZH is the algorithm/version byte for LZH: version 1, algorithm 2.
	algLZH = 0x12
	// compressPrefixBits is the number of noise bits we prepend to the DEFLATE
	// stream. The decoder reads the count from the low two bits of the first
	// body byte as 2 + (body[0] & 3); we always emit the minimum, 2, so those
	// low two bits are 0. Anything in 2..5 would decode; 2 keeps the shift small.
	compressPrefixBits = 2
)

var lzhSig = []byte{0x1f, 0x9d}

// Compress encodes data as a complete SAP-LZH stream (header included) that
// sapcompress.Decompress decodes back to exactly data. It is the precise
// inverse of that decoder's inflate step.
//
// The body is a raw DEFLATE stream shifted left by two bits so that the two
// low bits of the first body byte are zero: the decoder reads those as a noise
// prefix of 2 + 0 = 2 bits and right-shifts the body back by two, recovering
// the DEFLATE stream byte-for-byte. We use compress/flate at default settings;
// SAP's own encoder never emits DEFLATE stored blocks, but since our shift is
// undone exactly before inflation, any valid DEFLATE the standard library
// produces round-trips. Ratio is irrelevant here — only that the SAP decoder
// (and any real GUI, pending a live test) accepts the stream.
func Compress(data []byte) ([]byte, error) {
	// 1. Raw DEFLATE. compress/flate writes a headerless RFC-1951 stream, which
	//    is exactly what sits behind the SAP prefix.
	var deflated bytes.Buffer
	w, err := flate.NewWriter(&deflated, flate.DefaultCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	def := deflated.Bytes()

	// 2. Eight-byte SAP header.
	out := make([]byte, headerSize, headerSize+len(def)+1)
	binary.LittleEndian.PutUint32(out[0:4], uint32(len(data)))
	out[4] = algLZH
	out[5] = lzhSig[0]
	out[6] = lzhSig[1]
	out[7] = 0x00 // extra, unused by LZH

	// 3. Body: def shifted left by compressPrefixBits. The low two bits of the
	//    first body byte stay 0, so the decoder's prefix reads back as exactly
	//    2. Each output byte carries this byte's high bits shifted up and the
	//    previous byte's top bits carried in.
	const p = compressPrefixBits
	var carry byte
	for i := 0; i < len(def); i++ {
		out = append(out, (def[i]<<p)|carry)
		carry = def[i] >> (8 - p)
	}
	// The two bits shifted off the final byte need a home, or the DEFLATE
	// end-of-stream marker in that byte would be lost.
	out = append(out, carry)
	return out, nil
}
