package main

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// normalizeZeroWidth DELETES codepoints that carry no standalone glyph —
// format chars (Cf: BOM, ZWSP, ZWJ, bidi controls, soft hyphen) and combining
// marks (Mn/Me). Replacing them would still leave a rune the needle does not
// contain; deleting them makes the spliced needle contiguous again, and
// because matcher and renderer share the normalized text, columns stay
// aligned and the box lands on the needle.
//
// A base letter followed by combining marks is composed first, so a
// decomposed accent ("Jose" + U+0301) stays "José" instead of becoming a
// changed name. Only those sequences: NFC over the whole text also replaces
// canonical singletons — U+212A KELVIN SIGN became an ASCII K, U+037E GREEK
// QUESTION MARK a ';' — which erased the very homoglyph a finding is about,
// silenced the no-glyph warning and broke a --redact needle holding the
// original codepoint. A base that NFC would itself replace is never composed.
//
// Composing can still move a needle: "s3cr3tpasse" + U+0301 composes to
// "...passé", where the needle "s3cr3tpasse" no longer matches, though it
// did when the mark was simply deleted. g names what the run looks for; on a
// line where composing would leave a rune of some match uncovered, the
// sequences around that match are left uncomposed (marks deleted), and if
// that is not enough the whole line is. The box then covers what it covered
// before composing existed, and the rest of the text keeps its accents.
func normalizeZeroWidth(b []byte, g needleGuard) ([]byte, int) {
	s := string(b)
	if !strings.ContainsFunc(s, invisible) {
		return b, 0
	}
	var out strings.Builder
	out.Grow(len(s))
	n := 0
	for _, line := range strings.SplitAfter(s, "\n") {
		if !strings.ContainsFunc(line, invisible) {
			out.WriteString(line)
			continue
		}
		segs := segmentLine(line)
		comp := g.choose(segs)
		for k, sg := range segs {
			if comp[k] {
				out.WriteString(sg.comp)
				n += sg.compDel
			} else {
				out.WriteString(sg.base)
				n += sg.baseDel
			}
		}
	}
	return []byte(out.String()), n
}

// normalizeNeedle puts a --redact or --mark literal through the input's own
// normalization, so the needle and the text agree: a needle typed with a
// ZWSP, an ANSI code or a decomposed accent matches the cells the image
// draws, which no longer hold those.
func normalizeNeedle(s string) string {
	b, _ := stripANSI([]byte(s))
	out, _ := normalizeZeroWidth(b, needleGuard{})
	return string(out)
}

// invisible is a rune normalizeZeroWidth deletes.
func invisible(r rune) bool {
	return r == '\uFEFF' || r == '\u00AD' || unicode.Is(unicode.Cf, r) || isMark(r)
}

func isMark(r rune) bool { return unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) }

// markSeg is one base rune and the combining marks after it, or one
// invisible rune with nothing to attach to. base is what the cell shows with
// the marks deleted, comp what it shows composed; both are "" when nothing is
// shown, and both are one rune otherwise, so the two forms of a line stay
// aligned rune for rune. The counts are the runes each form deletes.
type markSeg struct {
	base, comp       string
	baseDel, compDel int
}

func segmentLine(line string) []markSeg {
	var segs []markSeg
	for i := 0; i < len(line); {
		r, size := utf8.DecodeRuneInString(line[i:])
		j := i + size
		if !isMark(r) {
			for j < len(line) {
				m, sz := utf8.DecodeRuneInString(line[j:])
				if !isMark(m) {
					break
				}
				j += sz
			}
		}
		marks := utf8.RuneCountInString(line[i+size : j])
		starter := line[i : i+size]
		var sg markSeg
		switch {
		case invisible(r):
			sg = markSeg{baseDel: 1 + marks, compDel: 1 + marks}
		default:
			// Invalid bytes are copied as they are, as before any of this.
			sg = markSeg{base: starter, comp: starter, baseDel: marks, compDel: marks}
			if marks > 0 && r != utf8.RuneError && norm.NFC.IsNormalString(starter) {
				c := norm.NFC.String(line[i:j])
				first, sz := utf8.DecodeRuneInString(c)
				rest := c[sz:]
				if c[:sz] != starter && !invisible(first) &&
					!strings.ContainsFunc(rest, func(r rune) bool { return !invisible(r) }) {
					sg.comp, sg.compDel = c[:sz], utf8.RuneCountInString(rest)
				}
			}
		}
		segs = append(segs, sg)
		i = j
	}
	return segs
}

