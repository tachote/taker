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
	Text         string
	R, G, B      uint8
	Bold, Italic bool
}

// LineTokens represents one display line.
type LineTokens struct {
	LineNum int
	IsGap   bool
	Tokens  []Token
}

// Tokenize tokenizes src and returns one LineTokens per source line.
// lang may be empty for auto-detection. filename is used for extension-based detection.
func Tokenize(src, lang, filename string, style *chroma.Style) ([]LineTokens, error) {
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
	lexer = chroma.Coalesce(lexer)

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

	var lines []LineTokens
	current := LineTokens{LineNum: 1}

	for tok := it(); tok != chroma.EOF; tok = it() {
		text := expandTabs(tok.Value, 4)
		entry := style.Get(tok.Type)
		r, g, b := colorToRGB(entry.Colour)
		// Use default text color if no color set
		if entry.Colour == 0 {
			defEntry := style.Get(chroma.Text)
			r, g, b = colorToRGB(defEntry.Colour)
			if r == 0 && g == 0 && b == 0 {
				r, g, b = 0xd4, 0xd4, 0xd4 // VS Code default text
			}
		}

		parts := strings.Split(text, "\n")
		for i, part := range parts {
			if i > 0 {
				lines = append(lines, current)
				current = LineTokens{LineNum: current.LineNum + 1}
			}
			if part != "" {
				current.Tokens = append(current.Tokens, Token{
					Text:   part,
					R:      r,
					G:      g,
					B:      b,
					Bold:   entry.Bold == chroma.Yes,
					Italic: entry.Italic == chroma.Yes,
				})
			}
		}
	}
	// Flush last line
	lines = append(lines, current)

	return lines, nil
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

	var result []LineTokens
	for _, n := range spec {
		if n == GapSentinel {
			result = append(result, LineTokens{IsGap: true})
			continue
		}
		if lt, ok := byNum[n]; ok {
			result = append(result, lt)
		}
	}
	return result
}

// LongestLineLen returns the character width of the longest line.
func LongestLineLen(lines []LineTokens) int {
	max := 0
	for _, lt := range lines {
		if lt.IsGap {
			continue
		}
		w := 0
		for _, tok := range lt.Tokens {
			w += utf8.RuneCountInString(tok.Text)
		}
		if w > max {
			max = w
		}
	}
	return max
}

func expandTabs(s string, tabWidth int) string {
	return strings.ReplaceAll(s, "\t", strings.Repeat(" ", tabWidth))
}

func colorToRGB(c chroma.Colour) (r, g, b uint8) {
	if c == 0 {
		return 0, 0, 0
	}
	return c.Red(), c.Green(), c.Blue()
}
