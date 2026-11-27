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
