package highlight

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// ShellLine colours one shell command line the way an interactive shell with
// syntax highlighting draws it. chroma's bash lexer only knows builtins,
// keywords, strings and variables: every external command (curl, grep, ls) and
// every flag comes back as plain Text, so a transcript's command line would
// read as one colour. ShellLine keeps the lexer's colours and adds three
// roles on top: the word in command position, flags (words that start with
// "-") and redirections.
//
// The tokens' text joins back to cmd exactly; ok is false when it would not
// (for example cmd holds a newline), so the caller can keep its plain colours.
func ShellLine(cmd string, style *chroma.Style) (toks []Token, ok bool) {
	if strings.ContainsAny(cmd, "\n\r") {
		return nil, false
	}
	if style == nil {
		style = styles.Fallback
	}
	lexer := lexers.Get("bash")
	if lexer == nil {
		return nil, false
	}
	it, err := lexer.Tokenise(nil, cmd)
	if err != nil {
		return nil, false
	}
	var all []chroma.Token
	for t := it(); t != chroma.EOF; t = it() {
		if t.Value != "" {
			all = append(all, t)
		}
	}

	fr, fg, fb := defaultText(style)
	colour := func(tt chroma.TokenType, alt ...chroma.TokenType) (uint8, uint8, uint8) {
		for _, t := range append([]chroma.TokenType{tt}, alt...) {
			if c := style.Get(t).Colour; c.IsSet() {
				return c.Red(), c.Green(), c.Blue()
			}
		}
		return fr, fg, fb
	}
	cr, cg, cb := colour(chroma.NameFunction, chroma.NameBuiltin)
	flr, flg, flb := colour(chroma.NameAttribute, chroma.NameTag, chroma.LiteralNumber)
	rr, rg, rb := colour(chroma.Operator, chroma.Punctuation)

	cmdPos := true       // the next word names a command
	assignValue := false // the next word is the value of VAR=, not a command
	var b strings.Builder
	for i, t := range all {
		b.WriteString(t.Value)
		r, g, bl := colour(t.Type)
		switch {
		case t.Type == chroma.Text && strings.TrimSpace(t.Value) == "":
			// Whitespace changes nothing.
		case t.Type == chroma.Text:
			switch {
			case assignValue:
				assignValue = false
			case cmdPos:
				r, g, bl = cr, cg, cb
				cmdPos = false
			case isRedirect(t.Value):
				r, g, bl = rr, rg, rb
			case strings.HasPrefix(t.Value, "-"):
				r, g, bl = flr, flg, flb
			}
		case t.Type == chroma.NameVariable && cmdPos && i+1 < len(all) &&
			all[i+1].Type == chroma.Operator && all[i+1].Value == "=":
			// VAR=value before a command: the command is still to come.
		case t.Type == chroma.Operator && t.Value == "=" && cmdPos:
			assignValue = true
		case t.Type == chroma.Punctuation || t.Type == chroma.Operator:
			if opensCommand(t.Value) {
				cmdPos = true
			}
		case t.Type.InCategory(chroma.Keyword):
			// if, then, do, while … are followed by a command; for, select,
			// case and in by a name or a word list.
			switch t.Value {
			case "for", "select", "case", "in":
				cmdPos = false
			default:
				cmdPos = true
			}
		default:
			if cmdPos && t.Type != chroma.TextWhitespace {
				cmdPos = false
			}
		}
		toks = append(toks, Token{Text: t.Value, R: r, G: g, B: bl})
	}
	if b.String() != cmd {
		return nil, false
	}
	return toks, true
}

// opensCommand reports whether a shell operator is followed by a new command.
func opensCommand(op string) bool {
	switch strings.TrimSpace(op) {
	case "|", "||", "&&", ";", "&", "|&", "(", "{", "$(", "`", "!":
		return true
	}
	return false
}

// isRedirect reports whether a word is a redirection such as >, 2>&1 or
// 2>/dev/null, which the bash lexer leaves as plain Text.
func isRedirect(w string) bool {
	w = strings.TrimLeft(w, "0123456789&")
	return strings.HasPrefix(w, ">") || strings.HasPrefix(w, "<")
}
