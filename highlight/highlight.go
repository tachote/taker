package highlight

import (
	"strings"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// Token is a single syntax-highlighted token with resolved RGB color.
type Token struct {
	Text    string
	R, G, B uint8
}

// LineTokens represents one display line.
type LineTokens struct {
	LineNum int
	IsGap   bool
	Skipped int // for gap rows: how many lines were omitted
	Tokens  []Token
}

// Tokenize tokenizes src and returns one LineTokens per source line.
// lang may be empty for auto-detection. filename is used for extension-based detection.
func Tokenize(src, lang, filename string, style *chroma.Style) ([]LineTokens, error) {
	lexer := chroma.Coalesce(pickLexer(src, lang, filename))

	if style == nil {
		style = styles.Get("monokai")
		if style == nil {
			style = styles.Fallback
		}
	}

	it, err := lexer.Tokenise(nil, src)
	if err != nil {
		return nil, err
	}

	fr, fg, fb := defaultText(style)

	var lines []LineTokens
	current := LineTokens{LineNum: 1}

	for tok := it(); tok != chroma.EOF; tok = it() {
		text := expandTabs(tok.Value, 4)
		entry := style.Get(tok.Type)
		r, g, b := fr, fg, fb
		if entry.Colour.IsSet() {
			r, g, b = entry.Colour.Red(), entry.Colour.Green(), entry.Colour.Blue()
		}

		parts := strings.Split(text, "\n")
		for i, part := range parts {
			if i > 0 {
				lines = append(lines, current)
				current = LineTokens{LineNum: current.LineNum + 1}
			}
			// A CRLF capture leaves \r on every part: it draws as a blank
			// glyph that pads the row and inflates the column count.
			part = strings.TrimSuffix(part, "\r")
			if part != "" {
				current.Tokens = append(current.Tokens, Token{Text: part, R: r, G: g, B: b})
			}
		}
	}
	// Flush last line
	lines = append(lines, current)

	// A trailing "\n" closes the last line; it does not open a new one. Without
	// this, every file that ends in a newline renders one phantom blank line.
	if len(lines) > 1 && len(lines[len(lines)-1].Tokens) == 0 {
		lines = lines[:len(lines)-1]
	}

	return lines, nil
}

// pickLexer chooses the lexer: an explicit language, then the filename, then
// content analysis, then plain text. A language chroma does not know falls
// through to the next step without a word — KnownLang is how a caller finds
// out.
func pickLexer(src, lang, filename string) chroma.Lexer {
	var lexer chroma.Lexer
	if lang != "" {
		lexer = lexers.Get(lang)
	}
	if lexer == nil && filename != "" {
		lexer = lexers.Match(filename)
	}
	if lexer == nil {
		lexer = lexers.Analyse(src)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	return lexer
}

// KnownLang reports whether name is a language chroma can highlight. Tokenize
// treats an unknown --lang as absent, so "pyhton" quietly becomes whatever the
// filename or content analysis guesses — on stdin, often plain text. The CLI
// needs this to say so instead of handing back an unhighlighted image.
func KnownLang(name string) bool {
	return name != "" && lexers.Get(name) != nil
}

// LexerName is the name of the lexer Tokenize would use for the same
// arguments, so a warning about an unknown --lang can say what was used
// instead.
func LexerName(src, lang, filename string) string {
	return pickLexer(src, lang, filename).Config().Name
}

// InvalidUTF8 counts the bytes of src that are not valid UTF-8, and the
// 1-based line of the first one (0 when there are none). chroma lexes runes, so
// each such byte becomes U+FFFD before a token exists; U+FFFD has a glyph, so
// nothing downstream notices that the image cannot show what the byte was, and
// a --mark or --redact needle holding those bytes can never match. The CLI
// uses this to say so. Rendering is unchanged: the source hash keeps the raw
// bytes for provenance.
func InvalidUTF8(src string) (count, firstLine int) {
	line := 1
	for i := 0; i < len(src); {
		r, size := utf8.DecodeRuneInString(src[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			count++
			if firstLine == 0 {
				firstLine = line
			}
		case r == '\n':
			line++
		}
		i += size
	}
	return count, firstLine
}

// Text is the plain text of the line, with tabs already expanded, so column
// numbers taken from it line up with what gets drawn.
func (lt LineTokens) Text() string {
	var b strings.Builder
	for _, tok := range lt.Tokens {
		b.WriteString(tok.Text)
	}
	return b.String()
}

// FindLiteral marks every occurrence of needle across the given lines.
//
// This is the difference between usable and not: counting columns to write
// "7:63-85" is error-prone, whereas pasting the secret you just found is the
// thing an analyst already has in the clipboard.
func FindLiteral(lines []LineTokens, needle string) Marks {
	if needle == "" {
		return nil
	}
	n := utf8.RuneCountInString(needle)
	found := Marks{}
	for _, lt := range lines {
		if lt.IsGap {
			continue
		}
		// strings.Index finds the bytes; columns are runes, so the count is
		// carried from one match to the next instead of converting the line
		// and comparing a fresh string at every position.
		text := lt.Text()
		off, col := 0, 0
		for {
			j := strings.Index(text[off:], needle)
			if j < 0 {
				break
			}
			col += utf8.RuneCountInString(text[off : off+j])
			found[lt.LineNum] = append(found[lt.LineNum], Span{Start: col + 1, End: col + n})
			col += n
			off += j + len(needle)
		}
	}
	if len(found) == 0 {
		return nil
	}
	return found
}

// Merge returns the union of m and other. Neither input is modified: RenderHTTP
// merges literal matches into the caller's highlight map, and a render must not
// leave the config it was handed different from how it found it.
func (m Marks) Merge(other Marks) Marks {
	if len(other) == 0 {
		return m
	}
	out := make(Marks, len(m)+len(other))
	for line, spans := range m {
		out[line] = append([]Span(nil), spans...)
	}
	for line, spans := range other {
		out[line] = append(out[line], spans...)
	}
	return out
}

// FilterLines applies a LineSpec (with gap sentinels) to the full tokenized lines.
// Returns a filtered slice ready for rendering.
func FilterLines(all []LineTokens, spec []int) []LineTokens {
	if len(spec) == 0 {
		return all
	}

	// Build map for O(1) lookup
	byNum := make(map[int]LineTokens, len(all))
	for _, lt := range all {
		byNum[lt.LineNum] = lt
	}

	// Keep only lines that exist, then derive the gaps from those. Deriving from
	// the spec's sentinels instead would emit gaps for out-of-range requests —
	// a trailing "896 lines skipped" on a 115-line file, with nothing after it.
	var found []LineTokens
	for _, n := range spec {
		if n == GapSentinel {
			continue
		}
		if lt, ok := byNum[n]; ok {
			found = append(found, lt)
		}
	}

	var result []LineTokens
	for i, lt := range found {
		if i > 0 {
			if skipped := lt.LineNum - found[i-1].LineNum - 1; skipped > 0 {
				result = append(result, LineTokens{IsGap: true, Skipped: skipped})
			}
		}
		result = append(result, lt)
	}
	return result
}

func expandTabs(s string, tabWidth int) string {
	return strings.ReplaceAll(s, "\t", strings.Repeat(" ", tabWidth))
}

// defaultText is the colour for a token the style leaves uncoloured. Style.Get
// already inherits the style's Text entry, so this only runs for styles that
// set no text colour at all — chroma's light "github" among them. The VS Code
// dark default on its white background drew plain-text evidence (snap of a
// .txt, every term and exec transcript) light grey on white, so the fallback
// follows the background: GitHub's own foreground on a light one.
func defaultText(style *chroma.Style) (r, g, b uint8) {
	if c := style.Get(chroma.Text).Colour; c.IsSet() {
		return c.Red(), c.Green(), c.Blue()
	}
	if bg := style.Get(chroma.Background).Background; bg.IsSet() && bg.Brightness() > 0.5 {
		return 0x1f, 0x23, 0x28 // GitHub's default foreground
	}
	return 0xd4, 0xd4, 0xd4 // VS Code default text
}
