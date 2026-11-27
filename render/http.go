package render

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"

	"github.com/tachote/taker/highlight"
)

const (
	httpDividerW      = 4 // a draggable-looking split zone, not a hairline
	httpDefaultCols   = 90
	httpDividerMargin = 20 // horizontal breathing room between a mark and the splitter
)

// Burp's tab strip and control row, measured off a real capture. A tab's label
// is inset inside its box, the underline is wider than the label, and the gap to
// the next tab is generous: that spacing is a large part of what makes a row of
// tabs read as a Burp panel.
const (
	burpTabLabelPad     = 6
	burpTabUnderlinePad = 31
	burpTabPitch        = 64
)

// HTTPConfig holds parameters for HTTP request/response evidence rendering.
// Either side may be empty: a finding often lives on one side alone (a data
// leak is the response; a smuggled verb is the request), and a single pane
// renders without the splitter.
type HTTPConfig struct {
	Request    string // may be empty when only the response is evidence
	Response   string // may be empty when only the request is evidence
	RequestHL  highlight.Marks
	ResponseHL highlight.Marks
	// Literals to find and mark in whichever panel contains them.
	Mark []string
	// MarkRe are compiled patterns marked the same way. They travel compiled
	// so a bad pattern is a caller's usage error, not a render error.
	MarkRe []*regexp.Regexp
	// Redact literals are hidden under a solid box instead of marked —
	// credentials and PII that must not reach the report image.
	Redact []string
	// RedactRe hides compiled-pattern matches the same way — a JWT or bearer
	// shape is a pattern, not a literal.
	RedactRe []*regexp.Regexp
	// RedactKeep leaves this many leading characters of each match visible
	// (sk_l…████) so the box still identifies what was hidden.
	RedactKeep int
	// RedactAsked means the caller requested masking — the footnote declares
	// "0 regions redacted" rather than looking like a clean capture.
	RedactAsked bool
	// MaxMarks caps the matches kept per literal, earliest first (0 = all).
	MaxMarks int
	// Caption states what the pane proves; empty leaves the pane bare.
	Caption string

	// Line specs (with gap sentinels) limiting each panel; nil renders all.
	RequestLines  []int
	ResponseLines []int
	// Context lines to keep either side of each mark. Resolved here rather than
	// by the caller because --mark's matches are only known after tokenizing.
	Context int

	WrapWidth int // columns per panel (0 = httpDefaultCols)

	// There is deliberately no theme field. HTTP evidence always renders in the
	// Burp palette: a report reader recognises a proxy capture at a glance, and
	// a Dracula-coloured pane labelled Request/Response/Pretty/Raw/Hex is a
	// screenshot of an application that does not exist.

	// Warn, when set, receives the warnings from both panels instead of stderr,
	// so the CLI can put them in its report. See Config.Warn.
	Warn func(msg string)

	// Facts, when non-nil, receives what the render actually drew: each panel's
	// extent and what every --mark literal found. A caller that has to report on
	// the image reads the numbers from here instead of recomputing the trim and
	// hoping the two agree.
	Facts *HTTPFacts
}

// warnf routes a render warning through Warn the way Config.warnf does, so the
// CLI's report and the caller's stderr both see it.
func (cfg *HTTPConfig) warnf(format string, args ...any) {
	if cfg.Warn != nil {
		cfg.Warn(fmt.Sprintf(format, args...))
	}
}

// burpColourMessage repaints a tokenized HTTP message with Burp's Pretty-view
// palette. chroma's http lexer stops at "header value" and knows nothing about
// cookies or form bodies, so the finer splits Burp shows are made here, on the
// line text. Body formats the lexer does understand (JSON) are left alone.
//
// The pass runs on the whole message, before any line range is applied, because
// it needs the blank line that ends the header block to tell a header from a
// body line.
func burpColourMessage(lines []highlight.LineTokens) []highlight.LineTokens {
	texts := make([]string, len(lines))
	for i, lt := range lines {
		texts[i] = lt.Text()
	}
	layout := layoutHTTP(texts)
	markup := isMarkup(layout.contentType)
	for i := range lines {
		lt := &lines[i]
		if lt.IsGap {
			continue
		}
		text := texts[i]
		switch {
		case i >= layout.body:
			lt.Tokens = burpBodyTokens(text, lt.Tokens, layout.contentType, markup)
		case isStartLine(text):
			// Request line or status line: one colour, no structure. Only a
			// line inside the header block can be one — a body line like
			// "GET /x HTTP/1.1" (a nested request, a pasted log) is body text.
			lt.Tokens = []highlight.Token{colourToken(text, burpLineColour)}
		case strings.TrimSpace(text) == "":
			// The separator that closes a header block.
		default:
			lt.Tokens = burpHeaderTokens(text)
		}
	}
	return lines
}

