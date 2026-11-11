package highlight

import "fmt"

// crowdedMarkLines is where a mark stops pointing at anything. Past a handful
// of lines the wash covers so much of the panel that the finding drowns in it,
// and the analyst is better served by a narrower literal than by a bigger
// image. The number is a legibility signal, not a rule: nothing is refused
// below or above it, the report just says how many lines were painted.
const crowdedMarkLines = 8

// ScopedSpan is one occurrence of a literal, and which message it is in. An
// HTTP pair is two messages, and a limit has to keep the earliest matches
// across both rather than the first N of each.
type ScopedSpan struct {
	Message int
	Line    int
	Span    Span
}

// MarkStats is what a --mark literal actually found. The counts exist because a
// literal is usually a guess: printing what it matched is the difference
// between "the image shows the finding" and "the image shows 25 red boxes and
// nobody noticed".
type MarkStats struct {
	Literal string
	Matches int // occurrences across every message
	Lines   int // distinct lines carrying at least one occurrence
	Dropped int // occurrences a limit left out
}

// Message renders the one line the CLI prints about this literal.
func (s MarkStats) Message() string {
	switch {
	case s.Matches == 0:
		return fmt.Sprintf("warning: --mark %q matched nothing", s.Literal)
	case s.Dropped > 0:
		return fmt.Sprintf("--mark %q: %d matches on %d lines; kept the first %d, dropped %d",
			s.Literal, s.Matches, s.Lines, s.Matches-s.Dropped, s.Dropped)
	case s.Lines >= crowdedMarkLines:
		return fmt.Sprintf("--mark %q: %d matches on %d lines; --max-marks keeps a mark visible",
			s.Literal, s.Matches, s.Lines)
	default:
		return fmt.Sprintf("--mark %q: %d %s", s.Literal, s.Matches, plural(s.Matches, "match", "matches"))
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// FindMarks locates every literal across one or more messages and reports what
// each one found. limit caps the occurrences kept per literal, earliest first
// (0 keeps all); the matches a cap leaves out are reported, never dropped in
// silence.
//
// It returns one Marks per message, in the order the messages were given, so an
// HTTP pair can merge each side into its own panel.
func FindMarks(messages [][]LineTokens, needles []string, limit int) ([]Marks, []MarkStats) {
	out := make([]Marks, len(messages))
	var stats []MarkStats

	for _, needle := range needles {
		if needle == "" {
			continue
		}
		st := MarkStats{Literal: needle}

		// Collect in message-then-line order: FindLiteral returns a map, and
		// "the first 5 matches" has to mean the earliest ones a reader would
		// find scrolling down the image.
		var hits []ScopedSpan
		lines := map[[2]int]bool{}
		for mi, msg := range messages {
			found := FindLiteral(msg, needle)
			for _, ln := range found.Lines() {
				lines[[2]int{mi, ln}] = true
				for _, sp := range found[ln] {
					hits = append(hits, ScopedSpan{Message: mi, Line: ln, Span: sp})
				}
			}
		}
		st.Matches, st.Lines = len(hits), len(lines)

		if limit > 0 && len(hits) > limit {
			st.Dropped = len(hits) - limit
			hits = hits[:limit]
		}
		for _, h := range hits {
			if out[h.Message] == nil {
				out[h.Message] = Marks{}
			}
			out[h.Message][h.Line] = append(out[h.Message][h.Line], h.Span)
		}
		stats = append(stats, st)
	}
	return out, stats
}
