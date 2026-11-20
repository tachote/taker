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

// pngWithMeta encodes img as PNG and inserts tEXt metadata chunks before IEND.
// Provenance travels inside the image: a PNG detached from the run — the usual
// case once it is pasted into a report — still states which command, revision
// and input hashes produced it.
func pngWithMeta(img image.Image, meta [][2]string) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
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

// textChunk builds a PNG tEXt chunk: keyword, a zero byte, then the value.
func textChunk(key, val string) []byte {
	payload := make([]byte, 0, len(key)+1+len(val))
	payload = append(payload, key...)
	payload = append(payload, 0)
	payload = append(payload, val...)

	var out bytes.Buffer
	out.Grow(12 + len(payload))
	_ = binary.Write(&out, binary.BigEndian, uint32(len(payload)))
	out.WriteString("tEXt")
	out.Write(payload)

	crc := crc32.NewIEEE()
	_, _ = crc.Write([]byte("tEXt"))
	_, _ = crc.Write(payload)
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], crc.Sum32())
	out.Write(sum[:])
	return out.Bytes()
}
