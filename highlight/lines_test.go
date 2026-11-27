package highlight

import (
	"reflect"
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/styles"
)

func TestParseLineSpec(t *testing.T) {
	G := GapSentinel
	cases := []struct {
		in   string
		want []int
	}{
		{"", nil},
		{"5", []int{5}},
		{"3,1,2", []int{1, 2, 3}},
		{"1,1,1", []int{1}},
		{"10-12", []int{10, 11, 12}},
		{"1,5", []int{1, G, 5}},
		{"1,2,5,6", []int{1, 2, G, 5, 6}},
		{"1-2,4-5,9", []int{1, 2, G, 4, 5, G, 9}},
		{"7-7", []int{7}},
	}
	for _, c := range cases {
		got, err := ParseLineSpec(c.in)
		if err != nil {
			t.Errorf("ParseLineSpec(%q) error: %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseLineSpec(%q) = %v, want %v", c.in, got, c.want)
		}
	}

	for _, bad := range []string{"abc", "5-3", "1-", "-4", "2-x"} {
		if _, err := ParseLineSpec(bad); err == nil {
			t.Errorf("ParseLineSpec(%q) should have failed", bad)
		}
	}
}

func TestBuildLineSpec(t *testing.T) {
	G := GapSentinel
	got := BuildLineSpec([]int{9, 3, 3, 4, 0, -2, 10})
	want := []int{3, 4, G, 9, 10}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildLineSpec = %v, want %v", got, want)
	}
	if BuildLineSpec(nil) != nil {
		t.Error("BuildLineSpec(nil) should be nil")
	}
}

func TestParseHighlightSpec(t *testing.T) {
	whole := []Span{{}}
	cases := []struct {
		in   string
		want Marks
	}{
		{"2,7-8", Marks{2: whole, 7: whole, 8: whole}},
		{"13", Marks{13: whole}},
		// Column spans are what makes evidence point at the token, not the line.
		{"7:34-72", Marks{7: {{Start: 34, End: 72}}}},
		{"7:34", Marks{7: {{Start: 34, End: 34}}}},
		{"5,7:34-72", Marks{5: whole, 7: {{Start: 34, End: 72}}}},
		{"7:1-5,7:20-25", Marks{7: {{Start: 1, End: 5}, {Start: 20, End: 25}}}},
	}
	for _, c := range cases {
		got, err := ParseHighlightSpec(c.in)
		if err != nil {
			t.Errorf("ParseHighlightSpec(%q): %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseHighlightSpec(%q) = %v, want %v", c.in, got, c.want)
		}
	}

	for _, bad := range []string{"7:0-5", "7:9-2", "7:x", "0:1-2", "7:", "x:1"} {
		if got, err := ParseHighlightSpec(bad); err == nil {
			t.Errorf("ParseHighlightSpec(%q) should have failed, got %v", bad, got)
		}
	}

	if got, _ := ParseHighlightSpec(""); got != nil {
		t.Error("empty spec should yield nil")
	}

	m := Marks{9: whole, 2: whole, 30: whole}
	if got := m.Lines(); !reflect.DeepEqual(got, []int{2, 9, 30}) {
		t.Errorf("Lines() = %v, want sorted", got)
	}
	if !m.Has(9) || m.Has(10) {
		t.Error("Has is wrong")
	}
}

// A trailing newline closes the last line; it must not open a phantom one.
func TestTokenizeTrailingNewline(t *testing.T) {
	cases := []struct {
		src  string
		want int
	}{
		{"a\nb\n", 2},
		{"a\nb", 2},
		{"a\n", 1},
		{"a", 1},
		{"a\n\n", 2},
		{"a\n\n\n", 3},
		{"", 1},
	}
	for _, c := range cases {
		lines, err := Tokenize(c.src, "text", "", nil)
		if err != nil {
			t.Fatalf("Tokenize(%q): %v", c.src, err)
		}
		if len(lines) != c.want {
			t.Errorf("Tokenize(%q) = %d lines, want %d", c.src, len(lines), c.want)
		}
	}
}

