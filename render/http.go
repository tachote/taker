package render

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"regexp"
	"strings"

	"github.com/tachote/taker/highlight"
	"golang.org/x/image/font"
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
	burpIconGap         = 32
	burpIconPlateW      = 38
	burpIconPlateH      = 36
	burpIconPlateR      = 6
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
	inHeaders := true
	contentType := ""
	for i := range lines {
		lt := &lines[i]
		if lt.IsGap {
			continue
		}
		text := lineText(*lt)
		switch {
		case inHeaders && isStartLine(text):
			// Request line or status line: one colour, no structure. Only a
			// line inside the header block can be one — a body line like
			// "GET /x HTTP/1.1" (a nested request, a pasted log) is body text.
			lt.Tokens = []highlight.Token{colourToken(text, burpLineColour)}
		case inHeaders && strings.TrimSpace(text) == "":
			inHeaders = false // the separator between headers and body
		case inHeaders:
			if name, value, ok := splitHeader(text); ok && contentType == "" && isContentTypeHeader(name) {
				contentType = strings.ToLower(strings.TrimSpace(value))
			}
			lt.Tokens = burpHeaderTokens(text)
		default:
			lt.Tokens = burpBodyTokens(text, lt.Tokens, contentType)
		}
	}
	return lines
}

// isContentTypeHeader names the one header whose value decides how the body is
// painted.
func isContentTypeHeader(name string) bool {
	return strings.EqualFold(strings.Trim(strings.TrimSpace(name), ":"), "content-type")
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
func burpBodyTokens(text string, lexed []highlight.Token, contentType string) []highlight.Token {
	if isFormContentType(contentType) || (contentType == "" && looksFormEncoded(text)) {
		return formFieldTokens(text)
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

// looksFormEncoded reports k=v&k=v. Every field has to carry an '=', so a line
// of prose with one '=' in it, or a JSON object, is not treated as a form.
func looksFormEncoded(s string) bool {
	if s == "" {
		return false
	}
	for _, field := range strings.Split(s, "&") {
		if !strings.Contains(field, "=") {
			return false
		}
	}
	return true
}

// RenderHTTP renders a side-by-side HTTP request/response evidence image.
//
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
	var err error
	if hasReq {
		reqLines, err = highlight.Tokenize(cfg.Request, "http", "request.http", theme.Chroma)
		if err != nil {
			return nil, fmt.Errorf("tokenize request: %w", err)
		}
		reqLines = burpColourMessage(reqLines)
	}
	if hasRes {
		resLines, err = highlight.Tokenize(cfg.Response, "http", "response.http", theme.Chroma)
		if err != nil {
			return nil, fmt.Errorf("tokenize response: %w", err)
		}
		resLines = burpColourMessage(resLines)
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
	reqSpec, resSpec := cfg.RequestLines, cfg.ResponseLines
	if s := highlight.ContextSpec(reqHL, cfg.Context); s != nil {
		reqSpec = s
	}
	if s := highlight.ContextSpec(resHL, cfg.Context); s != nil {
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
	if hasRes && highlight.Extent(resLines).First > 1 {
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
		// Pad the request pane before composing: the response pane is the long
		// one, and a mark that lands at the end of a request line would
		// otherwise touch the splitter.
		reqImg = padRight(reqImg, httpDividerMargin, theme.UI.Bg)
		pane = composeHTTP(reqImg, resImg, theme.UI)
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
	if imgA.Bounds().Dy() < h {
		a.MinHeight, a.noWarn = h, true
		if imgA, err = Render(a); err != nil {
			return nil, nil, fmt.Errorf("render %s: %w", a.Title, err)
		}
	}
	if imgB.Bounds().Dy() < h {
		b.MinHeight, b.noWarn = h, true
		if imgB, err = Render(b); err != nil {
			return nil, nil, fmt.Errorf("render %s: %w", b.Title, err)
		}
	}
	return imgA, imgB, nil
}

// padRight widens an image by n columns of bg on the right, so a pane can be
// given breathing room before it is composed against its neighbour.
func padRight(src *image.NRGBA, n int, bg color.NRGBA) *image.NRGBA {
	if n <= 0 {
		return src
	}
	b := src.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx()+n, b.Dy()))
	fillRect(out, out.Bounds(), bg)
	draw.Draw(out, image.Rect(0, 0, b.Dx(), b.Dy()), src, b.Min, draw.Src)
	return out
}

// composeHTTP places two equal-height panels side by side with a divider.
func composeHTTP(left, right *image.NRGBA, ui UIColors) *image.NRGBA {
	lw, rw := left.Bounds().Dx(), right.Bounds().Dx()
	h := max(left.Bounds().Dy(), right.Bounds().Dy())

	out := image.NewNRGBA(image.Rect(0, 0, lw+httpDividerW+rw, h))
	fillRect(out, out.Bounds(), ui.Bg)

	draw.Draw(out, image.Rect(0, 0, lw, h), left, left.Bounds().Min, draw.Src)
	// Burp's splitter is two hairlines — a light edge and a darker one behind
	// it — not a filled band, and it runs the full height of the window.
	fillRect(out, image.Rect(lw, 0, lw+1, h), ui.Splitter)
	if httpDividerW > 1 {
		fillRect(out, image.Rect(lw+1, 0, lw+httpDividerW, h), ui.SplitterShade)
	}
	draw.Draw(out, image.Rect(lw+httpDividerW, 0, lw+httpDividerW+rw, h), right, right.Bounds().Min, draw.Src)

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

// htmlRe recognises markup Burp's Render view can display. The opening tag
// needs a delimiter after it, or prose like "not <html-ish" counts as markup.
var htmlRe = regexp.MustCompile(`(?i)content-type:[ \t]*text/html|<!doctype[ \t]+html|<html[ \t\r\n>]`)

// isHTML reports whether a response body is markup Burp could render.
func isHTML(message string) bool { return htmlRe.MatchString(message) }

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

	// Burp's right-hand controls, right-aligned: hide-headers, pretty-print
	// (lit when active), show-non-printing, and the editor menu.
	ix := l.totalWidth - burpTitleX - burpControlsWidth(l.metrics)
	iconBaseline := tabTop + (burpTabRowH-burpTabBorderH+l.iconAscent)/2 - 2
	for _, c := range burpControls {
		if c.text != "" {
			// The \n toggle shares the icons' optical centre — it is one of
			// the controls, not a tab label, so it does not sit high like one.
			drawString(img, l.uiFace, c.text, ix, iconBaseline+l.uiAscent-l.iconAscent, ui.LineNumFg)
			ix += textWidth(l.uiFace, c.text) + burpIconGap
			continue
		}
		fg := ui.LineNumFg
		if c.active {
			// Burp marks the active toggle with a filled blue plate under a white
			// glyph, not by brightening the glyph. The plate is still muted: in
			// evidence the only loud thing should be the highlight box marking
			// the finding.
			cy := iconBaseline - l.iconAscent/2
			plate := image.Rect(ix-burpIconPlateW/2+1, cy-burpIconPlateH/2,
				ix+burpIconPlateW/2+1, cy+burpIconPlateH/2)
			fillRect(img, plate, ui.ControlActive)
			roundCorners(img, plate, burpIconPlateR, ui.TabBarBg, ui.ControlActive)
			fg = rgb(0xff, 0xff, 0xff)
		}
		drawString(img, l.iconFace, string(c.glyph), ix, iconBaseline, fg)
		ix += burpGlyphW(l.iconFace, c.glyph) + burpIconGap
	}
}

// burpGlyphW is a control glyph's own advance. iconW is the file-icon slot —
// the wordmark glyphs it now measures are wider than a toolbar glyph, and
// spacing the strip on it left every icon adrift.
func burpGlyphW(face font.Face, glyph rune) int {
	adv, ok := face.GlyphAdvance(glyph)
	if !ok {
		return burpIconPlateW
	}
	return int(adv >> 6)
}

// burpControl is one item in the editor's right-hand control strip. Burp mixes
// glyphs with a literal "\n" label, so an item is either a glyph or text.
type burpControl struct {
	glyph  rune
	text   string
	active bool
}

var burpControls = []burpControl{
	{glyph: '\ue668'},               // hide headers (eye with slash)
	{glyph: '\uf039', active: true}, // pretty-print, shown as the active toggle
	{text: `\n`},                    // show non-printing characters
	{glyph: '\uf0c9'},               // editor menu
}

func burpControlsWidth(m *metrics) int {
	w := 0
	for _, c := range burpControls {
		if c.text != "" {
			w += textWidth(m.uiFace, c.text) + burpIconGap
			continue
		}
		w += max(burpGlyphW(m.iconFace, c.glyph), burpIconPlateW) + burpIconGap
	}
	return w
}