// httpLayout says where the parts of a message are, as 0-based line indexes.
type httpLayout struct {
	status      int    // first line of the final header block: the start line the image must show
	body        int    // first body line; the line count when the message has no body
	contentType string // the final block's Content-Type, lowercased, parameters kept
	encoding    string // the final block's Content-Encoding, lowercased
}

// interimStatusRe matches the status lines that announce another response
// after them: 1xx (100 Continue, 103 Early Hints) and the 200 a proxy sends
// back for CONNECT, which curl -i prints in front of the real response.
var interimStatusRe = regexp.MustCompile(`(?i)^HTTP/[0-9](?:\.[0-9])? +(?:1[0-9]{2}\b|2[0-9]{2} +connection established)`)

// statusLineRe is the start of a status line, for the line after an interim block.
var statusLineRe = regexp.MustCompile(`^HTTP/[0-9]`)

// layoutHTTP finds the header block, the body, and the Content-Type that
// decides how the body is painted. An interim block followed by a status line
// is not the end of the headers: the real response comes after it, and its
// headers and body are typed by its own block. Only those two interim shapes
// open a new block, so a final response whose body echoes "HTTP/1.1 200" (an
// SSRF reading an internal service) keeps that echo as body.
func layoutHTTP(lines []string) httpLayout {
	start := 0
	for {
		l := httpLayout{status: start, body: len(lines)}
		sep := -1
		for i := start; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "" {
				sep = i
				break
			}
			name, value, ok := splitHeader(lines[i])
			if !ok {
				continue
			}
			switch v := strings.ToLower(strings.TrimSpace(value)); headerKey(name) {
			case "content-type":
				if l.contentType == "" {
					l.contentType = v
				}
			case "content-encoding":
				if l.encoding == "" {
					l.encoding = v
				}
			}
		}
		if sep < 0 {
			return l
		}
		next := sep + 1
		for next < len(lines) && strings.TrimSpace(lines[next]) == "" {
			next++
		}
		if next < len(lines) && interimStatusRe.MatchString(lines[start]) && statusLineRe.MatchString(lines[next]) {
			start = next
			continue
		}
		l.body = sep + 1
		return l
	}
}

// tokenizeHTTP tokenizes a message in two parts. The header block is plain
// text, because burpColourMessage repaints every line of it anyway. The body
// gets the lexer its declared Content-Type names. Handing the whole message to
// chroma's http lexer instead loses the body colours whenever that lexer
// rejects one line before it — an HTTP/2 pseudo header, a WebDAV verb, a header
// with an empty value — and the repainted headers hide that it happened.
func tokenizeHTTP(msg string, style *chroma.Style) ([]highlight.LineTokens, httpLayout, error) {
	raw := messageLines(msg)
	layout := layoutHTTP(raw)
	msg = strings.Join(raw, "\n")

	body := strings.Join(raw[layout.body:], "\n")
	if body == "" {
		lines, err := highlight.Tokenize(msg, "text", "", style)
		return lines, layout, err
	}
	lines, err := highlight.Tokenize(strings.Join(raw[:layout.body], "\n"), "text", "", style)
	if err != nil {
		return nil, layout, err
	}
	// The block ends in its blank separator, which Tokenize reads as a
	// trailing newline and drops. The body's line numbers depend on it.
	for len(lines) < layout.body {
		lines = append(lines, highlight.LineTokens{LineNum: len(lines) + 1})
	}
	bodyLines, err := highlight.Tokenize(body, bodyLexer(layout.contentType), "", style)
	if err != nil {
		return nil, layout, err
	}
	for i := range bodyLines {
		bodyLines[i].LineNum += layout.body
	}
	return append(lines, bodyLines...), layout, nil
}

// messageLines splits a message on the lines Tokenize numbers: chroma turns
// CRLF and a lone CR into LF before it lexes.
func messageLines(msg string) []string {
	msg = strings.ReplaceAll(msg, "\r\n", "\n")
	return strings.Split(strings.ReplaceAll(msg, "\r", "\n"), "\n")
}

// warnUnreadableBody says when a body's pixels are not its content. A capture
// saved without decoding draws as rows of replacement glyphs, which reads as
// "the server sent garbage" instead of "these are the wire bytes". The image
// stays as it is — it is faithful to the capture — and the warning says what
// it shows. The CLI's input normaliser re-encodes invalid UTF-8 as U+FFFD, so
// that rune counts as binary too.
func (cfg *HTTPConfig) warnUnreadableBody(side, msg string, layout httpLayout) {
	body := strings.Join(messageLines(msg)[layout.body:], "\n")
	if strings.TrimSpace(body) == "" {
		return
	}
	for _, coding := range strings.Split(layout.encoding, ",") {
		switch strings.TrimSpace(coding) {
		case "gzip", "x-gzip", "br", "deflate", "zstd", "compress", "x-compress":
			cfg.warnf("the %s body is compressed (Content-Encoding: %s); the image shows the compressed bytes, not the content — decode it before rendering (curl --compressed)",
				side, layout.encoding)
			return
		}
	}
	if !utf8.ValidString(body) || strings.ContainsAny(body, "\x00\ufffd") {
		cfg.warnf("the %s body is binary (invalid UTF-8, NUL or U+FFFD); the image shows replacement glyphs, not the content", side)
	}
}

