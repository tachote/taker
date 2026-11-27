package render

import (
	"image/color"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/tachote/taker/highlight"
)

// Non-contiguous highlights must produce one box each, not a single box
// swallowing every line in between.
func TestMergeRuns(t *testing.T) {
	cases := []struct {
		name string
		in   []markRun
		want []markRun
	}{
		{"empty", nil, nil},
		{"single", []markRun{{0, 99, 0, 10}}, []markRun{{0, 99, 0, 10}}},
		{"adjacent", []markRun{{0, 99, 0, 10}, {0, 99, 10, 20}}, []markRun{{0, 99, 0, 20}}},
		{"split", []markRun{{0, 99, 0, 10}, {0, 99, 40, 50}}, []markRun{{0, 99, 0, 10}, {0, 99, 40, 50}}},
		{
			"two runs",
			[]markRun{{0, 99, 0, 10}, {0, 99, 10, 20}, {0, 99, 60, 70}, {0, 99, 70, 80}},
			[]markRun{{0, 99, 0, 20}, {0, 99, 60, 80}},
		},
		{
			// A narrow column mark must not be stretched to a neighbour's width.
			"adjacent but different columns",
			[]markRun{{10, 40, 0, 10}, {10, 90, 10, 20}},
			[]markRun{{10, 40, 0, 10}, {10, 90, 10, 20}},
		},
		{
			// Two marks per row arrive interleaved; each column pair is its own
			// run, or the rows get doubled borders between them.
			"two marks per row",
			[]markRun{{10, 40, 0, 10}, {60, 90, 0, 10}, {10, 40, 10, 20}, {60, 90, 10, 20}},
			[]markRun{{10, 40, 0, 20}, {60, 90, 0, 20}},
		},
		{
			"interleaved run resumes after a gap",
			[]markRun{{10, 40, 0, 10}, {60, 90, 0, 10}, {10, 40, 30, 40}},
			[]markRun{{10, 40, 0, 10}, {60, 90, 0, 10}, {10, 40, 30, 40}},
		},
	}
	for _, c := range cases {
		if got := mergeRuns(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: mergeRuns(%v) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}

func TestSplitTokensAt(t *testing.T) {
	rowText := func(rows [][]highlight.Token) []string {
		out := make([]string, len(rows))
		for i, r := range rows {
			for _, tok := range r {
				out[i] += tok.Text
			}
		}
		return out
	}

	got := rowText(splitTokensAt([]highlight.Token{{Text: "abcdefg"}}, 3))
	if want := []string{"abc", "def", "g"}; !reflect.DeepEqual(got, want) {
		t.Errorf("single token: %v, want %v", got, want)
	}

	// Splits must fall on column boundaries across token seams, not per token.
	got = rowText(splitTokensAt([]highlight.Token{{Text: "ab"}, {Text: "cd"}, {Text: "ef"}}, 3))
	if want := []string{"abc", "def"}; !reflect.DeepEqual(got, want) {
		t.Errorf("multi token: %v, want %v", got, want)
	}

	// A blank line still needs one row, otherwise it vanishes from the render.
	if rows := splitTokensAt(nil, 3); len(rows) != 1 {
		t.Errorf("blank line produced %d rows, want 1", len(rows))
	}

	// Colors survive the split.
	rows := splitTokensAt([]highlight.Token{{Text: "xy", R: 1, G: 2, B: 3}}, 1)
	for _, r := range rows {
		for _, tok := range r {
			if tok.R != 1 || tok.G != 2 || tok.B != 3 {
				t.Errorf("color lost in split: %+v", tok)
			}
		}
	}
}

func TestGapLabel(t *testing.T) {
	cases := map[int]string{0: "···", 1: "··· 1 line skipped", 12: "··· 12 lines skipped"}
	for in, want := range cases {
		if got := gapLabel(in); got != want {
			t.Errorf("gapLabel(%d) = %q, want %q", in, got, want)
		}
	}
}

// Every theme needs a chrome background, or the margin renders pure black.
// The named aliases must be the same objects as the map entries, or they miss
// the init pass that fills ChromeBg in.
func TestThemesHaveChromeBg(t *testing.T) {
	for name, th := range Themes {
		if th.UI.ChromeBg.A == 0 {
			t.Errorf("theme %q has no ChromeBg", name)
		}
		if th.UI.ChromeBg == th.UI.Bg {
			t.Errorf("theme %q ChromeBg equals Bg; window edge will be invisible", name)
		}
	}
	if DefaultTheme != Themes["vscode-dark"] || BurpTheme != Themes["burp"] {
		t.Error("a theme alias points at a different object than the map entry")
	}
	if BurpTheme.UI.ChromeBg.A == 0 || DefaultTheme.UI.ChromeBg.A == 0 {
		t.Error("an alias was initialised before init() filled ChromeBg")
	}
}

// End-to-end smoke test: a render must not panic and must include the chrome
// margin on both axes.
func TestRenderSmoke(t *testing.T) {
	lines, err := highlight.Tokenize("package main\n\nfunc main() {}\n", "go", "x.go", DefaultTheme.Chroma)
	if err != nil {
		t.Fatal(err)
	}
	img, err := Render(&Config{
		Lines:          lines,
		Title:          "x.go",
		HighlightLines: highlight.Marks{1: {{}}, 3: {{}}},
		Theme:          DefaultTheme,
	})
	if err != nil {
		t.Fatal(err)
	}
	if w := img.Bounds().Dx(); w <= 2*chromeMargin {
		t.Errorf("image width %d does not exceed the chrome margin", w)
	}
	// Top-left pixel is outside the rounded corner: it must be chrome, not code.
	if got := img.NRGBAAt(0, 0); got != DefaultTheme.UI.ChromeBg {
		t.Errorf("corner pixel = %v, want ChromeBg %v", got, DefaultTheme.UI.ChromeBg)
	}
}

func TestWrapWords(t *testing.T) {
	m, err := loadMetrics()
	if err != nil {
		t.Fatal(err)
	}
	w := m.charWidth

	// Prose wraps at spaces; a caption is read, not inspected character by
	// character, so a word must never be cut in half.
	got := wrapWords(m.face, "alpha beta gamma delta", 11*w)
	for _, line := range got {
		if textWidth(m.face, line) > 11*w {
			t.Errorf("line %q exceeds the width", line)
		}
	}
	if len(got) < 2 {
		t.Errorf("expected a wrap, got %v", got)
	}
	if strings.Join(got, " ") != "alpha beta gamma delta" {
		t.Errorf("words lost or reordered: %v", got)
	}

	if got := wrapWords(m.face, "", 100); got != nil {
		t.Errorf("empty text should be nil, got %v", got)
	}
	// A word wider than the line is broken, not clipped: a URL or a path in a
	// caption is evidence, and drawing it past the image edge loses its tail.
	long := strings.Repeat("x", 200)
	got = wrapWords(m.face, "see "+long+" here", 30*w)
	for _, line := range got {
		if textWidth(m.face, line) > 30*w {
			t.Errorf("line %q exceeds the width", line)
		}
	}
	if joined := strings.Join(got, ""); !strings.Contains(joined, long) {
		t.Errorf("the long word lost characters: %v", got)
	}
	if got[0] != "see" || got[len(got)-1] != "here" && !strings.HasSuffix(got[len(got)-1], " here") {
		t.Errorf("the words around the long one moved: %v", got)
	}
	// Narrower than one character: still one rune per line, never a hang.
	if got := wrapWords(m.face, "abc", 1); strings.Join(got, "") != "abc" {
		t.Errorf("sub-character width: %v", got)
	}
}

// The footnote carries the revision the code was read from; a path cut at the
// image edge is a provenance claim nobody can check. Notes break between one
// another first, so "lines 1-3 of 10" stays one phrase, and a note wider than
// the strip is broken inside rather than clipped.
func TestFootnoteKeepsLongSourceRefVisible(t *testing.T) {
	m, err := loadMetrics()
	if err != nil {
		t.Fatal(err)
	}
	ref := "services/authentication/internal/handlers/login_handler_v2.py @ 9f344fad66cf"
	text := joinNotes(ref, "lines 1-3 of 10", "1 region redacted")
	const sw = 600
	lines, w, _ := footnoteStrip.layout(m, text, sw)
	if w != sw {
		t.Errorf("the footnote widened the image to %d", w)
	}
	for _, line := range lines {
		if lw := stripTextX + textWidth(m.face, line) + captionMinPad; lw > sw {
			t.Errorf("line %q is %dpx, past the %dpx image", line, lw, sw)
		}
	}
	all := strings.Join(lines, "")
	if !strings.Contains(all, "login_handler_v2.py") {
		t.Errorf("the file name was lost: %q", lines)
	}
	found := false
	for _, line := range lines {
		if strings.Contains(line, "lines 1-3 of 10") {
			found = true
		}
	}
	if !found {
		t.Errorf("the crop note was split across lines: %q", lines)
	}

	// A footnote that fits is one line, unchanged.
	if lines, _, _ := footnoteStrip.layout(m, joinNotes("a.py @ 1234", "lines 1-3 of 10"), 2000); len(lines) != 1 || lines[0] != "a.py @ 1234 \u00b7 lines 1-3 of 10" {
		t.Errorf("a fitting footnote changed: %q", lines)
	}
}

func TestMarkBoxes(t *testing.T) {
	m, err := loadMetrics()
	if err != nil {
		t.Fatal(err)
	}
	l := &layout{metrics: m, codeX: 100, contentX: 60, highlightR: 900}
	row := func(idx int, text string) visualLine {
		return visualLine{lineNum: 1, rowIndex: idx, isContinuation: idx > 0,
			tokens: []highlight.Token{{Text: text}}}
	}
	cw := m.charWidth

	// A whole-line mark is the full-width band, starting just inside the pane's
	// left edge so the wash covers the line number instead of half-covering it.
	got := l.markBoxes(highlight.Marks{1: {{}}}, 1, row(0, "abcdef"), 0)
	if len(got) != 1 || got[0].x1 != wholeLineInsetL || got[0].x2 != l.highlightR || !got[0].wholeLine {
		t.Errorf("whole line: got %+v", got)
	}

	// A column mark covers exactly its cells, plus breathing room.
	got = l.markBoxes(highlight.Marks{1: {{Start: 2, End: 4}}}, 1, row(0, "abcdef"), 0)
	if len(got) != 1 || got[0].x1 != 100+1*cw-markPadX || got[0].x2 != 100+4*cw+markPadX {
		t.Errorf("column span: got %+v", got)
	}

	// Marking past the end of the text would float a box in blank space.
	got = l.markBoxes(highlight.Marks{1: {{Start: 2, End: 99}}}, 1, row(0, "abcdef"), 0)
	if len(got) != 1 || got[0].x2 != 100+6*cw+markPadX {
		t.Errorf("clamp to text: got %+v", got)
	}

	// Wrapped: columns 8-12 of a 5-column-wide wrap land on rows 1 and 2.
	first := l.markBoxes(highlight.Marks{1: {{Start: 8, End: 12}}}, 1, row(1, "fghij"), 5)
	if len(first) != 1 || first[0].x1 != 100+2*cw-markPadX {
		t.Errorf("wrapped row 1: got %+v", first)
	}
	second := l.markBoxes(highlight.Marks{1: {{Start: 8, End: 12}}}, 1, row(2, "klmno"), 5)
	if len(second) != 1 || second[0].x2 != 100+2*cw+markPadX {
		t.Errorf("wrapped row 2: got %+v", second)
	}
	// A row the span does not reach gets nothing.
	if got := l.markBoxes(highlight.Marks{1: {{Start: 8, End: 12}}}, 1, row(0, "abcde"), 5); got != nil {
		t.Errorf("row outside the span: got %+v", got)
	}

	if got := l.markBoxes(nil, 1, row(0, "abc"), 0); got != nil {
		t.Errorf("no spans: got %+v", got)
	}
	if got := l.markBoxes(highlight.Marks{1: {{}}}, 1, visualLine{isGap: true}, 0); got != nil {
		t.Errorf("gap row: got %+v", got)
	}
}

// A line can be marked without naming a region. Marks.Has() calls it marked, so
// something has to be drawn — otherwise the "nothing highlighted" guard stays
// quiet while the image ships unmarked, which is the failure this tool exists
// to prevent. Absent and marked-with-no-spans must not look alike.
func TestMarkBoxesDistinguishesAbsentFromEmpty(t *testing.T) {
	m, err := loadMetrics()
	if err != nil {
		t.Fatal(err)
	}
	l := &layout{metrics: m, codeX: 100, contentX: 60, highlightR: 900}
	vl := visualLine{lineNum: 1, tokens: []highlight.Token{{Text: "abc"}}}

	marked := highlight.Marks{1: {}}
	if !marked.Has(1) {
		t.Fatal("Has should report a present line as marked")
	}
	got := l.markBoxes(marked, 1, vl, 0)
	if len(got) != 1 || got[0].x1 != wholeLineInsetL || got[0].x2 != l.highlightR || !got[0].wholeLine {
		t.Errorf("marked with no spans should draw the full band, got %+v", got)
	}

	// A line that is simply absent draws nothing.
	if got := l.markBoxes(highlight.Marks{2: {{}}}, 1, vl, 0); got != nil {
		t.Errorf("absent line should draw nothing, got %+v", got)
	}
}

// A wrap width wider than the canvas used to crop the right edge in silence.
// It must clamp the columns instead, so every character still renders.
func TestWrapWidthWiderThanCanvasKeepsContent(t *testing.T) {
	const cols = 300
	src := strings.Repeat("x", cols) + "\n"
	lines, err := highlight.Tokenize(src, "text", "", DefaultTheme.Chroma)
	if err != nil {
		t.Fatal(err)
	}
	img, err := Render(&Config{
		Lines: lines, Title: "t", Theme: DefaultTheme,
		Wrap: true, WrapWidth: cols, noWarn: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() > maxImageWidth+2*chromeMargin {
		t.Errorf("canvas %d exceeds the cap", img.Bounds().Dx())
	}

	// Every character has to survive: rows × wrap width must cover the line.
	fit := MaxWrapCols(false)
	if fit >= cols {
		t.Skip("canvas is wide enough on this platform; nothing to clamp")
	}
	wantRows := (cols + fit - 1) / fit
	vlines := buildVisualLines(lines, fit)
	total := 0
	for _, vl := range vlines {
		total += vl.runeLen()
	}
	if total != cols {
		t.Errorf("wrapped rows hold %d characters, want %d", total, cols)
	}
	if len(vlines) != wantRows {
		t.Errorf("got %d rows, want %d", len(vlines), wantRows)
	}
}

// The unmarked-image guard has to judge what was drawn. Checking only that the
// line numbers exist lets "7:900-999" — a real line, no real columns — through
// silently, which is the one outcome this warning exists to catch.
func TestWarnsWhenNothingWasActuallyMarked(t *testing.T) {
	lines, err := highlight.Tokenize("short\nalso short\n", "text", "", DefaultTheme.Chroma)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		marks    highlight.Marks
		wantWarn bool
	}{
		{"line out of range", highlight.Marks{99: {{}}}, true},
		{"columns past the end of a real line", highlight.Marks{1: {{Start: 900, End: 999}}}, true},
		{"valid column span", highlight.Marks{1: {{Start: 1, End: 3}}}, false},
		{"whole line", highlight.Marks{1: {{}}}, false},
	}
	for _, c := range cases {
		warned := captureStderr(t, func() {
			if _, err := Render(&Config{
				Lines: lines, Title: "t", Theme: DefaultTheme, HighlightLines: c.marks,
			}); err != nil {
				t.Fatal(err)
			}
		})
		got := strings.Contains(warned, "nothing was marked")
		if got != c.wantWarn {
			t.Errorf("%s: warned=%v want %v (stderr: %q)", c.name, got, c.wantWarn, warned)
		}
	}
}

// captureStderr runs fn with os.Stderr redirected and returns what it wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		io.Copy(&b, r)
		done <- b.String()
	}()
	fn()
	w.Close()
	os.Stderr = orig
	out := <-done
	r.Close()
	return out
}

// A path longer than the tab used to run off the canvas edge mid-word, which
// reads as a broken render. The tail must survive: the file name is what
// identifies the evidence.
func TestEllipsizeLeft(t *testing.T) {
	m, err := loadMetrics()
	if err != nil {
		t.Fatal(err)
	}
	cw := m.charWidth

	if got := ellipsizeLeft(m.face, "short.go", 100*cw); got != "short.go" {
		t.Errorf("a fitting title must be untouched, got %q", got)
	}

	long := strings.Repeat("very/deep/", 20) + "file.go"
	got := ellipsizeLeft(m.face, long, 30*cw)
	if textWidth(m.face, got) > 30*cw {
		t.Errorf("%q still exceeds the width", got)
	}
	if !strings.HasPrefix(got, "…") {
		t.Errorf("truncation should be marked, got %q", got)
	}
	if !strings.HasSuffix(got, "file.go") {
		t.Errorf("the file name must survive, got %q", got)
	}

	if got := ellipsizeLeft(m.face, long, 0); got != long {
		t.Errorf("a non-positive width should be a no-op, got %q", got)
	}
}

// Wrapping cannot make an over-long title fit, so a long title must not be
// reported as content overflowing — nor silently rewrap short code.
func TestLongTitleDoesNotTriggerContentWrap(t *testing.T) {
	lines, err := highlight.Tokenize("x = 1\n", "text", "", DefaultTheme.Chroma)
	if err != nil {
		t.Fatal(err)
	}
	warned := captureStderr(t, func() {
		if _, err := Render(&Config{
			Lines: lines,
			Title: strings.Repeat("very/deep/", 30) + "file.go",
			Theme: DefaultTheme,
		}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(warned, "content exceeds") {
		t.Errorf("a long title blamed the content: %q", warned)
	}
}

// A trimmed view that does not say it was trimmed reads as the whole message:
// the releaser cannot tell a 9-line response from the first 9 lines of a
// 3000-line one. These are the two claims the note is allowed to make.
func TestCropNote(t *testing.T) {
	shown := func(nums ...int) []highlight.LineTokens {
		out := make([]highlight.LineTokens, 0, len(nums))
		for _, n := range nums {
			out = append(out, highlight.LineTokens{LineNum: n, Tokens: []highlight.Token{{Text: "x"}}})
		}
		return out
	}
	withGap := func(nums ...int) []highlight.LineTokens {
		out := shown(nums...)
		return append([]highlight.LineTokens{{IsGap: true, Skipped: 40}}, out...)
	}

	cases := []struct {
		name  string
		shown []highlight.LineTokens
		total int
		want  string
	}{
		{"nothing left out", shown(1, 2, 3), 3, ""},
		{"contiguous window", shown(4, 5, 6), 87, "lines 4-6 of 87"},
		{"split window counts", shown(1, 50), 300, "2 of 300 lines"},
		// A gap row is a band saying "40 lines skipped", not a line: it must not
		// be counted as content, in either direction.
		{"gap rows do not inflate the count", withGap(1, 50), 300, "2 of 300 lines"},
		{"contiguous window with a gap", withGap(1, 2), 300, "lines 1-2 of 300"},
		{"labelled", shown(9), 40, "lines 9-9 of 40"},
		{"an excerpt at line 1 claims nothing", shown(1), 0, ""},
		// An excerpt from the middle of a file: the numbers are real, and the
		// reader is told the rest of the file is not in the image.
		{"excerpt from the middle", shown(59, 60, 61), 0, "lines 59-61 of the excerpt"},
	}
	for _, c := range cases {
		if got := cropNote("", c.shown, c.total); got != c.want {
			t.Errorf("%s: cropNote = %q, want %q", c.name, got, c.want)
		}
	}
	if got := cropNote("Response", shown(2, 3, 4), 9); got != "Response: lines 2-4 of 9" {
		t.Errorf("labelled note = %q", got)
	}
	if got := joinNotes("", "Response: lines 2-4 of 9", ""); got != "Response: lines 2-4 of 9" {
		t.Errorf("joinNotes = %q", got)
	}
	if got := joinNotes("Request: 2 of 9 lines", "Response: lines 2-4 of 9"); !strings.Contains(got, "Request") || !strings.Contains(got, "Response") {
		t.Errorf("joinNotes dropped a pane: %q", got)
	}
}

// The note has to reach the image, not just the function: a caption-only render
// and a cropped-and-captioned render must not be the same height.
func TestRenderFootnoteReachesTheImage(t *testing.T) {
	all, err := highlight.Tokenize("a=1\nb=2\nc=3\nd=4\ne=5\n", "http", "x.http", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Same visible lines, different truth about the total: that isolates the
	// footnote instead of measuring the crop itself.
	same := all[1:3]
	lying := &Config{Lines: same, Title: "t", TotalLines: 2}
	honest := &Config{Lines: same, Title: "t", TotalLines: len(all)}

	a, err := Render(lying)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Render(honest)
	if err != nil {
		t.Fatal(err)
	}
	if b.Bounds().Dy() <= a.Bounds().Dy() {
		t.Errorf("the note added no height: %d vs %d", b.Bounds().Dy(), a.Bounds().Dy())
	}
	if b.Bounds().Dx() != a.Bounds().Dx() {
		t.Errorf("the note changed the width: %d vs %d", b.Bounds().Dx(), a.Bounds().Dx())
	}
}

// A mark that falls outside the view disappears without a word, and the image
// then shows one box while the analyst's note says two lines are highlighted —
// or, worse, while the tool itself has just reported "2 matches". The count is
// computed over the whole input, so the view has to account for the difference.
func TestWarnsWhenOnlySomeMarksAreInView(t *testing.T) {
	all, err := highlight.Tokenize("a=1\nSECRET=2\nSECRET=3\nd=4\n", "text", "", DefaultTheme.Chroma)
	if err != nil {
		t.Fatal(err)
	}
	view := append([]highlight.LineTokens(nil), all[0:2]...) // lines 1-2 only

	warned := captureStderr(t, func() {
		if _, err := Render(&Config{
			Lines: view, Title: "t", Theme: DefaultTheme,
			HighlightLines: highlight.Marks{1: {{}}, 3: {{}}, 4: {{}}},
		}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(warned, "marked lines 3-4 are not in") {
		t.Errorf("missing report of the marks left out; stderr: %q", warned)
	}
	// One mark is drawn, so the older "nothing was marked" message must not fire
	// too: two warnings for one cause reads as two problems.
	if warnings := strings.Count(warned, "taker: warning:"); warnings != 1 {
		t.Errorf("got %d warnings, want 1: %q", warnings, warned)
	}

	// Nothing in view: the original message, and only that one.
	warned = captureStderr(t, func() {
		if _, err := Render(&Config{
			Lines: view, Title: "t", Theme: DefaultTheme, HighlightLines: highlight.Marks{3: {{}}},
		}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(warned, "nothing was marked") || strings.Contains(warned, "marked line 3 is") {
		t.Errorf("stderr: %q", warned)
	}
}

// The window chrome adds a title band above the tab bar. The only check that
// matters is the band's presence and its dots: an empty strip of chrome is a
// regression the eye misses but the pixels do not.
func TestWindowChromeAddsTitleBand(t *testing.T) {
	lines, err := highlight.Tokenize("x = 1\n", "text", "", DefaultTheme.Chroma)
	if err != nil {
		t.Fatal(err)
	}
	bare, err := Render(&Config{Lines: lines, Title: "t", Theme: DefaultTheme, noWarn: true})
	if err != nil {
		t.Fatal(err)
	}
	win, err := Render(&Config{Lines: lines, Title: "t", Theme: DefaultTheme, WindowChrome: true, noWarn: true})
	if err != nil {
		t.Fatal(err)
	}
	if win.Bounds().Dy() != bare.Bounds().Dy()+windowBarH {
		t.Errorf("window chrome added %dpx, want %d", win.Bounds().Dy()-bare.Bounds().Dy(), windowBarH)
	}
	// The first traffic-light dot must be drawn inside the band.
	want := rgb(0xff, 0x5f, 0x57)
	got := win.NRGBAAt(chromeMargin+windowDotInset+windowDotR, chromeMargin+windowBarH/2)
	if got != want {
		t.Errorf("first dot = %v, want %v", got, want)
	}
}

// The shadow lives in the chrome margin: the band below the window must hold
// pixels darker than the canvas.
func TestShadowDarkensTheMargin(t *testing.T) {
	lines, err := highlight.Tokenize("x = 1\n", "text", "", DefaultTheme.Chroma)
	if err != nil {
		t.Fatal(err)
	}
	img, err := Render(&Config{Lines: lines, Title: "t", Theme: DefaultTheme, Shadow: true, noWarn: true})
	if err != nil {
		t.Fatal(err)
	}
	ui := DefaultTheme.UI
	w := img.Bounds().Dx()
	y := img.Bounds().Dy() - chromeMargin + shadowOffsetY + shadowSpread/2
	found := false
	for x := chromeMargin; x < w-chromeMargin; x++ {
		p := img.NRGBAAt(x, y)
		if p != ui.ChromeBg {
			found = true
			break
		}
	}
	if !found {
		t.Error("no shadow pixels below the window")
	}
}

// A mark border's corner pixel must be background, not border colour: the
// stroke now rounds with the window instead of ending in a square point.
func TestMarkBorderRoundsItsCorners(t *testing.T) {
	src := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n"
	lines, err := highlight.Tokenize(src, "text", "", DefaultTheme.Chroma)
	if err != nil {
		t.Fatal(err)
	}
	img, err := Render(&Config{
		Lines: lines, Title: "t", Theme: DefaultTheme,
		HighlightLines: highlight.Marks{1: {{Start: 2, End: 10}}},
		noWarn:         true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ui := DefaultTheme.UI
	// The box's top-left corner is (min x, min y) over all border pixels. A
	// square stroke paints that pixel fully; a rounded one leaves it a blend
	// or the mark's own wash.
	x1, y1 := -1, -1
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			if img.NRGBAAt(x, y) == ui.HighlightBorder {
				if y1 == -1 || y < y1 {
					y1 = y
				}
				if x1 == -1 || x < x1 {
					x1 = x
				}
			}
		}
	}
	if x1 == -1 {
		t.Fatal("no mark border found")
	}
	if img.NRGBAAt(x1, y1) == ui.HighlightBorder {
		t.Error("corner pixel is full border colour: the stroke is still square")
	}
}

// The light theme must be visibly light — the one property a regression can
// silently flip is the editor background.
func TestGitHubLightIsActuallyLight(t *testing.T) {
	lines, err := highlight.Tokenize("x = 1\n", "text", "", Themes["github-light"].Chroma)
	if err != nil {
		t.Fatal(err)
	}
	img, err := Render(&Config{Lines: lines, Title: "t", Theme: Themes["github-light"], noWarn: true})
	if err != nil {
		t.Fatal(err)
	}
	bg := img.NRGBAAt(img.Bounds().Dx()/2, chromeMargin+tabBarHeight+10)
	if bg.R < 200 || bg.G < 200 || bg.B < 200 {
		t.Errorf("github-light editor bg = %v, want near-white", bg)
	}
}

func TestCompressRanges(t *testing.T) {
	cases := []struct {
		in   []int
		want string
	}{
		{[]int{4}, "4"},
		{[]int{4, 9, 10, 11}, "4, 9-11"},
		{[]int{1, 2, 3, 5, 7, 8}, "1-3, 5, 7-8"},
	}
	for _, c := range cases {
		if got := compressRanges(c.in); got != c.want {
			t.Errorf("compressRanges(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --redact hides the text under a solid box — black on every theme, counted
// on the Config so the report and the footnote agree with the image.
func TestRedactDrawsBlackBoxes(t *testing.T) {
	lines, err := highlight.Tokenize("key = \"hunter2\"\nplain = 1\n", "text", "", DefaultTheme.Chroma)
	if err != nil {
		t.Fatal(err)
	}
	redact, _ := highlight.FindMarks([][]highlight.LineTokens{lines}, []string{"hunter2"}, 0)
	cfg := &Config{Lines: lines, Title: "t", Theme: DefaultTheme, RedactRegions: redact[0], noWarn: true}
	img, err := Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Redacted != 1 {
		t.Fatalf("Redacted = %d, want 1", cfg.Redacted)
	}
	// vscode-dark's background is #1e1e1e, never pure black — a pure-black run
	// can only be the redaction box.
	black := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := img.NRGBAAt(x, y)
			if c.R == 0 && c.G == 0 && c.B == 0 && c.A == 0xff {
				black++
			}
		}
	}
	if black < 50 {
		t.Errorf("only %d pure-black pixels; the redaction box is not drawn", black)
	}
}

// --redact-keep leaves the first characters visible: the box shrinks to the
// rest, and a span shorter than the keep draws nothing at all.
func TestRedactKeepLeavesPrefixVisible(t *testing.T) {
	lines, err := highlight.Tokenize("key = \"hunter2\"\n", "text", "", DefaultTheme.Chroma)
	if err != nil {
		t.Fatal(err)
	}
	redact, _ := highlight.FindMarks([][]highlight.LineTokens{lines}, []string{"hunter2"}, 0)
	cfg := &Config{Lines: lines, Title: "t", Theme: DefaultTheme,
		RedactRegions: highlight.KeepSpans(redact[0], 4), noWarn: true}
	if _, err := Render(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Redacted != 1 {
		t.Fatalf("Redacted = %d, want 1 (the suffix stays hidden)", cfg.Redacted)
	}

	cfg2 := &Config{Lines: lines, Title: "t", Theme: DefaultTheme,
		RedactRegions: highlight.KeepSpans(redact[0], 7), noWarn: true}
	if _, err := Render(cfg2); err != nil {
		t.Fatal(err)
	}
	if cfg2.Redacted != 0 {
		t.Errorf("Redacted = %d, want 0 when the keep covers the whole secret", cfg2.Redacted)
	}
}

// Terminal mode has no source lines to number: the gutter disappears and the
// image is narrower than the same content rendered as code.
func TestTerminalDropsTheGutter(t *testing.T) {
	lines, err := highlight.Tokenize("$ id\nuid=0(root)\n", "text", "", DefaultTheme.Chroma)
	if err != nil {
		t.Fatal(err)
	}
	code, err := Render(&Config{Lines: lines, Title: "t", Theme: DefaultTheme, noWarn: true})
	if err != nil {
		t.Fatal(err)
	}
	term, err := Render(&Config{Lines: lines, Title: "t", Theme: DefaultTheme, Terminal: true, noWarn: true})
	if err != nil {
		t.Fatal(err)
	}
	if term.Bounds().Dx() >= code.Bounds().Dx() {
		t.Errorf("terminal width %d >= code width %d; the gutter was not dropped",
			term.Bounds().Dx(), code.Bounds().Dx())
	}
}

// A snap draws its footnote and caption in place on one canvas, while the HTTP
// path appends them with withFootnote and withCaption. The two must produce the
// same pixels, or the same note looks different depending on the command.
func TestInPlaceStripsMatchTheWrappers(t *testing.T) {
	lines, err := highlight.Tokenize("a = 1\nb = 2\nc = 3\n", "python", "x.py", DefaultTheme.Chroma)
	if err != nil {
		t.Fatal(err)
	}
	m, err := metricsFor()
	if err != nil {
		t.Fatal(err)
	}
	for _, caption := range []string{"short", strings.Repeat("a long caption that has to wrap ", 6)} {
		base := Config{Lines: lines, Title: "x.py", Theme: DefaultTheme, WindowChrome: true, Shadow: true, noWarn: true}
		bare, err := Render(&base)
		if err != nil {
			t.Fatal(err)
		}
		const ref = "x.py @ 9f344fad66cf"
		full := base
		full.SourceRef, full.Caption = ref, caption
		got, err := Render(&full)
		if err != nil {
			t.Fatal(err)
		}
		want := withCaption(withFootnote(bare, ref, DefaultTheme.UI, m), caption, DefaultTheme.UI, m)
		if got.Bounds() != want.Bounds() {
			t.Fatalf("bounds %v, want %v", got.Bounds(), want.Bounds())
		}
		if !reflect.DeepEqual(got.Pix, want.Pix) {
			t.Errorf("caption %.10q: in-place strips differ from the wrapper composition", caption)
		}
	}
}

// With --shadow the window's rounded corners must sit on the shadow, not on
// the bare canvas: masking them back to the flat ChromeBg left a light notch
// at each corner, sharpest on the light theme.
func TestShadowRunsUnderTheRoundedCorners(t *testing.T) {
	lines, err := highlight.Tokenize("x = 1\n", "text", "", DefaultTheme.Chroma)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"vscode-dark", "github-light"} {
		th := Themes[name]
		img, err := Render(&Config{Lines: lines, Title: "t", Theme: th, Shadow: true, noWarn: true})
		if err != nil {
			t.Fatal(err)
		}
		// The bottom-left corner pixel of the window lies outside the arc.
		x, y := chromeMargin, img.Bounds().Dy()-chromeMargin-1
		corner := img.NRGBAAt(x, y)
		beside := img.NRGBAAt(x-1, y) // the margin just left of it, shadowed
		if corner == th.UI.ChromeBg {
			t.Errorf("%s: corner pixel is flat ChromeBg %v: the shadow stops at the corner", name, corner)
		}
		if lum(corner) > lum(beside)+0.02 {
			t.Errorf("%s: corner %v is lighter than the shadow beside it %v", name, corner, beside)
		}
	}
}

func lum(c color.NRGBA) float64 { return (float64(c.R) + float64(c.G) + float64(c.B)) / (3 * 255) }

// Plain identifiers and whole terminal transcripts take the style's Text
// colour; a style that does not set one falls back to a dark-theme grey in
// the highlighter. On a light background that grey is near-invisible, so
// every code theme must set a Text colour that reads on its own background.
func TestThemesSetReadableTextColour(t *testing.T) {
	for name, th := range Themes {
		if th.HTTPOnly {
			continue
		}
		e := th.Chroma.Get(chroma.Text)
		if !e.Colour.IsSet() {
			t.Errorf("%s: the chroma style sets no Text colour", name)
			continue
		}
		fg := rgb(e.Colour.Red(), e.Colour.Green(), e.Colour.Blue())
		if c := contrast(fg, th.UI.Bg); c < 4.5 {
			t.Errorf("%s: Text %v on Bg %v has contrast %.2f, want >= 4.5", name, fg, th.UI.Bg, c)
		}
	}
}

// The same property end to end: a transcript rendered on github-light must
// put dark ink on the page, not pale grey.
func TestGitHubLightTranscriptIsReadable(t *testing.T) {
	th := Themes["github-light"]
	lines, err := highlight.Tokenize("$ id\nuid=1000(user)\n", "text", "", th.Chroma)
	if err != nil {
		t.Fatal(err)
	}
	for _, lt := range lines {
		for _, tok := range lt.Tokens {
			if c := contrast(rgb(tok.R, tok.G, tok.B), th.UI.Bg); c < 4.5 {
				t.Errorf("token %q colour #%02x%02x%02x has contrast %.2f on white", tok.Text, tok.R, tok.G, tok.B, c)
			}
		}
	}
}
