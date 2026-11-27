package highlight

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

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
	Flag    string // the flag to name in messages: "--mark", "--mark-re", "--redact"
	Matches int    // occurrences across every message
	Lines   int    // distinct lines carrying at least one occurrence
	Dropped int    // occurrences a limit left out
}

func (s MarkStats) flag() string {
	if s.Flag != "" {
		return s.Flag
	}
	return "--mark"
}

// Message renders the one line the CLI prints about this literal.
func (s MarkStats) Message() string {
	f := s.flag()
	switch {
	case s.Matches == 0:
		return fmt.Sprintf("warning: %s %q matched nothing", f, s.Literal)
	case s.Dropped > 0:
		return fmt.Sprintf("%s %q: %d matches on %d lines; kept the first %d, dropped %d",
			f, s.Literal, s.Matches, s.Lines, s.Matches-s.Dropped, s.Dropped)
	case s.Lines >= crowdedMarkLines:
		return fmt.Sprintf("%s %q: %d matches on %d lines; --max-marks keeps a mark visible",
			f, s.Literal, s.Matches, s.Lines)
	default:
		return fmt.Sprintf("%s %q: %d %s", f, s.Literal, s.Matches, plural(s.Matches, "match", "matches"))
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

	seen := map[string]bool{}
	for _, needle := range needles {
		if needle == "" || seen[needle] {
			continue
		}
		seen[needle] = true
		st := MarkStats{Literal: needle, Flag: "--mark"}
		// The token text has tabs expanded to their tab stops, so a needle
		// that still holds a literal tab can never match what the reader sees,
		// and no fixed run of spaces stands for it either: how many a tab
		// became depends on its column. Each tab matches a run of spaces.
		find := func(msg []LineTokens) Marks { return FindLiteral(msg, needle) }
		if strings.Contains(needle, "\t") {
			re := tabNeedle(needle)
			find = func(msg []LineTokens) Marks { return findRegex(msg, re) }
		}

		// Collect in message-then-line order: the finders return a map, and
		// "the first 5 matches" has to mean the earliest ones a reader would
		// find scrolling down the image.
		var hits []ScopedSpan
		lines := map[[2]int]bool{}
		for mi, msg := range messages {
			found := find(msg)
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

// FindRegexMarks is FindMarks for compiled patterns. Match offsets are byte
// offsets while spans count characters, so each match is measured in runes — a
// span that counted bytes would drift right on any line holding non-ASCII text.
// Zero-width matches are skipped: an empty box points at nothing.
func FindRegexMarks(messages [][]LineTokens, patterns []*regexp.Regexp, limit int) ([]Marks, []MarkStats) {
	out := make([]Marks, len(messages))
	var stats []MarkStats

	for _, re := range patterns {
		if re == nil {
			continue
		}
		st := MarkStats{Literal: re.String(), Flag: "--mark-re"}

		var hits []ScopedSpan
		lines := map[[2]int]bool{}
		for mi, msg := range messages {
			found := findRegex(msg, re)
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

// findRegex returns every non-empty match of re on the given lines, in rune
// columns. Matches come in order and never overlap, so the column is carried
// forward from the previous match instead of being recounted from column 1:
// that recount made a one-line JSON body quadratic in its length.
func findRegex(lines []LineTokens, re *regexp.Regexp) Marks {
	var found Marks
	for _, lt := range lines {
		if lt.IsGap {
			continue
		}
		text := lt.Text()
		prev, col := 0, 0
		for _, loc := range re.FindAllStringIndex(text, -1) {
			if loc[0] == loc[1] {
				continue
			}
			col += utf8.RuneCountInString(text[prev:loc[0]])
			start := col + 1
			col += utf8.RuneCountInString(text[loc[0]:loc[1]])
			prev = loc[1]
			if found == nil {
				found = Marks{}
			}
			found[lt.LineNum] = append(found[lt.LineNum], Span{Start: start, End: col})
		}
	}
	return found
}

// tabNeedle compiles a literal needle that holds tabs into a pattern where
// each tab matches the run of spaces it became when the text was expanded.
func tabNeedle(needle string) *regexp.Regexp {
	parts := strings.Split(needle, "\t")
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	return regexp.MustCompile(strings.Join(parts, " +"))
}

// KeepSpans shifts every span's start right by n, so a redaction can leave the
// first characters of a secret legible ("sk_l…") while the box still says
// where the rest was. A whole-line span is not a redaction candidate and is
// dropped.
//
// A span n or fewer characters long has nothing left to hide, so it is dropped
// too — and that is a secret drawn in full. consumed counts those spans so the
// caller can refuse or warn: a regex match or a non-ASCII literal can be
// shorter than keep even when every byte-length check upstream passed, and a
// redaction that silently exposes one of five matches looks exactly like one
// that hid all five.
func KeepSpans(m Marks, n int) (kept Marks, consumed int) {
	if n <= 0 {
		return m, 0
	}
	out := Marks{}
	for ln, spans := range m {
		for _, sp := range spans {
			if sp.WholeLine() {
				continue
			}
			sp.Start += n
			if sp.Start <= sp.End {
				out[ln] = append(out[ln], sp)
			} else {
				consumed++
			}
		}
	}
	if len(out) == 0 {
		return nil, consumed
	}
	return out, consumed
}