// mediaType is a Content-Type value without its parameters.
func mediaType(contentType string) string {
	mime, _, _ := strings.Cut(contentType, ";")
	return strings.TrimSpace(mime)
}

// bodyLexer names the lexer for a declared Content-Type, with the fallback
// chroma's own http lexer uses: a structured suffix ("application/problem+json")
// is read as its base type when the full type has no lexer of its own.
func bodyLexer(contentType string) string {
	mime := mediaType(contentType)
	lx := lexers.MatchMimeType(mime)
	if slash, plus := strings.Index(mime, "/"), strings.LastIndex(mime, "+"); lx == nil && slash >= 0 && plus > slash {
		lx = lexers.MatchMimeType(mime[:slash+1] + mime[plus+1:])
	}
	// Tokenize looks the lexer up by name. A name it cannot resolve would make
	// it guess from the body's content, and a body without a known type is text.
	if lx == nil || lexers.Get(lx.Config().Name) == nil {
		return "text"
	}
	return lx.Config().Name
}

// headerKey is a header name as it compares: lowercased, without the colon
// splitHeader leaves on it.
func headerKey(name string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(name), ":"))
}

// isStartLine reports a request line ("GET /x HTTP/1.1") or a status line
// ("HTTP/1.1 200 OK"). It is a test, not "is this the first line", because an
// HTTP/2 message has no start line at all: it opens straight into its pseudo
// headers, and treating `:method` as one flat run flattens the whole block.
func isStartLine(s string) bool {
	if strings.HasPrefix(s, "HTTP/") {
		return true
	}
	parts := strings.SplitN(s, " ", 3)
	return len(parts) == 3 &&
		!strings.Contains(parts[0], ":") &&
		strings.HasPrefix(parts[2], "HTTP/")
}

func lineText(lt highlight.LineTokens) string {
	var b strings.Builder
	for _, tok := range lt.Tokens {
		b.WriteString(tok.Text)
	}
	return b.String()
}

func colourToken(text string, c color.NRGBA) highlight.Token {
	return highlight.Token{Text: text, R: c.R, G: c.G, B: c.B}
}

// burpHeaderTokens colours one header line: the name and its colon in the line
// colour, the value in the plain value colour — except in a cookie header, where
// Burp still separates the cookie's name from its data.
func burpHeaderTokens(text string) []highlight.Token {
	name, value, ok := splitHeader(text)
	if !ok {
		// Not a header line (a folded value, a malformed tool's output):
		// colouring it as a value is the least wrong thing to do.
		return []highlight.Token{colourToken(text, burpHeaderValue)}
	}
	toks := []highlight.Token{colourToken(name, burpLineColour)}
	if isCookieHeader(name) {
		return append(toks, burpCookieTokens(name, value)...)
	}
	return append(toks, colourToken(value, burpHeaderValue))
}

// splitHeader cuts a header line into its name (colon included) and its value.
// The name is everything up to the first colon, except for HTTP/2 pseudo
// headers, where the name itself starts with one: in ":method: GET" the name is
// ":method", not the empty string before the leading colon.
func splitHeader(text string) (name, value string, ok bool) {
	if strings.HasPrefix(text, ":") {
		i := strings.Index(text[1:], ":")
		if i < 0 {
			return text, "", true // a pseudo header with no value
		}
		return text[:i+2], text[i+2:], true
	}
	name, value, ok = strings.Cut(text, ":")
	if ok {
		name += ":"
	}
	return name, value, ok
}

func isCookieHeader(name string) bool {
	switch strings.ToLower(strings.Trim(strings.TrimSpace(name), ":")) {
	case "set-cookie", "set-cookie2", "cookie":
		return true
	}
	return false
}

