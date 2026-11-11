package render

import (
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

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
	// A word longer than the line still has to come out whole.
	long := strings.Repeat("x", 40)
	if got := wrapWords(m.face, long, 5*w); len(got) != 1 || got[0] != long {
		t.Errorf("an over-long word must not be split: %v", got)
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

	// A whole-line mark is the full-bleed band, starting at the pane's left edge
	// so the wash covers the line number instead of half-covering it.
	got := l.markBoxes(highlight.Marks{1: {{}}}, 1, row(0, "abcdef"), 0)
	if len(got) != 1 || got[0].x1 != 0 || got[0].x2 != l.highlightR || !got[0].wholeLine {
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
	if len(got) != 1 || got[0].x1 != 0 || got[0].x2 != l.highlightR || !got[0].wholeLine {
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