// A CRLF capture is what a real HTTP message looks like on disk. The \r is not
// content: drawn it is a blank glyph, counted it is a phantom column.
func TestTokenizeStripsCR(t *testing.T) {
	lines, err := Tokenize("GET /x HTTP/1.1\r\nHost: a.test\r\n\r\na=1\r\n", "text", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, lt := range lines {
		if strings.Contains(lt.Text(), "\r") {
			t.Errorf("line %d still carries a carriage return: %q", i+1, lt.Text())
		}
	}
	if got := lines[1].Text(); got != "Host: a.test" {
		t.Errorf("line 2 = %q, want the header text without the CR", got)
	}
}

func TestFilterLinesSkipCount(t *testing.T) {
	all := make([]LineTokens, 0, 30)
	for i := 1; i <= 30; i++ {
		all = append(all, LineTokens{LineNum: i})
	}

	spec, err := ParseLineSpec("1-2,10,25-26")
	if err != nil {
		t.Fatal(err)
	}
	got := FilterLines(all, spec)

	var gaps []int
	for _, lt := range got {
		if lt.IsGap {
			gaps = append(gaps, lt.Skipped)
		}
	}
	// 3..9 hidden (7 lines), then 11..24 hidden (14 lines).
	if want := []int{7, 14}; !reflect.DeepEqual(gaps, want) {
		t.Errorf("gap skip counts = %v, want %v", gaps, want)
	}
	if len(got) != 5+len(gaps) {
		t.Errorf("got %d rows, want %d", len(got), 5+len(gaps))
	}
}

// Gaps come from the lines that exist, not from the requested spec: an
// out-of-range request must not invent a gap that leads nowhere.
func TestFilterLinesOutOfRange(t *testing.T) {
	all := []LineTokens{{LineNum: 1}, {LineNum: 2}, {LineNum: 3}}

	spec, err := ParseLineSpec("3,900")
	if err != nil {
		t.Fatal(err)
	}
	got := FilterLines(all, spec)
	if len(got) != 1 || got[0].LineNum != 3 {
		t.Fatalf("got %+v, want just line 3", got)
	}

	// Nothing in range at all.
	spec, _ = ParseLineSpec("500-600")
	if got := FilterLines(all, spec); len(got) != 0 {
		t.Errorf("got %+v, want no rows", got)
	}

	// A gap only between two lines that both exist.
	spec, _ = ParseLineSpec("1,3,900")
	got = FilterLines(all, spec)
	if len(got) != 3 || !got[1].IsGap || got[1].Skipped != 1 {
		t.Errorf("got %+v, want line 1, gap(1), line 3", got)
	}
}

// Pasting the token you found beats counting columns, so the search has to be
// exact: every occurrence, in runes, with no overlap.
func TestFindLiteral(t *testing.T) {
	lines := []LineTokens{
		{LineNum: 1, Tokens: []Token{{Text: "grant_type=x&secret="}, {Text: "sk_live_AAA"}}},
		{LineNum: 2, Tokens: []Token{{Text: "no match here"}}},
		{LineNum: 3, IsGap: true},
		{LineNum: 4, Tokens: []Token{{Text: "aXa aXa"}}},
	}

	got := FindLiteral(lines, "sk_live_AAA")
	want := Marks{1: {{Start: 21, End: 31}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	// Both occurrences, and they must not overlap.
	got = FindLiteral(lines, "aXa")
	want = Marks{4: {{Start: 1, End: 3}, {Start: 5, End: 7}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("repeated literal: got %v, want %v", got, want)
	}

	// A match spanning a token boundary still resolves against the whole line.
	if got := FindLiteral(lines, "secret=sk_live"); len(got[1]) != 1 {
		t.Errorf("cross-token match: got %v", got)
	}

	if got := FindLiteral(lines, "absent"); got != nil {
		t.Errorf("no match should be nil, got %v", got)
	}
	if got := FindLiteral(lines, ""); got != nil {
		t.Errorf("empty needle should be nil, got %v", got)
	}

	// Columns are rune positions: a multi-byte prefix must not shift them.
	uni := []LineTokens{{LineNum: 1, Tokens: []Token{{Text: "señor=TOKEN"}}}}
	if got := FindLiteral(uni, "TOKEN"); !reflect.DeepEqual(got, Marks{1: {{Start: 7, End: 11}}}) {
		t.Errorf("rune columns: got %v", got)
	}
}

func TestMarksMerge(t *testing.T) {
	a := Marks{1: {{}}, 2: {{Start: 5, End: 9}}}
	b := Marks{2: {{Start: 20, End: 25}}, 3: {{}}}

	got := a.Merge(b)
	want := Marks{1: {{}}, 2: {{Start: 5, End: 9}, {Start: 20, End: 25}}, 3: {{}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	// Neither input may change. RenderHTTP merges literal matches into the
	// caller's highlight map, and a render that edits the config it was handed
	// makes a second render with the same config produce a different image.
	if !reflect.DeepEqual(a, Marks{1: {{}}, 2: {{Start: 5, End: 9}}}) {
		t.Errorf("receiver was modified: %v", a)
	}
	if !reflect.DeepEqual(b, Marks{2: {{Start: 20, End: 25}}, 3: {{}}}) {
		t.Errorf("argument was modified: %v", b)
	}

	// The copied span slices must not alias the originals either.
	got[2] = append(got[2], Span{Start: 99, End: 100})
	if len(a[2]) != 1 {
		t.Errorf("appending to the result reached back into the receiver: %v", a[2])
	}

	var nilMarks Marks
	if got := nilMarks.Merge(Marks{7: {{}}}); !reflect.DeepEqual(got, Marks{7: {{}}}) {
		t.Errorf("merging into nil: got %v", got)
	}
	if got := a.Merge(nil); !reflect.DeepEqual(got, a) {
		t.Errorf("merging nil should return the receiver unchanged: got %v", got)
	}
}

func TestContextSpec(t *testing.T) {
	G := GapSentinel
	m := Marks{10: {{}}, 50: {{Start: 3, End: 8}}}
	got := ContextSpec(m, 2)
	want := []int{8, 9, 10, 11, 12, G, 48, 49, 50, 51, 52}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	// Clamped at the top of the file, never below line 1.
	if got := ContextSpec(Marks{2: {{}}}, 5); got[0] != 1 {
		t.Errorf("context near the start should begin at line 1, got %v", got)
	}
	// Nothing to center on, or no context asked for.
	if got := ContextSpec(nil, 3); got != nil {
		t.Errorf("no marks should give no spec, got %v", got)
	}
	if got := ContextSpec(m, 0); got != nil {
		t.Errorf("no context should give no spec, got %v", got)
	}
}

// A gutter is read as ground truth. Numbering an excerpt from 1 puts a line
// number in the image that is not the file's, which is the one kind of error
// evidence must not make.
func TestShiftLines(t *testing.T) {
	lines := []LineTokens{
		{LineNum: 1, Tokens: []Token{{Text: "a"}}},
		{LineNum: 2, Tokens: []Token{{Text: "b"}}},
		{IsGap: true, Skipped: 40},
		{LineNum: 3, Tokens: []Token{{Text: "c"}}},
	}

	got := ShiftLines(lines, 500)
	want := []int{500, 501, 0, 502}
	for i, w := range want {
		if w == 0 {
			if !got[i].IsGap {
				t.Errorf("line %d: gap sentinel was renumbered into a line", i)
			}
			continue
		}
		if got[i].LineNum != w {
			t.Errorf("line %d = %d, want %d", i, got[i].LineNum, w)
		}
	}
	// The input must not be touched: the caller may still need the original.
	if lines[0].LineNum != 1 {
		t.Errorf("input was modified: first line is now %d", lines[0].LineNum)
	}
	// 1 is the default and has to be a no-op.
	if got := ShiftLines(lines, 1); got[0].LineNum != 1 {
		t.Errorf("first-line 1 shifted the input: %d", got[0].LineNum)
	}
}

// A light style that sets no text colour (chroma's "github") used to fall back
// to the dark-theme default, #d4d4d4: plain-text evidence drawn light grey on
// white, barely legible. The fallback follows the background instead.
func TestTokenizeTextFallbackFollowsBackground(t *testing.T) {
	gh := styles.Get("github")
	if gh.Get(chroma.Text).Colour.IsSet() {
		t.Skip("chroma's github style now sets a text colour; the fallback is not exercised")
	}
	lines, err := Tokenize("user=admin password=hunter2\n", "text", "", gh)
	if err != nil {
		t.Fatal(err)
	}
	tok := lines[0].Tokens[0]
	if luma := (299*int(tok.R) + 587*int(tok.G) + 114*int(tok.B)) / 1000; luma > 96 {
		t.Errorf("plain text on github = #%02x%02x%02x, want a dark colour on its white background", tok.R, tok.G, tok.B)
	}

	// A dark style keeps its own text colour.
	lines, _ = Tokenize("x\n", "text", "", styles.Get("monokai"))
	if tok := lines[0].Tokens[0]; tok.R < 0xc0 {
		t.Errorf("plain text on monokai = #%02x%02x%02x, want its light text colour", tok.R, tok.G, tok.B)
	}

	// A style whose text colour really is black is set, not missing.
	black := chroma.MustNewStyle("black-on-white", chroma.StyleEntries{
		chroma.Text:       "#000000",
		chroma.Background: "bg:#ffffff",
	})
	lines, _ = Tokenize("x\n", "text", "", black)
	if tok := lines[0].Tokens[0]; tok.R|tok.G|tok.B != 0 {
		t.Errorf("black text style drew #%02x%02x%02x, want #000000", tok.R, tok.G, tok.B)
	}
}

// Every range expands to one entry per line, so a typo such as "1-300000000"
// used gigabytes before anything could refuse it. With the length known the
// range is clamped first; without it a range too wide to expand is refused.
func TestLineSpecClampsToFile(t *testing.T) {
	G := GapSentinel
	cases := []struct {
		in      string
		maxLine int
		want    []int
	}{
		{"1-300000000", 3, []int{1, 2, 3}},
		{"2,1-99999999", 5, []int{1, 2, 3, 4, 5}},
		{"1,3,900", 3, []int{1, G, 3, G, 900}},
		// Wholly past the end keeps its first line: an empty spec would mean
		// "every line" and render the file the analyst tried to narrow.
		{"500-600", 3, []int{500}},
	}
	for _, c := range cases {
		got, err := ParseLineSpecMax(c.in, c.maxLine)
		if err != nil {
			t.Errorf("ParseLineSpecMax(%q, %d): %v", c.in, c.maxLine, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseLineSpecMax(%q, %d) = %v, want %v", c.in, c.maxLine, got, c.want)
		}
	}
	if _, err := ParseLineSpec("1-300000000"); err == nil || !strings.Contains(err.Error(), "spans more than") {
		t.Errorf("unclamped huge range: err = %v, want a refusal", err)
	}
	if _, err := ParseLineSpec("1-1000000"); err != nil {
		t.Errorf("a range of exactly MaxRangeLines should pass: %v", err)
	}
	if _, err := ParseHighlightSpec("2-10000000"); err == nil {
		t.Error("ParseHighlightSpec should refuse an unclamped huge range")
	}

	// The clamped-off part of a highlight stays as one line past the end, so
	// the out-of-view warning still names a mark the analyst asked for.
	hl, err := ParseHighlightSpecMax("2-10000000,7:3-4", 3)
	if err != nil {
		t.Fatal(err)
	}
	whole := []Span{{}}
	if want := (Marks{2: whole, 3: whole, 4: whole, 7: {{Start: 3, End: 4}}}); !reflect.DeepEqual(hl, want) {
		t.Errorf("ParseHighlightSpecMax = %v, want %v", hl, want)
	}
	if hl, _ := ParseHighlightSpecMax("5-9", 3); !reflect.DeepEqual(hl, Marks{5: whole}) {
		t.Errorf("range wholly past the end = %v, want just line 5", hl)
	}

	// Context windows clamp too; a mark past the end keeps its own number.
	if got := ContextSpecMax(Marks{2: whole}, 10000000, 3); !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Errorf("ContextSpecMax huge context = %v, want [1 2 3]", got)
	}
	if got := ContextSpecMax(Marks{10: whole}, 2, 3); !reflect.DeepEqual(got, []int{10}) {
		t.Errorf("ContextSpecMax mark past the end = %v, want [10]", got)
	}
	// Overlapping windows are emitted once.
	if got := ContextSpec(Marks{10: whole, 11: whole}, 2); !reflect.DeepEqual(got, []int{8, 9, 10, 11, 12, 13}) {
		t.Errorf("overlapping windows = %v", got)
	}
}

// A line-spec error has to say what shape was expected, not echo strconv; a
// trailing comma is accepted in --lines as it is in --highlight; and line 0
// is refused by both parsers alike.
func TestLineSpecErrors(t *testing.T) {
	if got, err := ParseLineSpec("1,3,"); err != nil || !reflect.DeepEqual(got, []int{1, GapSentinel, 3}) {
		t.Errorf("trailing comma: got %v, %v", got, err)
	}
	cases := []struct {
		in, want string
		hl       bool
	}{
		{"2:", "columns go in --highlight", false},
		{"x", "expected N or N-M", false},
		{"0-5", "line numbers start at 1", false},
		{"0", "line numbers start at 1", false},
		{"2-3:4", "a column range needs a single line", true},
		{"x:1", "expected N:C or N:C-D", true},
		{"7:x", "expected N:C or N:C-D", true},
		{"0-5", "line numbers start at 1", true},
	}
	for _, c := range cases {
		var err error
		if c.hl {
			_, err = ParseHighlightSpec(c.in)
		} else {
			_, err = ParseLineSpec(c.in)
		}
		if err == nil {
			t.Errorf("%q (highlight=%v) should fail", c.in, c.hl)
			continue
		}
		if !strings.Contains(err.Error(), c.want) || strings.Contains(err.Error(), "strconv") {
			t.Errorf("%q (highlight=%v): err = %q, want it to say %q and not mention strconv", c.in, c.hl, err, c.want)
		}
	}
}

// Matches after multi-byte runes and back to back: the column count is carried
// from one match to the next, so a byte offset leaking into it would show here.
func TestFindLiteralMultibyte(t *testing.T) {
	lines := []LineTokens{{LineNum: 1, Tokens: []Token{{Text: "ñ=ab → abab ✓ab"}}}}
	got := FindLiteral(lines, "ab")
	want := Marks{1: {{Start: 3, End: 4}, {Start: 8, End: 9}, {Start: 10, End: 11}, {Start: 14, End: 15}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	// A multi-byte needle counts its own length in runes too.
	if got := FindLiteral(lines, "→ a"); !reflect.DeepEqual(got, Marks{1: {{Start: 6, End: 8}}}) {
		t.Errorf("multi-byte needle: got %v", got)
	}
	// Non-overlapping, as before: "aaa" holds one "aa", not two.
	aaa := []LineTokens{{LineNum: 1, Tokens: []Token{{Text: "aaa"}}}}
	if got := FindLiteral(aaa, "aa"); !reflect.DeepEqual(got, Marks{1: {{Start: 1, End: 2}}}) {
		t.Errorf("overlap: got %v", got)
	}
}

func BenchmarkFindLiteral(b *testing.B) {
	lines := make([]LineTokens, 20000)
	for i := range lines {
		lines[i] = LineTokens{LineNum: i + 1, Tokens: []Token{{Text: strings.Repeat("señor=value; ", 8) + "token=sk_live_ABC"}}}
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		FindLiteral(lines, "sk_live_ABC")
	}
}

// An unknown --lang falls through to filename or content detection without a
// word. KnownLang and LexerName are how the CLI can say so, and what it used.
func TestKnownLang(t *testing.T) {
	for _, name := range []string{"go", "python", "http", "text", "JSON"} {
		if !KnownLang(name) {
			t.Errorf("KnownLang(%q) = false", name)
		}
	}
	for _, name := range []string{"", "pyhton", "no-such-lexer"} {
		if KnownLang(name) {
			t.Errorf("KnownLang(%q) = true", name)
		}
	}
	if got := LexerName("x = 1\n", "pyhton", "a.go"); got != "Go" {
		t.Errorf("LexerName with an unknown --lang and a .go file = %q, want Go", got)
	}
	if got := LexerName("x = 1\n", "python", "a.go"); got != "Python" {
		t.Errorf("LexerName with a known --lang = %q, want Python", got)
	}
}

// Invalid bytes turn into U+FFFD inside chroma and draw as a real glyph, so the
// image silently loses their values. The count is what lets the CLI say so.
func TestInvalidUTF8(t *testing.T) {
	cases := []struct {
		src         string
		count, line int
	}{
		{"plain\nñ → ✓\n", 0, 0},
		{"token=abc\xff\xfedef\n", 2, 1},
		{"ok\nok\nbad\x80\n\xc3\n", 2, 3},
		// A literal U+FFFD in the source is valid UTF-8, not a lost byte.
		{"already � here\n", 0, 0},
	}
	for _, c := range cases {
		n, line := InvalidUTF8(c.src)
		if n != c.count || line != c.line {
			t.Errorf("InvalidUTF8(%q) = %d, line %d; want %d, line %d", c.src, n, line, c.count, c.line)
		}
	}
	// The rendering stays as it was: each invalid byte is one U+FFFD.
	lines, err := Tokenize("token=abc\xff\xfedef\n", "text", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := lines[0].Text(); got != "token=abc��def" {
		t.Errorf("rendered text = %q", got)
	}
}

// Overlapping marks from different needles reached the renderer as separate
// boxes, and the nested borders split one secret into what reads as two
// values. Normalize returns each line's column spans as a union.
func TestMarksNormalize(t *testing.T) {
	m := Marks{2: {{Start: 10, End: 25}, {Start: 12, End: 18}, {Start: 26, End: 30}, {Start: 40, End: 41}}, 3: {{}, {}}, 4: nil}
	got := m.Normalize()
	want := Marks{2: {{Start: 10, End: 30}, {Start: 40, End: 41}}, 3: {{}}, 4: nil}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	// The receiver is not touched: it may be the caller's config.
	if len(m[2]) != 4 || m[2][1] != (Span{Start: 12, End: 18}) {
		t.Errorf("receiver was modified: %v", m[2])
	}
	// A whole-line span stays beside the column spans it shares a line with.
	if got := (Marks{5: {{Start: 3, End: 4}, {}, {Start: 1, End: 3}}}).Normalize(); !reflect.DeepEqual(got, Marks{5: {{}, {Start: 1, End: 4}}}) {
		t.Errorf("whole line with columns: got %v", got)
	}
	var nilMarks Marks
	if got := nilMarks.Normalize(); got != nil {
		t.Errorf("nil Normalize = %v, want nil", got)
	}
}

// A tab moves to the next tab stop; it is not a fixed run of spaces. With a
// fixed run, "#define A\t1" and "#define XY\t333" land their values in
// different columns although every editor and terminal lines them up. The
// column carries across tokens and resets on each line.
func TestTokenizeTabStops(t *testing.T) {
	src := "#define A\t1\t/* one */\n#define XY\t333\t/* three */\n\tindent\n"
	lines, err := Tokenize(src, "c", "tabs.c", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"#define A   1   /* one */",
		"#define XY  333 /* three */",
		"    indent",
	}
	for i, w := range want {
		if got := lines[i].Text(); got != w {
			t.Errorf("width 4, line %d = %q, want %q", i+1, got, w)
		}
	}

	// A terminal transcript uses 8, like the terminal that printed it.
	lines, err = TokenizeTabs("KEY\tVALUE\nAPI_TOKEN\tsecret\nñ\tx\n", "text", "", nil, 8)
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"KEY     VALUE", "API_TOKEN       secret", "ñ       x"}
	for i, w := range want {
		if got := lines[i].Text(); got != w {
			t.Errorf("width 8, line %d = %q, want %q", i+1, got, w)
		}
	}
}

// A pasted needle still holds the tab, while the drawn text holds the spaces
// it expanded to — however many that was at that column.
func TestFindMarksMatchesTabNeedle(t *testing.T) {
	lines, err := TokenizeTabs("KEY\tVALUE\nAPI_TOKEN\tsecret\n", "text", "", nil, 8)
	if err != nil {
		t.Fatal(err)
	}
	found, stats := FindMarks([][]LineTokens{lines}, []string{"API_TOKEN\tsecret", "KEY\tVALUE"}, 0)
	if stats[0].Matches != 1 || stats[1].Matches != 1 {
		t.Fatalf("stats = %+v, want one match each", stats)
	}
	if got := found[0][2]; len(got) != 1 || got[0] != (Span{Start: 1, End: 22}) {
		t.Errorf("API_TOKEN\\tsecret = %v, want {1 22}", got)
	}
	if got := found[0][1]; len(got) != 1 || got[0] != (Span{Start: 1, End: 13}) {
		t.Errorf("KEY\\tVALUE = %v, want {1 13}", got)
	}
	if stats[0].Literal != "API_TOKEN\tsecret" || stats[0].Flag != "--mark" {
		t.Errorf("stats name the needle as given: %+v", stats[0])
	}
}