// burpCookieTokens colours a cookie header's value. The two header spellings
// are not the same shape: a request's `Cookie:` holds several name=value
// pairs and Burp colours each one, while `Set-Cookie:` carries one pair plus
// attributes, which stay in the value colour.
// The leading space stays in the value colour so the header reads as one line.
func burpCookieTokens(name, value string) []highlight.Token {
	toks := []highlight.Token{}
	pad := len(value) - len(strings.TrimLeft(value, " \t"))
	if pad > 0 {
		toks = append(toks, colourToken(value[:pad], burpHeaderValue))
	}
	rest := value[pad:]

	pair := func(seg string) {
		n, d, ok := strings.Cut(seg, "=")
		if !ok {
			toks = append(toks, colourToken(seg, burpHeaderValue))
			return
		}
		toks = append(toks,
			colourToken(n, burpParamName),
			colourToken("=", burpLineColour),
			colourToken(d, burpParamValue))
	}

	if strings.EqualFold(strings.Trim(strings.TrimSpace(name), ":"), "cookie") {
		for {
			seg, tail, found := strings.Cut(rest, ";")
			pair(seg)
			if !found {
				return toks
			}
			// The separator (and the spaces after it) is not a pair.
			sep := ";"
			for strings.HasPrefix(tail, " ") {
				sep += " "
				tail = tail[1:]
			}
			toks = append(toks, colourToken(sep, burpHeaderValue))
			rest = tail
		}
	}

	first, tail, _ := strings.Cut(rest, ";")
	pair(first)
	if tail != "" {
		toks = append(toks, colourToken(";"+tail, burpHeaderValue))
	}
	return toks
}

// repaintFraming turns a bare punctuation token into body text. It covers the
// characters a body uses to frame its own structure — json's braces, brackets,
// colon and comma, xml's angle brackets — and leaves the content colours alone.
func repaintFraming(toks []highlight.Token) []highlight.Token {
	out := make([]highlight.Token, len(toks))
	for i, tok := range toks {
		if isFraming(tok.Text) {
			out[i] = colourToken(tok.Text, burpBodyText)
			continue
		}
		out[i] = tok
	}
	return out
}

func isFraming(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch r {
		case '{', '}', '[', ']', ',', ':', '<', '>':
		default:
			return false
		}
	}
	return true
}

// burpBodyTokens colours a body line. A URL-encoded body gets Burp's field
// colours — separators in the line colour, names and values distinct — while
// anything else keeps what the lexer produced, so a JSON body stays coloured.
//
// Burp decides the body view from the declared Content-Type, not from the
// shape of a line: a json string carrying "a=1&b=2" is json, not a form. Only
// a message without a declared type falls back to the line's shape.
//
// markup says the body is XML or HTML, whose tag delimiters are split out of
// the lexer's tokens first (see splitMarkup).
func burpBodyTokens(text string, lexed []highlight.Token, contentType string, markup bool) []highlight.Token {
	if isFormContentType(contentType) || (contentType == "" && looksFormEncoded(text)) {
		return formFieldTokens(text)
	}
	if markup {
		lexed = splitMarkup(lexed)
	}
	// Burp paints a body's framing as ordinary text: in
	// `"username":"test",` the key and its value carry the json colours
	// while the colon and the comma stay plain. Measured on a capture of a
	// json body, where both came out neutral while the key was gold — so
	// the lexer's operator colour is repainted here rather than in the
	// style, which also colours the separators of a URL's query string and
	// there the theme's roles (separator, operator) are blue.
	return repaintFraming(lexed)
}

// isMarkup reports a declared XML or HTML body: the two lexers whose tag
// tokens carry delimiters. Decided by type, not by token text, because a JSON
// key holding "<b>" is in the same tag colour and must stay whole.
func isMarkup(contentType string) bool {
	switch bodyLexer(contentType) {
	case "XML", "HTML":
		return true
	}
	return false
}

// splitMarkup gives markup delimiters the plain body colour, as Burp paints
// them. chroma's XML lexer folds them into its tag tokens ("<user", "id=",
// "</name><email>"), and the HTML lexer leaves "></" and ")</" in the
// punctuation colour, so the same structure looked different depending on the
// declared type. Only the delimiters move: names keep the tag colour, and the
// text is unchanged, so marks and redaction columns still line up.
func splitMarkup(toks []highlight.Token) []highlight.Token {
	out := make([]highlight.Token, 0, len(toks))
	for _, tok := range toks {
		c := color.NRGBA{tok.R, tok.G, tok.B, 0xff}
		var delims string
		switch {
		case c == burpNameTag:
			delims = "<>/?!=" // a tag or attribute name holds none of these
		case c == burpLineColour && strings.ContainsAny(tok.Text, "<>"):
			delims = "<>/"
		default:
			out = append(out, tok)
			continue
		}
		rest := tok.Text
		for rest != "" {
			isDelim := strings.ContainsRune(delims, rune(rest[0]))
			n := strings.IndexFunc(rest, func(r rune) bool { return strings.ContainsRune(delims, r) != isDelim })
			if n < 0 {
				n = len(rest)
			}
			if isDelim {
				out = append(out, colourToken(rest[:n], burpBodyText))
			} else {
				out = append(out, highlight.Token{Text: rest[:n], R: tok.R, G: tok.G, B: tok.B})
			}
			rest = rest[n:]
		}
	}
	return out
}

