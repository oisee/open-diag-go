package alv

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/oisee/vibing-steampunk/pkg/sapcompress"
)

// The ALV row blob (KNOWLEDGE.md §12) is a doubly-nested SAP-LZH container
// fragmented into RFC rows inside an APPL RFC_TR item:
//
//	RFC_TR value
//	  └─ outer SAP-LZH stream, split into ≤250-byte RFC rows
//	       └─ (decompressed) an OLE-automation "VARS" stream that carries
//	            └─ one or more nested SAP-LZH blobs (NOT row-chunked)
//	                 └─ (decompressed) the DataProvider R3TABLE:
//	                      • a field catalog (column names + widths)
//	                      • data packets of row-major fixed-width cells
//
// DecodeRows walks this all the way down; EncodeRows builds a byte-compatible
// container from scratch. The two are inverses at the level of decoded rows,
// which is what the round-trip test pins (the compressed bytes are free to
// differ — chunk boundaries and DEFLATE output are not canonical).

// LZH magic as it appears mid-stream: the algorithm byte 0x12 immediately
// followed by the 1F 9D signature. The 8-byte header begins 4 bytes earlier
// (the u32 length sits in front of the 0x12). Equivalently, KNOWLEDGE.md's
// "header starts 5 bytes before" the 1F 9D signature.
var lzhMagic = []byte{0x12, 0x1f, 0x9d}

// RFC-row chunk markers used to fragment the outer stream.
var (
	rowMarker = []byte{0x03, 0x05, 0x03, 0x05} // 03 05 03 05 <len u16 BE>, then <len> payload bytes
	endMarker = []byte{0x03, 0x05, 0x03, 0x06} // 03 05 03 06 00 00 ends a chunk stream
)

// rfcRowPayload is the payload size of a full RFC row; the wire uses 250
// (0x00FA). The last row of a stream may be shorter.
const rfcRowPayload = 250

// Catalog record geometry, read off a live 7.58 T100 ALV catalog. Each column
// is one fixed record; the values we need sit at fixed offsets inside it.
const (
	catalogRecordSize = 904 // one column descriptor
	catNameOffset     = 0   // 30-byte space-padded field name
	catNameLen        = 30
	catLenOffset      = 160 // internal field length, u32 LE
	catTypeOffset     = 164 // a type/flags word, u32 LE (low byte kept as Type)
)

// Data-packet framing. The wire marks a DataProvider packet with FF FF FF FF
// then a 1-byte packet kind (0x01 for the first). Our encoder writes its own
// self-describing packet behind that marker so DecodeRows can recover rows
// unambiguously; a captured packet whose framing does not match is read as
// carrying no rows (the row values page in on scroll and are absent here).
var dataMarker = []byte{0xff, 0xff, 0xff, 0xff, 0x01}

// dataHeaderLen is rowCount(u32 LE) + rowWidth(u32 LE) following dataMarker.
const dataHeaderLen = 8

// Column is one ALV column: its field name, a type/flags byte, and its
// fixed cell width in bytes.
type Column struct {
	Name string
	Type byte
	Len  int
}

// ErrNoCatalog is returned when no field catalog can be found in the blob.
var ErrNoCatalog = errors.New("alv: no R3TABLE field catalog found in blob")

// ExtractLZHStreams scans an RFC_TR value for SAP-LZH streams and returns each
// one de-chunked into a complete, self-contained compressed stream ready for
// sapcompress.Decompress: the 8-byte header followed by the concatenated RFC-row
// payloads with their 6-byte markers removed. One entry per 12 1F 9D found at a
// valid header position.
func ExtractLZHStreams(rfctrValue []byte) [][]byte {
	var streams [][]byte
	pos := 0
	for {
		j := bytes.Index(rfctrValue[pos:], lzhMagic)
		if j < 0 {
			break
		}
		hdr := pos + j - 4 // u32 length precedes the 0x12
		next := pos + j + len(lzhMagic)
		if hdr < 0 {
			pos = next
			continue
		}
		if _, err := sapcompress.ParseHeader(rfctrValue[hdr:]); err != nil {
			pos = next
			continue
		}
		streams = append(streams, dechunk(rfctrValue, hdr))
		pos = next
	}
	return streams
}

// dechunk reconstructs one compressed stream starting at the 8-byte header at
// start. It keeps the header verbatim, then walks the RFC rows: a 03 05 03 05
// marker is followed by a u16 big-endian length and exactly that many payload
// bytes (copied by length, never scanned, so marker bytes inside a payload are
// safe); a 03 05 03 06 terminator ends the stream. Any bytes before the first
// marker — the unmarked lead-in a live server leaves when the header does not
// fall on a row boundary — are copied through as-is.
func dechunk(b []byte, start int) []byte {
	out := make([]byte, 0, len(b)-start)
	if start+headerSize > len(b) {
		return out
	}
	out = append(out, b[start:start+headerSize]...)
	i := start + headerSize
	for i < len(b) {
		if i+6 <= len(b) && bytes.Equal(b[i:i+4], rowMarker) {
			n := int(binary.BigEndian.Uint16(b[i+4 : i+6]))
			i += 6
			if i+n > len(b) {
				n = len(b) - i
			}
			out = append(out, b[i:i+n]...)
			i += n
			continue
		}
		if i+4 <= len(b) && bytes.Equal(b[i:i+4], endMarker) {
			break
		}
		out = append(out, b[i])
		i++
	}
	return out
}

