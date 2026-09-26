package diag

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/klauspost/compress/flate"
	"github.com/oisee/vibing-steampunk/pkg/sapcompress"
)

// The strict SAP-LZH reference decoder used during interoperability testing
// rejected a standard 32 KiB DEFLATE reference at distance 23259. Its largest
// supported distance was 16122, so keep the writer's search window within it.
const diagLZHWindow = 16122

// compressDIAGLZH wraps DIAG item bytes in SAP-LZH. The three noise bits and
// trailing byte reproduce the framing accepted by a live A4H dispatcher for
// a compressed frontend DataProvider reply. This encodes LZH (Compress=2),
// not the native GUI's LZC (Compress=1).
func compressDIAGLZH(plain []byte) ([]byte, error) {
	if len(plain) == 0 || uint64(len(plain)) > uint64(^uint32(0)) {
		return nil, errors.New("diag: SAP-LZH body length must be 1 through 2^32-1 bytes")
	}
	var deflated bytes.Buffer
	w, err := flate.NewWriterWindow(&deflated, diagLZHWindow)
	if err != nil {
		return nil, fmt.Errorf("diag: create SAP-LZH compressor: %w", err)
	}
	if _, err := w.Write(plain); err != nil {
		return nil, fmt.Errorf("diag: compress SAP-LZH body: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("diag: finish SAP-LZH body: %w", err)
	}
	stream := make([]byte, 8, 8+deflated.Len()+2)
	binary.LittleEndian.PutUint32(stream[:4], uint32(len(plain)))
	copy(stream[4:], []byte{0x12, 0x1f, 0x9d, 0x02})
	carry := byte(0x1f) // five low bits: 2-bit noise count plus three one-bits
	for _, b := range deflated.Bytes() {
		stream = append(stream, b<<5|carry)
		carry = b >> 3
	}
	stream = append(stream, carry, 0)
	decoded, err := sapcompress.Decompress(stream)
	if err != nil || !bytes.Equal(decoded, plain) {
		return nil, errors.New("diag: SAP-LZH body failed local decompression check")
	}
	return stream, nil
}