// isFormContentType reports a declared URL-encoded body, parameters included.
func isFormContentType(contentType string) bool {
	return strings.Contains(contentType, "x-www-form-urlencoded")
}

// formFieldTokens paints one body line as Burp paints a URL-encoded body.
func formFieldTokens(text string) []highlight.Token {
	var toks []highlight.Token
	for i, field := range strings.Split(text, "&") {
		if i > 0 {
			toks = append(toks, colourToken("&", burpLineColour))
		}
		name, value, found := strings.Cut(field, "=")
		if !found {
			toks = append(toks, colourToken(field, burpLineColour))
			continue
		}
		toks = append(toks,
			colourToken(name, burpParamName),
			colourToken("=", burpLineColour),
			colourToken(value, burpParamValue),
		)
	}
	return toks
}

// looksFormEncoded reports k=v&k=v. Every field has to carry an '=' after a
// name that could be URL-encoded: never empty, and without the raw spaces,
// quotes, angle brackets or braces that a form encoder would have escaped.
// Requiring the '=' alone let any line with one in it through — prose
// ("total = 5"), markup (<a href="/x">), script, a JSON object.
func looksFormEncoded(s string) bool {
	if s == "" {
		return false
	}
	for _, field := range strings.Split(s, "&") {
		name, _, found := strings.Cut(field, "=")
		if !found || name == "" || strings.ContainsAny(name, " \t<>\"{}") {
			return false
		}
	}
	return true
}

// HTTPFacts is what RenderHTTP drew: marks are resolved inside the render, so
// the counts a caller needs only exist here. A Facts entry for a side that was
// never rendered keeps its zero value — Present tells the two apart.
type HTTPFacts struct {
	Request  PanelFacts
	Response PanelFacts
	Marks    []highlight.MarkStats
	Redacted int // black boxes drawn across both panels
}

// Present is set on a PanelFacts only when its side was rendered, so a caller
// can tell "panel absent" from "panel rendered and its extent is zero".
type PanelFacts struct {
	highlight.ViewExtent
	Total   int
	Present bool
}