// chunkRFC is dechunk's inverse: it fragments a complete compressed stream into
// RFC rows. The 8-byte header is emitted verbatim, then the body is split into
// rfcRowPayload-byte pieces, each introduced by a 03 05 03 05 <len BE> marker
// (including the first, so dechunk has no unmarked lead-in to scan), and the
// stream is closed with 03 05 03 06 00 00.
func chunkRFC(stream []byte) []byte {
	out := make([]byte, 0, len(stream)+len(stream)/rfcRowPayload*6+16)
	if len(stream) < headerSize {
		return append(out, stream...)
	}
	out = append(out, stream[:headerSize]...)
	body := stream[headerSize:]
	for off := 0; off < len(body); off += rfcRowPayload {
		end := off + rfcRowPayload
		if end > len(body) {
			end = len(body)
		}
		piece := body[off:end]
		out = append(out, rowMarker...)
		out = binary.BigEndian.AppendUint16(out, uint16(len(piece)))
		out = append(out, piece...)
	}
	out = append(out, endMarker...)
	out = append(out, 0x00, 0x00)
	return out
}

// allBlobs returns every decompressed payload in an RFC_TR value: each outer
// stream, plus every nested SAP-LZH blob found inside a decompressed outer
// stream (one level of nesting, as the wire uses).
func allBlobs(rfctrValue []byte) [][]byte {
	var blobs [][]byte
	for _, stream := range ExtractLZHStreams(rfctrValue) {
		dec, err := sapcompress.Decompress(stream)
		if err != nil {
			continue
		}
		blobs = append(blobs, dec)
		pos := 0
		for {
			j := bytes.Index(dec[pos:], lzhMagic)
			if j < 0 {
				break
			}
			hdr := pos + j - 4
			next := pos + j + len(lzhMagic)
			if hdr < 0 {
				pos = next
				continue
			}
			if _, err := sapcompress.ParseHeader(dec[hdr:]); err != nil {
				pos = next
				continue
			}
			if inner, err := sapcompress.Decompress(dec[hdr:]); err == nil {
				blobs = append(blobs, inner)
			}
			pos = next
		}
	}
	return blobs
}

// DecodeRows decodes the ALV row blob in an RFC_TR value into its column
// catalog and rows. It descends both compression levels, reads the field
// catalog (column names and widths), and slices any data packet whose framing
// matches into row-major fixed-width cells.
//
// Note on live captures: a SAP.DataPOnDemand provider ships the catalog (and
// index/control tables) up front and pages the actual row values in on scroll.
// A frame that carries only the catalog therefore decodes to the correct
// columns with zero rows — which is faithful, not a failure.
func DecodeRows(rfctrValue []byte) (cols []Column, rows [][]string, err error) {
	blobs := allBlobs(rfctrValue)

	// Catalog: the blob that parses into the most valid column records.
	var best []Column
	for _, b := range blobs {
		if c := parseCatalog(b); len(c) > len(best) {
			best = c
		}
	}
	if len(best) == 0 {
		return nil, nil, ErrNoCatalog
	}
	cols = best

	rowWidth := 0
	for _, c := range cols {
		rowWidth += c.Len
	}

	// Rows: the first data packet whose declared row width matches the catalog.
	for _, b := range blobs {
		if r, ok := parseDataPacket(b, cols, rowWidth); ok {
			rows = r
			break
		}
	}
	return cols, rows, nil
}

// parseCatalog reads a field catalog from a blob: a whole number of
// catalogRecordSize records, each carrying a valid field name and a plausible
// width. It returns nil unless every record in the blob validates, which keeps
// non-catalog blobs (index tables, control metadata) from matching.
func parseCatalog(b []byte) []Column {
	if len(b) == 0 || len(b)%catalogRecordSize != 0 {
		return nil
	}
	n := len(b) / catalogRecordSize
	cols := make([]Column, 0, n)
	for r := 0; r < n; r++ {
		rec := b[r*catalogRecordSize:]
		name := trimName(rec[catNameOffset : catNameOffset+catNameLen])
		if !validFieldName(name) {
			return nil
		}
		width := int(binary.LittleEndian.Uint32(rec[catLenOffset : catLenOffset+4]))
		if width <= 0 || width > 0x7fff {
			return nil
		}
		typ := rec[catTypeOffset] // low byte of the type/flags word
		cols = append(cols, Column{Name: name, Type: typ, Len: width})
	}
	return cols
}