// needleGuard is what a run will look for in its normalized input: the
// --redact and --mark literals and every compiled pattern, matched the way
// the renderer will see the line, tabs expanded.
type needleGuard struct {
	lits     []string
	res      []*regexp.Regexp
	tabWidth int
}

func newNeedleGuard(tabWidth int, lits []string, res ...[]*regexp.Regexp) needleGuard {
	g := needleGuard{tabWidth: tabWidth}
	for _, l := range lits {
		switch {
		case l == "":
		case strings.Contains(l, "\t"):
			// The renderer matches a needle's tab against the run of
			// spaces the text's tab expanded to (highlight.tabNeedle).
			parts := strings.Split(l, "\t")
			for i, p := range parts {
				parts[i] = regexp.QuoteMeta(p)
			}
			g.res = append(g.res, regexp.MustCompile(strings.Join(parts, " +")))
		default:
			g.lits = append(g.lits, l)
		}
	}
	for _, list := range res {
		for _, re := range list {
			if re != nil {
				g.res = append(g.res, re)
			}
		}
	}
	return g
}

// choose says, per segment of one line, whether it is drawn composed.
func (g needleGuard) choose(segs []markSeg) []bool {
	comp := make([]bool, len(segs))
	differs := false
	for k, sg := range segs {
		comp[k] = true
		differs = differs || sg.comp != sg.base
	}
	if !differs || (len(g.lits) == 0 && len(g.res) == 0) {
		return comp
	}
	covBase := g.cover(segs, make([]bool, len(segs)))
	covered := func(c []bool) bool {
		cov := g.cover(segs, c)
		for k := range covBase {
			if covBase[k] && !cov[k] {
				return false
			}
		}
		return true
	}
	if covered(comp) {
		return comp
	}
	// Leave the sequences under and beside each lost match uncomposed: the
	// beside ones because \b and \B look one rune past the match.
	covComp := g.cover(segs, comp)
	hybrid := make([]bool, len(segs))
	copy(hybrid, comp)
	shown := []int{} // indexes of segments that draw a cell
	for k, sg := range segs {
		if sg.base != "" {
			shown = append(shown, k)
		}
	}
	for x, k := range shown {
		if covBase[k] && !covComp[k] {
			for _, y := range []int{x - 1, x, x + 1} {
				if y >= 0 && y < len(shown) {
					hybrid[shown[y]] = false
				}
			}
		}
	}
	if covered(hybrid) {
		return hybrid
	}
	return make([]bool, len(segs))
}

// cover marks the segments that some needle's match touches when the line is
// drawn with segment k composed where comp[k] holds.
func (g needleGuard) cover(segs []markSeg, comp []bool) []bool {
	var b strings.Builder
	var owner []int // owner[i] is the segment byte i of the drawn line came from
	col := 0
	for k, sg := range segs {
		u := sg.base
		if comp[k] {
			u = sg.comp
		}
		switch {
		case u == "" || u == "\n" || u == "\r":
			continue // the renderer matches a line without its ending
		case u == "\t" && g.tabWidth > 0:
			w := g.tabWidth - col%g.tabWidth
			for range w {
				b.WriteByte(' ')
				owner = append(owner, k)
			}
			col += w
			continue
		}
		b.WriteString(u)
		for range len(u) {
			owner = append(owner, k)
		}
		col++
	}
	text := b.String()
	cov := make([]bool, len(segs))
	mark := func(lo, hi int) {
		for i := lo; i < hi; i++ {
			cov[owner[i]] = true
		}
	}
	for _, l := range g.lits {
		for off := 0; ; {
			j := strings.Index(text[off:], l)
			if j < 0 {
				break
			}
			mark(off+j, off+j+len(l))
			off += j + len(l)
		}
	}
	for _, re := range g.res {
		for _, loc := range re.FindAllStringIndex(text, -1) {
			mark(loc[0], loc[1])
		}
	}
	return cov
}