// RenderHTTP renders HTTP evidence in Burp's palette: the request and the
// response side by side, or one pane alone when only one side is given. Marks
// (--mark, --mark-re, explicit spans) and redactions are found in the whole
// capture, then each panel is trimmed to its line range or, when it has a mark
// and Context is set, to the lines around its marks. A footnote states what
// the trim left out and what was redacted; Caption, when set, goes above it.
//
// It fails when both sides are empty, when a line range matches no line of
// its panel, and when a panel cannot be drawn (Render's errors, such as an
// image too tall to open). Warnings go to Warn: a redaction that matched
// nothing, a --context that replaced a line range, a view without the request
// or status line, a compressed or binary body, and the panels' own warnings.
// When Facts is set, it receives what the image shows.
func RenderHTTP(cfg *HTTPConfig) (*image.NRGBA, error) {
	theme := BurpTheme

	wrapCols := cfg.WrapWidth
	if wrapCols <= 0 {
		wrapCols = httpDefaultCols
	}

	hasReq := strings.TrimSpace(cfg.Request) != ""
	hasRes := strings.TrimSpace(cfg.Response) != ""
	if !hasReq && !hasRes {
		return nil, fmt.Errorf("nothing to render: the request and the response are both empty")
	}

	var reqLines, resLines []highlight.LineTokens
	var reqLayout, resLayout httpLayout
	var err error
	if hasReq {
		reqLines, reqLayout, err = tokenizeHTTP(cfg.Request, theme.Chroma)
		if err != nil {
			return nil, fmt.Errorf("tokenize request: %w", err)
		}
		reqLines = burpColourMessage(reqLines)
		cfg.warnUnreadableBody("request", cfg.Request, reqLayout)
	}
	if hasRes {
		resLines, resLayout, err = tokenizeHTTP(cfg.Response, theme.Chroma)
		if err != nil {
			return nil, fmt.Errorf("tokenize response: %w", err)
		}
		resLines = burpColourMessage(resLines)
		cfg.warnUnreadableBody("response", cfg.Response, resLayout)
	}

	// Literals are located in the full capture, before any trimming: --context
	// has to be able to center the view on a match. Present panes are searched
	// together so a literal that appears on each side is reported once, with a
	// count that reflects what the analyst will actually see.
	var msgs [][]highlight.LineTokens
	var sideOf []int // index into msgs -> 0 request, 1 response
	if hasReq {
		msgs = append(msgs, reqLines)
		sideOf = append(sideOf, 0)
	}
	if hasRes {
		msgs = append(msgs, resLines)
		sideOf = append(sideOf, 1)
	}

	reqHL, resHL := cfg.RequestHL, cfg.ResponseHL
	marked, stats := highlight.FindMarks(msgs, cfg.Mark, cfg.MaxMarks)
	reMarked, reStats := highlight.FindRegexMarks(msgs, cfg.MarkRe, cfg.MaxMarks)
	stats = append(stats, reStats...)
	for i, side := range sideOf {
		m := marked[i].Merge(reMarked[i])
		if side == 0 {
			reqHL = reqHL.Merge(m)
		} else {
			resHL = resHL.Merge(m)
		}
	}

	// Redact literals are hidden, not marked — found the same way but painted
	// over rather than boxed in red. They never merge into HighlightLines.
	redact, redactStats := highlight.FindMarks(msgs, cfg.Redact, 0)
	redactRe, redactReStats := highlight.FindRegexMarks(msgs, cfg.RedactRe, 0)
	var reqRedact, resRedact highlight.Marks
	for i, side := range sideOf {
		m := highlight.KeepSpans(redact[i].Merge(redactRe[i]), cfg.RedactKeep)
		if side == 0 {
			reqRedact = reqRedact.Merge(m)
		} else {
			resRedact = resRedact.Merge(m)
		}
	}
	for _, st := range redactStats {
		if st.Matches == 0 {
			cfg.warnf("--redact %q matched nothing", st.Literal)
		}
	}
	for _, st := range redactReStats {
		if st.Matches == 0 {
			cfg.warnf("--redact-re %q matched nothing", st.Literal)
		}
	}
	if cfg.RedactKeep > 0 && reqRedact == nil && resRedact == nil {
		for _, st := range append(redactStats, redactReStats...) {
			if st.Matches > 0 {
				cfg.warnf("--redact-keep %d leaves nothing hidden", cfg.RedactKeep)
				break
			}
		}
	}

	// Counted before the trim: the view has to be able to state its own extent.
	totalReq, totalRes := len(reqLines), len(resLines)
	// A 3000-line body is taller than any viewer can open, so trim to what
	// matters. Gap bands report what was left out inside the view; the footnote
	// reports what the view itself left out.
	// --context replaces a panel's range only when that panel has a mark to
	// center on, so the warning is decided here, per panel: the caller cannot
	// know before tokenizing whether a --mark literal landed on this side.
	reqSpec, resSpec := cfg.RequestLines, cfg.ResponseLines
	if s := highlight.ContextSpec(reqHL, cfg.Context); s != nil {
		if reqSpec != nil {
			cfg.warnf("--context overrides --nr; ignoring it")
		}
		reqSpec = s
	}
	if s := highlight.ContextSpec(resHL, cfg.Context); s != nil {
		if resSpec != nil {
			cfg.warnf("--context overrides --ns; ignoring it")
		}
		resSpec = s
	}
	reqLines = highlight.FilterLines(reqLines, reqSpec)
	resLines = highlight.FilterLines(resLines, resSpec)
	if hasReq && len(reqLines) == 0 {
		return nil, fmt.Errorf("--nr matched no request line")
	}
	if hasRes && len(resLines) == 0 {
		return nil, fmt.Errorf("--ns matched no response line")
	}

	// A trimmed view that drops the first line drops the one line that names
	// where the message went — the surface of the finding. The crop note says
	// the count; this warning says what specifically is missing.
	if hasReq && highlight.Extent(reqLines).First > 1 {
		cfg.warnf("the request line is not in the view; the image does not show where the request was sent")
	}
	// After an interim block (100 Continue, a proxy's "Connection established")
	// the status that matters is the final one, and line 1 is not it.
	if hasRes && !showsLine(resLines, resLayout.status+1) {
		cfg.warnf("the status line is not in the view; the image does not show the response's status")
	}

	if cfg.Facts != nil {
		if hasReq {
			cfg.Facts.Request = PanelFacts{ViewExtent: highlight.Extent(reqLines), Total: totalReq, Present: true}
		}
		if hasRes {
			cfg.Facts.Response = PanelFacts{ViewExtent: highlight.Extent(resLines), Total: totalRes, Present: true}
		}
		cfg.Facts.Marks = stats
	}

	// Tabs read the whole capture, not the trimmed view: in Burp an editor tab
	// reflects the message it holds, not how far you scrolled.
	var pane *image.NRGBA
	redacted := 0
	if hasReq && hasRes {
		req := &Config{
			Lines: reqLines, Title: "Request", HighlightLines: reqHL, RedactRegions: reqRedact,
			Theme: theme, Wrap: true, WrapWidth: wrapCols, HTTPMode: true,
			httpTabs: burpTabs("Request", cfg.Request), Warn: cfg.Warn,
		}
		res := &Config{
			Lines: resLines, Title: "Response", HighlightLines: resHL, RedactRegions: resRedact,
			Theme: theme, Wrap: true, WrapWidth: wrapCols, HTTPMode: true,
			httpTabs: burpTabs("Response", cfg.Response), Warn: cfg.Warn,
		}

		reqImg, resImg, err := renderPair(req, res)
		if err != nil {
			return nil, err
		}
		redacted = req.Redacted + res.Redacted
		// No window chrome here: a Burp screenshot is a flush pane, and evidence
		// that looks like it came straight out of Burp reads as more credible.
		pane = composeHTTP(reqImg, resImg, httpDividerMargin, theme.UI)
	} else {
		// One side only: the splitter would frame a pane whose other half does
		// not exist. Render the message that is evidence.
		single := &Config{
			Theme: theme, Wrap: true, WrapWidth: wrapCols, HTTPMode: true, Warn: cfg.Warn,
		}
		if hasReq {
			single.Title, single.Lines, single.HighlightLines, single.RedactRegions = "Request", reqLines, reqHL, reqRedact
			single.httpTabs = burpTabs("Request", cfg.Request)
		} else {
			single.Title, single.Lines, single.HighlightLines, single.RedactRegions = "Response", resLines, resHL, resRedact
			single.httpTabs = burpTabs("Response", cfg.Response)
		}
		img, err := Render(single)
		if err != nil {
			return nil, err
		}
		redacted = single.Redacted
		pane = img
	}

	var notes []string
	if hasReq {
		notes = append(notes, cropNote("Request", reqLines, totalReq))
	}
	if hasRes {
		notes = append(notes, cropNote("Response", resLines, totalRes))
	}
	notes = append(notes, redactNote(redacted, cfg.RedactAsked))
	if cfg.Facts != nil {
		cfg.Facts.Redacted = redacted
	}
	note := joinNotes(notes...)
	if cfg.Caption == "" && note == "" {
		return pane, nil
	}
	m, err := metricsFor()
	if err != nil {
		return nil, err
	}
	pane = withFootnote(pane, note, theme.UI, m)
	return withCaption(pane, cfg.Caption, theme.UI, m), nil
}

