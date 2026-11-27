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
		// Spanish captions are routine for this tool's users.
		{"taker:label", "Inyección SQL — línea 16"},
	}
	data, err := pngWithMeta(img, meta)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		t.Fatalf("metadata broke the PNG: %v", err)
	}

	got := pngTextChunks(t, data)
	for _, kv := range meta {
		if got[kv[0]] != kv[1] {
			t.Errorf("text chunk %q = %q, want %q", kv[0], got[kv[0]], kv[1])
		}
	}
	// tEXt is Latin-1 by definition: UTF-8 bytes there read back garbled in
	// every spec-following viewer (exiftool showed InyecciÃ³n). Non-ASCII has
	// to travel as iTXt, and ASCII stays tEXt so existing readers keep working.
	for _, c := range pngChunks(t, data) {
		ascii := true
		for i := 0; i < len(c.val); i++ {
			if c.val[i] >= 0x80 {
				ascii = false
			}
		}
		if c.typ == "tEXt" && !ascii {
			t.Errorf("tEXt %q carries non-Latin-1 UTF-8 bytes: %q", c.key, c.val)
		}
		if c.typ == "iTXt" && ascii {
			t.Errorf("ASCII value %q moved to iTXt; plain tEXt readers would miss it", c.key)
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

// pngChunk is one text chunk as a reader sees it: tEXt, or iTXt decoded.
type pngChunk struct{ typ, key, val string }

// pngChunks walks the real chunk structure of an encoded PNG and returns its
// text metadata — what a consumer holding only the detached image can read.
// Every chunk's CRC must check, not just ours: a corrupt chunk is worse than
// a missing one.
func pngChunks(t *testing.T, data []byte) []pngChunk {
	t.Helper()
	var out []pngChunk
	if len(data) < 8 {
		t.Fatalf("not a PNG: %d bytes", len(data))
	}
	off := 8 // signature
	for off+12 <= len(data) {
		length := int(binary.BigEndian.Uint32(data[off:]))
		ctype := string(data[off+4 : off+8])
		payload := data[off+8 : off+8+length]
		if ctype == "IEND" {
			break
		}
		want := crc32.ChecksumIEEE(data[off+4 : off+8+length])
		if binary.BigEndian.Uint32(data[off+8+length:]) != want {
			t.Fatalf("chunk %s has a bad CRC", ctype)
		}
		switch ctype {
		case "tEXt":
			key, val, _ := bytes.Cut(payload, []byte{0})
			out = append(out, pngChunk{ctype, string(key), string(val)})
		case "iTXt":
			// keyword 0 compression-flag method language 0 translated 0 text
			key, rest, _ := bytes.Cut(payload, []byte{0})
			if len(rest) < 2 || rest[0] != 0 {
				t.Fatalf("iTXt %q: want an uncompressed chunk", key)
			}
			_, rest, _ = bytes.Cut(rest[2:], []byte{0}) // language tag
			_, text, _ := bytes.Cut(rest, []byte{0})    // translated keyword
			out = append(out, pngChunk{ctype, string(key), string(text)})
		}
		off += 12 + length
	}
	return out
}

// pngTextChunks is pngChunks by keyword.
func pngTextChunks(t *testing.T, data []byte) map[string]string {
	t.Helper()
	got := map[string]string{}
	for _, c := range pngChunks(t, data) {
		got[c.key] = c.val
	}
	return got
}
