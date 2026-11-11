package highlight

import (
	"strings"
	"testing"
)

func linesOf(texts ...string) []LineTokens {
	out := make([]LineTokens, 0, len(texts))
	for i, t := range texts {
		out = append(out, LineTokens{LineNum: i + 1, Tokens: []Token{{Text: t}}})
	}
	return out
}

// A literal is a guess about where the finding is, so the count is the only
// thing telling the analyst whether the mark landed on the vulnerable line or
// painted half the panel. Same for the cap: dropping matches silently would
// turn a marked image into an unmarked one that still looks marked.
func TestFindMarksReportsAndCaps(t *testing.T) {
	req := linesOf("GET /a?t=abc HTTP/1.1", "Host: x")
	res := linesOf("HTTP/1.1 200", "abc-1", "abc-2", "abc-3")

	marked, stats := FindMarks([][]LineTokens{req, res}, []string{"abc"}, 0)
	if len(stats) != 1 {
		t.Fatalf("got %d stats, want 1", len(stats))
	}
	if stats[0].Matches != 4 || stats[0].Lines != 4 || stats[0].Dropped != 0 {
		t.Errorf("stats = %+v, want 4 matches on 4 lines, none dropped", stats[0])
	}
	// One Marks per message, and a literal found on both sides lands on both.
	if len(marked[0]) != 1 || len(marked[1]) != 3 {
		t.Errorf("marks per message = %d/%d, want 1/3", len(marked[0]), len(marked[1]))
	}

	marked, stats = FindMarks([][]LineTokens{req, res}, []string{"abc"}, 2)
	if stats[0].Dropped != 2 {
		t.Errorf("dropped = %d, want 2", stats[0].Dropped)
	}
	// Earliest first: the request line, then the response's first body line.
	if !marked[0].Has(1) {
		t.Error("the earliest match (request line) was not kept")
	}
	if len(marked[1]) != 1 || !marked[1].Has(2) {
		t.Errorf("kept response lines = %v, want just line 2", marked[1].Lines())
	}
	if msg := stats[0].Message(); !strings.Contains(msg, "kept the first 2") ||
		!strings.Contains(msg, "dropped 2") {
		t.Errorf("message = %q, want the kept and dropped counts", msg)
	}
}

func TestMarkStatsMessage(t *testing.T) {
	cases := []struct {
		st   MarkStats
		want string
	}{
		{MarkStats{Literal: "x"}, "warning: --mark \"x\" matched nothing"},
		{MarkStats{Literal: "x", Matches: 1, Lines: 1}, "--mark \"x\": 1 match"},
		{MarkStats{Literal: "x", Matches: 2, Lines: 2}, "--mark \"x\": 2 matches"},
		// Past a handful of lines the wash buries the finding, and the analyst
		// is told the lever that fixes it.
		{MarkStats{Literal: "x", Matches: 20, Lines: 20}, "--mark \"x\": 20 matches on 20 lines; --max-marks keeps a mark visible"},
	}
	for _, c := range cases {
		if got := c.st.Message(); got != c.want {
			t.Errorf("Message() = %q, want %q", got, c.want)
		}
	}
}