// showsLine reports whether source line n is in a filtered view.
func showsLine(lines []highlight.LineTokens, n int) bool {
	for _, lt := range lines {
		if !lt.IsGap && lt.LineNum == n {
			return true
		}
	}
	return false
}

// renderPair renders both panels at the same height, so the shorter one's
// gutter and separator run the full length instead of stopping mid-image.
func renderPair(a, b *Config) (*image.NRGBA, *image.NRGBA, error) {
	imgA, err := Render(a)
	if err != nil {
		return nil, nil, fmt.Errorf("render %s: %w", a.Title, err)
	}
	imgB, err := Render(b)
	if err != nil {
		return nil, nil, fmt.Errorf("render %s: %w", b.Title, err)
	}
	h := max(imgA.Bounds().Dy(), imgB.Bounds().Dy())
	return extendDown(imgA, h), extendDown(imgB, h), nil
}

// extendDown grows a rendered HTTP panel to height h by repeating its last
// row. That row is bottom padding — background plus the gutter rule, nothing
// else — so the result is the same image Render draws with MinHeight set,
// without laying out, wrapping and drawing every line a second time.
func extendDown(src *image.NRGBA, h int) *image.NRGBA {
	b := src.Bounds()
	if b.Dy() >= h {
		return src
	}
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), h))
	copy(out.Pix, src.Pix[src.PixOffset(b.Min.X, b.Min.Y):])
	last := out.Pix[(b.Dy()-1)*out.Stride : b.Dy()*out.Stride]
	for y := b.Dy(); y < h; y++ {
		copy(out.Pix[y*out.Stride:], last)
	}
	return out
}

// composeHTTP places two equal-height panels side by side with a divider. gap
// columns of background separate the left panel from the splitter: the
// response pane is the long one, and a mark that lands at the end of a request
// line would otherwise touch the splitter.
func composeHTTP(left, right *image.NRGBA, gap int, ui UIColors) *image.NRGBA {
	lw, rw := left.Bounds().Dx(), right.Bounds().Dx()
	h := max(left.Bounds().Dy(), right.Bounds().Dy())
	gap = max(gap, 0)
	split := lw + gap

	out := image.NewNRGBA(image.Rect(0, 0, split+httpDividerW+rw, h))
	draw.Draw(out, image.Rect(0, 0, lw, h), left, left.Bounds().Min, draw.Src)
	if gap > 0 {
		// The gap continues the left panel: its header band, then the editor
		// background below it.
		headerH := min(burpTitleH+burpTabRowH, h)
		fillRect(out, image.Rect(lw, 0, split, h), ui.Bg)
		fillRect(out, image.Rect(lw, 0, split, headerH-burpTabBorderH), ui.TabBarBg)
		fillRect(out, image.Rect(lw, headerH-burpTabBorderH, split, headerH), ui.TabBorder)
	}
	// Burp's splitter is two hairlines — a light edge and a darker one behind
	// it — not a filled band, and it runs the full height of the window.
	fillRect(out, image.Rect(split, 0, split+1, h), ui.Splitter)
	if httpDividerW > 1 {
		fillRect(out, image.Rect(split+1, 0, split+httpDividerW, h), ui.SplitterShade)
	}
	draw.Draw(out, image.Rect(split+httpDividerW, 0, split+httpDividerW+rw, h), right, right.Bounds().Min, draw.Src)

	// Hairline around the pane, like Burp's split container.
	drawRectOutline(out, out.Bounds(), ui.TabBorder, 1)

	return out
}

