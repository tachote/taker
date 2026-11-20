package main

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"testing"
)

// The metadata has to survive the PNG: a consumer decodes the image the same
// way a reviewer does, so the test walks the real chunk structure.
func TestPNGMetaRoundTrips(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	meta := [][2]string{
		{"taker:version", "0.3.0"},
		{"taker:command", "taker a.go -o out.png"},
		{"taker:sha256", "abc123"},
	}
	data, err := pngWithMeta(img, meta)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		t.Fatalf("metadata broke the PNG: %v", err)
	}

	got := map[string]string{}
	off := 8 // signature
	for off < len(data) {
		length := int(binary.BigEndian.Uint32(data[off:]))
		ctype := string(data[off+4 : off+8])
		payload := data[off+8 : off+8+length]
		if ctype == "IEND" {
			break
		}
		// Every chunk's CRC must check, not just ours: a corrupt chunk is
		// worse than a missing one.
		want := crc32.ChecksumIEEE(data[off+4 : off+8+length])
		if binary.BigEndian.Uint32(data[off+8+length:]) != want {
			t.Fatalf("chunk %s has a bad CRC", ctype)
		}
		if ctype == "tEXt" {
			key, val, _ := bytes.Cut(payload, []byte{0})
			got[string(key)] = string(val)
		}
		off += 12 + length
	}
	for _, kv := range meta {
		if got[kv[0]] != kv[1] {
			t.Errorf("tEXt %q = %q, want %q", kv[0], got[kv[0]], kv[1])
		}
	}
}

func TestSHA256Hex(t *testing.T) {
	// The value is the published SHA-256 of the empty string — a wrong
	// implementation would not produce it.
	const empty = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got := sha256Hex(nil); got != empty {
		t.Errorf("sha256 of empty input = %s, want %s", got, empty)
	}
}
