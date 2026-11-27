package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/tachote/taker/highlight"
)

// writeFiles puts each name/body pair in dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A line spec is clamped to the input before it is expanded. "1-999999999" is
// a fine way to say "to the end" of a short file, and without the input's
// length it was either a gigabyte of line numbers or a refusal of a spec that
// names only lines the file has.
func TestLineSpecsClampToTheInput(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"a.py":   "one\ntwo\nthree\n",
		"r.http": "GET / HTTP/1.1\r\nHost: x\r\n\r\n",
		"s.http": "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n\r\nhello\n",
	})
	for _, tc := range []struct {
		name string
		args []string
		want func(t *testing.T, rep Report)
	}{
		{"snap --lines", []string{"a.py", "--lines", "2-999999999"}, func(t *testing.T, rep Report) {
			if rep.Crop == nil || rep.Crop.First != 2 || rep.Crop.Last != 3 {
				t.Errorf("crop = %+v, want lines 2-3", rep.Crop)
			}
		}},
		{"snap --highlight", []string{"a.py", "-H", "2-999999999"}, func(t *testing.T, rep Report) {
			if rep.Crop == nil || rep.Crop.Shown != 3 {
				t.Errorf("crop = %+v, want the whole 3-line file", rep.Crop)
			}
		}},
		{"snap --first-line", []string{"a.py", "--first-line", "70", "--lines", "71-999999999"}, func(t *testing.T, rep Report) {
			if rep.Crop == nil || rep.Crop.First != 71 || rep.Crop.Last != 72 {
				t.Errorf("crop = %+v, want lines 71-72", rep.Crop)
			}
		}},
		{"http --nr --ns", []string{"http", "-r", "r.http", "-s", "s.http", "--nr", "1-999999999", "--ns", "1-999999999"}, func(t *testing.T, rep Report) {
			if rep.Panels == nil || rep.Panels.Request == nil || rep.Panels.Response == nil ||
				rep.Panels.Request.Last != 3 || rep.Panels.Response.Last != 4 {
				t.Errorf("panels = %+v, want both messages whole", rep.Panels)
			}
		}},
		{"http --hr --hs", []string{"http", "-r", "r.http", "-s", "s.http", "--hr", "2-999999999", "--hs", "4-999999999"}, func(t *testing.T, rep Report) {
			if rep.Panels == nil || rep.Panels.Request == nil || rep.Panels.Request.Last != 3 {
				t.Errorf("panels = %+v, want the request whole", rep.Panels)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append(slices.Clone(tc.args), "-o", filepath.Join(t.TempDir(), "o.png"), "--json")
			code, stdout, stderr := runTaker(t, dir, args...)
			if code != exitOK {
				t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
			}
			var rep Report
			if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
				t.Fatalf("not a json report: %v\n%s", err, stdout)
			}
			tc.want(t, rep)
		})
	}
}

// decodePNG reads the pixels of a PNG the CLI wrote.
func decodePNG(t *testing.T, path string) *image.NRGBA {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	out := image.NewNRGBA(img.Bounds())
	draw.Draw(out, out.Bounds(), img, img.Bounds().Min, draw.Src)
	return out
}

// Overlapping highlight spans on one value are one box. Drawn as given they
// nest, and the inner border splits one value into what reads as two.
func TestOverlappingMarksDrawOneBox(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"n.py":   "secret = \"sk_live_abcdef\"\nother line\n",
		"n.http": "POST /x HTTP/1.1\nHost: h\n\ntoken=sk_live_abcdef&x=1\n",
	})
	for _, tc := range []struct {
		name          string
		overlap, want []string
	}{
		{"snap -H and -m", []string{"n.py", "-H", "1:10-16,1:13-25", "-m", "sk_live"}, []string{"n.py", "-H", "1:10-25"}},
		{"http --hr and -m", []string{"http", "-r", "n.http", "--hr", "4:7-13,4:10-20", "-m", "sk_live"}, []string{"http", "-r", "n.http", "--hr", "4:7-20"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var imgs []*image.NRGBA
			for i, args := range [][]string{tc.overlap, tc.want} {
				out := filepath.Join(t.TempDir(), fmt.Sprintf("%d.png", i))
				if code, stdout, stderr := runTaker(t, dir, append(slices.Clone(args), "-o", out)...); code != exitOK {
					t.Fatalf("%v: exit = %d\n%s\n%s", args, code, stdout, stderr)
				}
				imgs = append(imgs, decodePNG(t, out))
			}
			if imgs[0].Bounds() != imgs[1].Bounds() || !bytes.Equal(imgs[0].Pix, imgs[1].Pix) {
				t.Errorf("overlapping spans drew a different image from their union")
			}
		})
	}
}

// Two -m needles on one transcript value are one span, as in snap and http.
func TestTermMarksAreUnioned(t *testing.T) {
	lines, err := highlight.Tokenize("$ env\nKEY=sk_live_abcdef\n", "text", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	m := termMarks(lines, []string{"sk_live", "live_abc"}, nil, 0, newReporter("", "term"))
	if want := []highlight.Span{{Start: 5, End: 15}}; !slices.Equal(m[2], want) {
		t.Errorf("line 2 spans = %+v, want %+v", m[2], want)
	}
}