type burpTab struct {
	label    string
	disabled bool // Burp dims a view that cannot render the current body
}

// jwtRe matches a compact JWS: two base64url segments that both decode to a
// JSON object, so both start with the "eyJ" that encodes `{"`.
var jwtRe = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}`)

// htmlRe recognises markup in a body that declares no type. The opening tag
// needs a delimiter after it, or prose like "not <html-ish" counts as markup.
var htmlRe = regexp.MustCompile(`(?i)<!doctype[ \t]+html|<html[ \t\r\n>]`)

// isHTML reports whether a response body is markup Burp could render. Burp
// decides from the declared Content-Type, as burpBodyTokens does: a JSON API
// that returns markup inside a string is JSON, and a browser does not render
// it — for XSS evidence an enabled Render tab would claim otherwise. Only a
// message without a declared type has its body sniffed.
func isHTML(message string) bool {
	lines := messageLines(message)
	layout := layoutHTTP(lines)
	if layout.contentType != "" {
		switch mediaType(layout.contentType) {
		case "text/html", "application/xhtml+xml":
			return true
		}
		return false
	}
	return htmlRe.MatchString(strings.Join(lines[layout.body:], "\n"))
}

// burpTabs is the view strip Burp shows above a message editor. Burp adds an
// editor tab per detected payload type, so a request bearing a JWT gets one.
func burpTabs(panel, message string) []burpTab {
	tabs := []burpTab{{label: "Pretty"}, {label: "Raw"}, {label: "Hex"}}
	if panel == "Response" {
		// Render only works on markup, so Burp greys it out for anything else.
		return append(tabs, burpTab{label: "Render", disabled: !isHTML(message)})
	}
	if jwtRe.MatchString(message) {
		tabs = append(tabs, burpTab{label: "JSON Web Token"})
	}
	return tabs
}

// drawHTTPPanelHeader draws Burp Suite's message-editor header: a title strip
// over a row of view tabs, with the active tab underlined in the accent color.
// The two rows share one background colour and one continuous band — Burp does
// not separate them — and the band is closed by a 2px border.
func drawHTTPPanelHeader(img *image.NRGBA, l *layout, cfg *Config, ui UIColors) {
	fillRect(img, image.Rect(0, 0, l.totalWidth, burpTitleH+burpTabRowH), ui.TabBarBg)

	// Title, in the larger face and bold: in Burp it outranks the tab labels.
	drawStringBold(img, l.face, cfg.Title, burpTitleX,
		l.baselineIn(0, burpTitleH-1), ui.TabText)

	// Tab row. Same colour as the title band, so no divider is drawn between
	// them; only the active tab's underline sits in here.
	tabTop := burpTitleH
	tabBottom := tabTop + burpTabRowH - burpTabBorderH

	// Burp's tab labels sit at the top of the row, not centred in it: the
	// underline claims the bottom strip, and the gap between the two is what
	// makes the underline read as a tab indicator instead of a text decoration.
	baseline := tabTop + l.uiAscent - 3
	// The first label aligns under the title, as in Burp's own pane.
	x := burpTitleX - burpTabLabelPad
	for i, tab := range cfg.httpTabs {
		w := textWidth(l.uiFace, tab.label)
		fg := ui.LineNumFg
		if tab.disabled {
			fg = ui.GapFg
		}
		if i == 0 { // Pretty is the active view
			fg = ui.TabText
			fillRect(img, image.Rect(x, tabBottom-burpTabUnderlineH,
				x+w+burpTabLabelPad+burpTabUnderlinePad, tabBottom), ui.TabAccent)
		}
		drawString(img, l.uiFace, tab.label, x+burpTabLabelPad, baseline, fg)
		x += w + burpTabPitch
	}

	// The band's closing border, below the underline.
	fillRect(img, image.Rect(0, tabBottom, l.totalWidth, tabBottom+burpTabBorderH), ui.TabBorder)
}