// parseDataPacket finds dataMarker in a blob and, if the row width recorded
// behind it matches the catalog, slices the payload into rows of fixed-width
// cells. A packet whose framing does not match (e.g. a captured catalog-record
// separator that shares the FF FF FF FF 01 marker) yields ok=false.
func parseDataPacket(b []byte, cols []Column, rowWidth int) ([][]string, bool) {
	idx := bytes.Index(b, dataMarker)
	if idx < 0 {
		return nil, false
	}
	p := idx + len(dataMarker)
	if p+dataHeaderLen > len(b) {
		return nil, false
	}
	rowCount := int(binary.LittleEndian.Uint32(b[p : p+4]))
	declWidth := int(binary.LittleEndian.Uint32(b[p+4 : p+8]))
	if declWidth != rowWidth || rowWidth == 0 {
		return nil, false
	}
	p += dataHeaderLen
	if rowCount < 0 || p+rowCount*rowWidth > len(b) {
		return nil, false
	}
	var rows [][]string
	for r := 0; r < rowCount; r++ {
		row := b[p+r*rowWidth : p+(r+1)*rowWidth]
		cells := make([]string, len(cols))
		off := 0
		for i, c := range cols {
			cells[i] = trimCell(row[off : off+c.Len])
			off += c.Len
		}
		rows = append(rows, cells)
	}
	return rows, true
}

// EncodeRows builds an RFC_TR-compatible ALV row blob from a catalog and rows.
// It serializes the field catalog and one data packet, compresses each as a
// nested SAP-LZH blob, concatenates them into an outer stream, compresses that,
// and fragments the result into RFC rows — the exact shape DecodeRows expects.
func EncodeRows(cols []Column, rows [][]string) ([]byte, error) {
	catalog := buildCatalog(cols)
	data, err := buildDataPacket(cols, rows)
	if err != nil {
		return nil, err
	}

	innerCat, err := Compress(catalog)
	if err != nil {
		return nil, err
	}
	innerData, err := Compress(data)
	if err != nil {
		return nil, err
	}

	// The outer "VARS" stream carries the nested blobs contiguously.
	outerPayload := make([]byte, 0, len(innerCat)+len(innerData))
	outerPayload = append(outerPayload, innerCat...)
	outerPayload = append(outerPayload, innerData...)

	outerStream, err := Compress(outerPayload)
	if err != nil {
		return nil, err
	}
	return chunkRFC(outerStream), nil
}

// buildCatalog serializes columns into fixed catalogRecordSize records that
// parseCatalog reads back. The field name is written at the three offsets a
// live catalog repeats it at; the width and type sit where parseCatalog looks.
func buildCatalog(cols []Column) []byte {
	buf := make([]byte, len(cols)*catalogRecordSize)
	for r, c := range cols {
		rec := buf[r*catalogRecordSize : (r+1)*catalogRecordSize]
		for i := range rec {
			rec[i] = 0x20 // space-pad, as the wire does
		}
		writeName(rec[0:catNameLen], c.Name)     // primary name
		writeName(rec[40:40+catNameLen], c.Name) // repeated (matches wire)
		writeName(rec[176:176+catNameLen], c.Name)
		// The two descriptor words are binary, so clear their span first.
		for i := catLenOffset; i < catLenOffset+8; i++ {
			rec[i] = 0
		}
		binary.LittleEndian.PutUint32(rec[catLenOffset:], uint32(c.Len))
		binary.LittleEndian.PutUint32(rec[catTypeOffset:], uint32(c.Type))
	}
	return buf
}

// buildDataPacket serializes rows into a self-describing FF FF FF FF 01 packet:
// rowCount, rowWidth, then row-major fixed-width space-padded cells.
func buildDataPacket(cols []Column, rows [][]string) ([]byte, error) {
	rowWidth := 0
	for _, c := range cols {
		rowWidth += c.Len
	}
	buf := make([]byte, 0, len(dataMarker)+dataHeaderLen+len(rows)*rowWidth)
	buf = append(buf, dataMarker...)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(rows)))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(rowWidth))
	for _, row := range rows {
		if len(row) != len(cols) {
			return nil, fmt.Errorf("alv: row has %d cells, catalog has %d columns", len(row), len(cols))
		}
		for i, c := range cols {
			cell := []byte(row[i])
			if len(cell) > c.Len {
				return nil, fmt.Errorf("alv: value %q does not fit column %s(%d)", row[i], c.Name, c.Len)
			}
			padded := make([]byte, c.Len)
			copy(padded, cell)
			for j := len(cell); j < c.Len; j++ {
				padded[j] = 0x20
			}
			buf = append(buf, padded...)
		}
	}
	return buf, nil
}

// --- small helpers ---

func trimName(b []byte) string {
	return string(bytes.TrimRight(b, " \x00"))
}

// trimCell strips the trailing space/NUL padding of a fixed-width cell.
func trimCell(b []byte) string {
	return string(bytes.TrimRight(b, " \x00"))
}

func writeName(dst []byte, name string) {
	for i := range dst {
		dst[i] = 0x20
	}
	copy(dst, name)
}

// validFieldName holds an ABAP-ish column name: non-empty, opening with a
// letter, and otherwise letters, digits, '_' or '/'.
func validFieldName(s string) bool {
	if s == "" || len(s) > catNameLen {
		return false
	}
	first := s[0]
	if !((first >= 'A' && first <= 'Z') || (first >= 'a' && first <= 'z')) {
		return false // must open with a letter
	}
	for i, c := range []byte(s) {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z':
		case c == '_' || c == '/':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}
