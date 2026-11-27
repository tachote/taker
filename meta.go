package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
)

// sha256Hex is the fingerprint an evidence set is checked against: the report
// carries it and the PNG embeds it, so a reviewer can tie the image to the
// exact bytes that were rendered.
func sha256Hex(b []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

// pngWithMeta encodes img as PNG and inserts text metadata chunks before IEND.
// Provenance travels inside the image: a PNG detached from the run — the usual
// case once it is pasted into a report — still states which command, revision
// and input hashes produced it.
func pngWithMeta(img image.Image, meta [][2]string) ([]byte, error) {
	var buf bytes.Buffer
	// BestSpeed skips the per-row adaptive filter search and the slow deflate
	// levels. Evidence is mostly flat panel colour, so the file grows ~4% while
	// encoding — the largest cost of a run once rendering is fast — drops ~30%.
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	raw := buf.Bytes()
	if len(meta) == 0 {
		return raw, nil
	}
	// IEND is the mandatory last chunk: 4-byte length (0) + "IEND" + CRC.
	iend := len(raw) - 12
	if iend < 8 || string(raw[iend+4:iend+8]) != "IEND" {
		return raw, nil
	}
	var out bytes.Buffer
	out.Grow(len(raw) + len(meta)*80)
	out.Write(raw[:iend])
	for _, kv := range meta {
		out.Write(textChunk(kv[0], kv[1]))
	}
	out.Write(raw[iend:])
	return out.Bytes(), nil
}

// textChunk builds the chunk for one value. tEXt is ISO-8859-1 by definition,
// so UTF-8 written there reads back garbled — a Spanish caption came out as
// InyecciÃ³n in exiftool. A value with any non-ASCII byte goes out as iTXt,
// whose text is UTF-8; pure ASCII stays tEXt, which every reader understands.
func textChunk(key, val string) []byte {
	ctype := "tEXt"
	payload := make([]byte, 0, len(key)+5+len(val))
	payload = append(payload, key...)
	payload = append(payload, 0)
	if !isASCII(val) {
		ctype = "iTXt"
		// Uncompressed (flag 0, method 0), no language tag, no translated
		// keyword: each empty field is its zero terminator.
		payload = append(payload, 0, 0, 0, 0)
	}
	payload = append(payload, val...)

	var out bytes.Buffer
	out.Grow(12 + len(payload))
	_ = binary.Write(&out, binary.BigEndian, uint32(len(payload)))
	out.WriteString(ctype)
	out.Write(payload)

	crc := crc32.NewIEEE()
	_, _ = crc.Write([]byte(ctype))
	_, _ = crc.Write(payload)
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], crc.Sum32())
	out.Write(sum[:])
	return out.Bytes()
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
