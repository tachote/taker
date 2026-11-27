package render

import (
	"bytes"
	"fmt"
	"image/color"
	"reflect"
	"strings"
	"testing"

	"github.com/tachote/taker/highlight"
)

const (
	testReq = "POST /login HTTP/1.1\nHost: app.example.com\nContent-Type: application/json\n\n" +
		`{"user":"admin","pass":"hunter2"}` + "\n"
	testRes = "HTTP/1.1 200 OK\nContent-Type: application/json\nSet-Cookie: sid=abc; HttpOnly\n\n" +
		`{"token":"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.sig","admin":true}` + "\n"
)

func TestRenderHTTPComposition(t *testing.T) {
	img, err := RenderHTTP(&HTTPConfig{
		Request:    testReq,
		Response:   testRes,
		RequestHL:  highlight.Marks{5: {{}}},
		ResponseHL: highlight.Marks{5: {{}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		t.Fatalf("empty image %v", b)
	}
	// The pane is flush: no chrome margin, so the top-left pixel belongs to the
	// panel's title strip, not to a surrounding background.
	if got := img.NRGBAAt(1, 1); got != BurpTheme.UI.TabBarBg {
		t.Errorf("top-left pixel = %v, want title strip %v", got, BurpTheme.UI.TabBarBg)
	}
}

// A short panel is re-rendered to the taller one's height, so the gutter and
// background run the full length instead of stopping mid-image.
func TestRenderPairEqualisesHeight(t *testing.T) {
	short := &Config{
		Lines: tokenizeFor(t, "GET / HTTP/1.1\n"), Title: "Request",
		Theme: BurpTheme, Wrap: true, WrapWidth: 80, HTTPMode: true,
	}
	tall := &Config{
		Lines: tokenizeFor(t, strings.Repeat("X-Pad: y\n", 30)), Title: "Response",
		Theme: BurpTheme, Wrap: true, WrapWidth: 80, HTTPMode: true,
	}

	a, b, err := renderPair(short, tall)
	if err != nil {
		t.Fatal(err)
	}
	if a.Bounds().Dy() != b.Bounds().Dy() {
		t.Fatalf("panel heights differ: %d vs %d", a.Bounds().Dy(), b.Bounds().Dy())
	}

	out := composeHTTP(a, b, 0, BurpTheme.UI)
	wantW := a.Bounds().Dx() + httpDividerW + b.Bounds().Dx()
	if out.Bounds().Dx() != wantW {
		t.Errorf("composed width %d, want %d", out.Bounds().Dx(), wantW)
	}
	if out.Bounds().Dy() != a.Bounds().Dy() {
		t.Errorf("composed height %d, want %d", out.Bounds().Dy(), a.Bounds().Dy())
	}

	// The short panel's gutter must reach the bottom of the composed pane.
	deep := out.Bounds().Dy() - 4
	if got := out.NRGBAAt(2, deep); got != BurpTheme.UI.Bg {
		t.Errorf("short panel at y=%d is %v, want panel background %v", deep, got, BurpTheme.UI.Bg)
	}
}

// Line specs must reach the HTTP panels, or a long body has no way to be
// trimmed and produces an image no viewer can open.
func TestRenderHTTPLineSpecs(t *testing.T) {
	long := "HTTP/1.1 200 OK\n\n" + strings.Repeat("padding line\n", 400)

	full, err := RenderHTTP(&HTTPConfig{Request: testReq, Response: long})
	if err != nil {
		t.Fatal(err)
	}
	trimmed, err := RenderHTTP(&HTTPConfig{
		Request: testReq, Response: long,
		ResponseLines: []int{1, 2, 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	if trimmed.Bounds().Dy() >= full.Bounds().Dy() {
		t.Errorf("trimmed pane (%d) should be shorter than the full one (%d)",
			trimmed.Bounds().Dy(), full.Bounds().Dy())
	}

	if _, err := RenderHTTP(&HTTPConfig{
		Request: testReq, Response: testRes,
		ResponseLines: []int{9000},
	}); err == nil {
		t.Error("a spec matching no response line should fail, not emit a blank panel")
	}
}

// Refusing is the point: a body this long renders taller than any viewer opens,
// and allocating it first costs about a gigabyte.
func TestRenderRefusesUnviewableHeight(t *testing.T) {
	huge := "HTTP/1.1 200 OK\n\n" + strings.Repeat("line\n", 1200)
	_, err := RenderHTTP(&HTTPConfig{Request: testReq, Response: huge})
	if err == nil {
		t.Fatal("expected a height error")
	}
	for _, want := range []string{"decode", "--context"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q so the fix is obvious", err, want)
		}
	}
}

// A width below the floor would put one character per row.
func TestWrapWidthFloor(t *testing.T) {
	img, err := RenderHTTP(&HTTPConfig{
		Request: testReq, Response: testRes, WrapWidth: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	wide, err := RenderHTTP(&HTTPConfig{
		Request: testReq, Response: testRes, WrapWidth: minContentChars,
	})
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dy() != wide.Bounds().Dy() {
		t.Errorf("width 1 should clamp to %d: got height %d, want %d",
			minContentChars, img.Bounds().Dy(), wide.Bounds().Dy())
	}
}

func tokenizeFor(t *testing.T, src string) []highlight.LineTokens {
	t.Helper()
	lines, err := highlight.Tokenize(src, "http", "x.http", BurpTheme.Chroma)
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

// HTTP evidence is Burp-coloured by construction, not by configuration. A
// caller cannot ask for another palette: a Dracula-tinted pane labelled
// "Request / Response / Pretty / Raw / Hex" is a screenshot of an application
// that does not exist, which is a false claim to put in a report.
func TestHTTPIsAlwaysBurp(t *testing.T) {
	if reflect.TypeOf(HTTPConfig{}).NumField() != len(httpConfigFields) {
		t.Fatalf("HTTPConfig changed shape; if a theme knob came back, remove it")
	}
	for _, name := range httpConfigFields {
		if _, ok := reflect.TypeOf(HTTPConfig{}).FieldByName(name); !ok {
			t.Errorf("HTTPConfig lost field %q", name)
		}
	}
	if _, ok := reflect.TypeOf(HTTPConfig{}).FieldByName("Theme"); ok {
		t.Error("HTTPConfig must not expose a theme")
	}

	img, err := RenderHTTP(&HTTPConfig{Request: testReq, Response: testRes})
	if err != nil {
		t.Fatal(err)
	}
	// Title strip, so the Burp palette really reached the panels.
	if got := img.NRGBAAt(1, 1); got != BurpTheme.UI.TabBarBg {
		t.Errorf("title strip = %v, want Burp's %v", got, BurpTheme.UI.TabBarBg)
	}
	if BurpTheme == nil || BurpTheme.Chroma == nil {
		t.Fatal("BurpTheme is not wired up")
	}
}

var httpConfigFields = []string{
	"Request", "Response", "RequestHL", "ResponseHL", "Mark", "MarkRe", "MaxMarks",
	"Redact", "RedactRe", "RedactKeep", "RedactAsked", "Caption",
	"RequestLines", "ResponseLines", "Context", "WrapWidth",
	// An out-parameter, not a knob: the renderer writes what it drew into it so
	// the machine-readable report and the image's own footnote cannot disagree.
	// It cannot change a pixel.
	"Facts",
	// A channel for the render's own warnings, so the CLI can put them in the
	// report instead of leaving them on a stderr nobody reads. Not a knob either.
	"Warn",
}

// --mark and --context have to compose: the literal is what you have, and the
// window around it is what makes a 3000-line body readable. The marks are only
// known after tokenizing, so resolving the context outside the renderer left
// this combination silently doing nothing.
func TestMarkAndContextCompose(t *testing.T) {
	var body strings.Builder
	body.WriteString("HTTP/1.1 200 OK\n\n")
	for i := 1; i <= 400; i++ {
		fmt.Fprintf(&body, "filler line %d\n", i)
	}
	body.WriteString("needle=SECRET_VALUE\n")

	full, err := RenderHTTP(&HTTPConfig{Request: testReq, Response: body.String()})
	if err != nil {
		t.Fatal(err)
	}
	windowed, err := RenderHTTP(&HTTPConfig{
		Request: testReq, Response: body.String(),
		Mark: []string{"SECRET_VALUE"}, Context: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if windowed.Bounds().Dy() >= full.Bounds().Dy() {
		t.Errorf("context did not trim: %d vs full %d", windowed.Bounds().Dy(), full.Bounds().Dy())
	}

	// A literal that is nowhere leaves the pane untrimmed rather than empty.
	none, err := RenderHTTP(&HTTPConfig{
		Request: testReq, Response: body.String(),
		Mark: []string{"NOT_PRESENT"}, Context: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if none.Bounds().Dy() != full.Bounds().Dy() {
		t.Errorf("an unmatched mark should not trim anything: %d vs %d",
			none.Bounds().Dy(), full.Bounds().Dy())
	}
}

// Burp's Pretty view colours more than a lexer parses: a header's name and its
// value differ, a cookie's name is separated from its data, and a form body
// separates field names from values. Losing that turns the panel into flat
// monochrome, which is the state this test exists to prevent.
func TestBurpColourMessage(t *testing.T) {
	msg := "POST /doLogin HTTP/1.1\n" +
		"Host: demo.testfire.net\n" +
		"Set-Cookie: SID=68B9FA79D23F48A269262E622C58280D; Path=/; HttpOnly\n" +
		"Set-Cookie: Data=0DAwMDAwZkNVcnBvcmF0ZXR4M; Path=/\n" +
		"Content-Type: application/x-www-form-urlencoded\n" +
		"\n" +
		"uid=admin&passw=x"
	lines, err := highlight.Tokenize(msg, "http", "x.http", BurpStyle)
	if err != nil {
		t.Fatal(err)
	}
	got := burpColourMessage(lines)

	tokenColour := func(line int, text string) color.NRGBA {
		for _, tok := range got[line-1].Tokens {
			if tok.Text == text {
				return color.NRGBA{tok.R, tok.G, tok.B, 0xff}
			}
		}
		t.Fatalf("line %d has no token %q", line, text)
		return color.NRGBA{}
	}

	// Request line: one colour, no structure.
	if line := got[0]; len(line.Tokens) != 1 || line.Tokens[0].R != burpLineColour.R {
		t.Errorf("request line = %+v, want a single line-coloured run", line.Tokens)
	}
	// Header name and value are different colours.
	if c := tokenColour(2, "Host:"); c != burpLineColour {
		t.Errorf("header name = %v, want %v", c, burpLineColour)
	}
	if c := tokenColour(2, " demo.testfire.net"); c != burpHeaderValue {
		t.Errorf("header value = %v, want %v", c, burpHeaderValue)
	}
	// A cookie: name separated, hex data left plain, attributes plain.
	if c := tokenColour(3, "SID"); c != burpParamName {
		t.Errorf("cookie name = %v, want %v", c, burpParamName)
	}
	// One class for every cookie value, hex-shaped or not: Burp's theme defines
	// a single cookieValue colour (#A5C35B). An earlier version of this test
	// demanded grey for the hex-shaped one, because a screenshot had been read
	// on the wrong row — the grey belonged to the Server header above it.
	if c := tokenColour(3, "68B9FA79D23F48A269262E622C58280D"); c != burpParamValue {
		t.Errorf("cookie data = %v, want Burp's cookieValue %v", c, burpParamValue)
	}
	if c := tokenColour(3, "; Path=/; HttpOnly"); c != burpHeaderValue {
		t.Errorf("cookie attributes = %v, want %v", c, burpHeaderValue)
	}
	// Data that is not an opaque hex blob is painted as data.
	if c := tokenColour(4, "0DAwMDAwZkNVcnBvcmF0ZXR4M"); c != burpParamValue {
		t.Errorf("cookie data = %v, want %v", c, burpParamValue)
	}
	// A form body: names, values and separators each get their own colour.
	if c := tokenColour(7, "uid"); c != burpParamName {
		t.Errorf("field name = %v, want %v", c, burpParamName)
	}
	if c := tokenColour(7, "admin"); c != burpParamValue {
		t.Errorf("field value = %v, want %v", c, burpParamValue)
	}
	if c := tokenColour(7, "&"); c != burpLineColour {
		t.Errorf("field separator = %v, want %v", c, burpLineColour)
	}
}

// An HTTP/2 pseudo header's name starts with a colon of its own. Cutting at the
// first colon makes the name empty and paints the rest of the line as a value,
// which is how the whole header block of an HTTP/2 message gets flattened.
func TestBurpColourPseudoHeaders(t *testing.T) {
	msg := ":method: GET\n:authority: api.test\n:path: /v1/users?q=1\naccept: */*\n\n"
	lines, err := highlight.Tokenize(msg, "http", "x.http", BurpStyle)
	if err != nil {
		t.Fatal(err)
	}
	got := burpColourMessage(lines)

	want := map[int]struct{ name, value string }{
		1: {":method:", " GET"},
		2: {":authority:", " api.test"},
		3: {":path:", " /v1/users?q=1"},
	}
	for line, w := range want {
		toks := got[line-1].Tokens
		if len(toks) != 2 {
			t.Fatalf("line %d: got %d tokens %+v, want name and value", line, len(toks), toks)
		}
		if toks[0].Text != w.name || toks[0].R != burpLineColour.R {
			t.Errorf("line %d name = %q (%v), want %q in the line colour", line, toks[0].Text, toks[0], w.name)
		}
		if toks[1].Text != w.value || toks[1].R != burpHeaderValue.R {
			t.Errorf("line %d value = %q (%v), want %q in the value colour", line, toks[1].Text, toks[1], w.value)
		}
	}
}

// A body the lexer does understand must survive the repaint untouched: JSON
// carries its own colours, and flattening it to the header palette would be a
// regression dressed up as consistency.
// Measured on a capture of Burp's pretty view with a json body: the key came
// out gold, the value green, and both the colon and the comma plain text. The
// lexer calls them operators, which in this palette means the HTTP blue.
func TestBurpJSONFramingIsPlainText(t *testing.T) {
	lexed := []highlight.Token{
		colourToken("    ", burpLineColour),
		colourToken(`"user"`, burpNameTag),
		colourToken(":", burpLineColour),
		colourToken(`"test"`, burpString),
		colourToken(",", burpLineColour),
	}
	got := burpBodyTokens(`    "user":"test",`, lexed, "application/json")
	want := []highlight.Token{lexed[0], lexed[1], colourToken(":", burpBodyText), lexed[3], colourToken(",", burpBodyText)}
	if len(got) != len(want) {
		t.Fatalf("got %d tokens, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("token %d = %q%v, want %q%v", i, got[i].Text, got[i], want[i].Text, want[i])
		}
	}
}

func TestBurpColourLeavesJSONContentAlone(t *testing.T) {
	msg := "HTTP/1.1 200 OK\nContent-Type: application/json\n\n" +
		`{"user":"admin","pass":"hunter2"}`
	lines, err := highlight.Tokenize(msg, "http", "x.http", BurpStyle)
	if err != nil {
		t.Fatal(err)
	}
	before := append([]highlight.Token(nil), lines[3].Tokens...)
	got := burpColourMessage(lines)

	// The body's *content* keeps the colors the lexer gave it: keys, strings,
	// numbers, booleans. The pass only repaints the framing (braces, colon,
	// comma), which the test above pins against the capture.
	if len(got[3].Tokens) != len(before) {
		t.Fatalf("token count changed: %d -> %d", len(before), len(got[3].Tokens))
	}
	for i, tok := range got[3].Tokens {
		if isFraming(tok.Text) {
			if c := (color.NRGBA{tok.R, tok.G, tok.B, 0xff}); c != burpBodyText {
				t.Errorf("framing %q = %v, want body text %v", tok.Text, c, burpBodyText)
			}
			continue
		}
		if !reflect.DeepEqual(tok, before[i]) {
			t.Errorf("content token %q was repainted: %+v -> %+v", tok.Text, before[i], tok)
		}
	}
	// The header above it did get repainted, so the pass is not a no-op.
	if got[1].Tokens[0].R != burpLineColour.R {
		t.Errorf("header name was not repainted: %+v", got[1].Tokens)
	}
}

// A json body whose strings hold "a=1&b=2" satisfies the form heuristic —
// every &-field carries an '=' — but the declared Content-Type says json, and
// Burp decides the body view by type, not by shape. Painting it as a form was
// how a redirect URL inside a string ended up in param colours.
func TestBurpColourJSONWithAmpersandsIsNotAForm(t *testing.T) {
	msg := "HTTP/1.1 200 OK\nContent-Type: application/json; charset=utf-8\n\n" +
		`{"url":"a=1&b=2","ok":true}`
	lines, err := highlight.Tokenize(msg, "http", "x.http", BurpStyle)
	if err != nil {
		t.Fatal(err)
	}
	got := burpColourMessage(lines)

	for _, tok := range got[3].Tokens {
		c := color.NRGBA{tok.R, tok.G, tok.B, 0xff}
		if c == burpParamName || c == burpParamValue {
			t.Errorf("token %q was painted as a form field — the json body was treated as a form", tok.Text)
		}
	}
}

// Without a declared Content-Type the body view falls back to the line's
// shape — a bare "a=1&b=2" capture has no other signal, and Burp's param
// colours are still the right read of it.
func TestBurpColourNoContentTypeKeepsFormGuess(t *testing.T) {
	msg := "POST /x HTTP/1.1\nHost: a.test\n\nuid=admin&passw=x"
	lines, err := highlight.Tokenize(msg, "http", "x.http", BurpStyle)
	if err != nil {
		t.Fatal(err)
	}
	got := burpColourMessage(lines)

	var sawName bool
	for _, tok := range got[3].Tokens {
		if tok.Text == "uid" && (color.NRGBA{tok.R, tok.G, tok.B, 0xff}) == burpParamName {
			sawName = true
		}
	}
	if !sawName {
		t.Errorf("body was not painted as a form: %+v", got[3].Tokens)
	}
}

// A request line is only a request line inside the header block. In the body
// — a nested request captured by an SSRF, a pasted log — "GET /x HTTP/1.1" is
// text and must not be flattened to the single line colour.
func TestBurpColourBodyLineThatLooksLikeARequest(t *testing.T) {
	msg := "HTTP/1.1 200 OK\nContent-Type: text/plain\n\nGET /internal HTTP/1.1"
	lines, err := highlight.Tokenize(msg, "http", "x.http", BurpStyle)
	if err != nil {
		t.Fatal(err)
	}
	got := burpColourMessage(lines)

	if len(got[3].Tokens) == 1 && got[3].Tokens[0].R == burpLineColour.R {
		t.Errorf("body line was painted as a start line: %+v", got[3].Tokens)
	}
	// The real start line still gets the flat treatment.
	if len(got[0].Tokens) != 1 || got[0].Tokens[0].R != burpLineColour.R {
		t.Errorf("status line lost its colour: %+v", got[0].Tokens)
	}
}

// A request Cookie carries several name=value pairs and Burp colours each
// one; Set-Cookie carries one pair plus attributes, which stay plain.
func TestBurpCookiePairsEachGetColours(t *testing.T) {
	colours := func(msg string, line int) []color.NRGBA {
		lines, err := highlight.Tokenize(msg, "http", "x.http", BurpStyle)
		if err != nil {
			t.Fatal(err)
		}
		got := burpColourMessage(lines)
		var out []color.NRGBA
		for _, tok := range got[line].Tokens {
			out = append(out, color.NRGBA{tok.R, tok.G, tok.B, 0xff})
		}
		return out
	}
	count := func(cs []color.NRGBA, c color.NRGBA) int {
		n := 0
		for _, x := range cs {
			if x == c {
				n++
			}
		}
		return n
	}

	req := "GET / HTTP/1.1\nHost: a\nCookie: session=abc; csrftoken=xyz; theme=dark\n\n"
	if got := count(colours(req, 2), burpParamName); got != 3 {
		t.Errorf("cookie pairs: %d param names coloured, want 3", got)
	}

	res := "HTTP/1.1 200 OK\nSet-Cookie: session=abc; HttpOnly; SameSite=None\n\n"
	cs := colours(res, 1)
	if got := count(cs, burpParamName); got != 1 {
		t.Errorf("set-cookie: %d param names coloured, want 1 (attributes stay plain)", got)
	}
	// SameSite=None must not slip into the pair colours.
	if count(cs, burpParamValue) != 1 {
		t.Errorf("set-cookie attributes picked up param colours: %v", cs)
	}
}

// The report has to describe the image the reader gets, so the renderer hands
// out what it actually drew instead of letting the caller recompute the trim.
// Marks are resolved inside the render, which is why their counts live here too.
func TestRenderHTTPReportsWhatItDrew(t *testing.T) {
	var body strings.Builder
	body.WriteString("HTTP/1.1 200 OK\n\n")
	for i := 1; i <= 200; i++ {
		fmt.Fprintf(&body, "body line %d\n", i)
	}
	facts := &HTTPFacts{}
	_, err := RenderHTTP(&HTTPConfig{
		Request:  testReq,
		Response: body.String(),
		Mark:     []string{"body line 100"},
		Context:  2,
		Facts:    facts,
	})
	if err != nil {
		t.Fatal(err)
	}

	// The message is a status line, a blank line and 200 body lines, so the
	// mark lands on message line 102 and ±2 context gives 100-104.
	if facts.Response.Shown != 5 {
		t.Errorf("response view shows %d lines, want 5 around the mark", facts.Response.Shown)
	}
	if facts.Response.First != 100 || facts.Response.Last != 104 {
		t.Errorf("response extent = %d-%d, want 100-104", facts.Response.First, facts.Response.Last)
	}
	if facts.Response.Total != 202 {
		t.Errorf("response total = %d, want the whole message's 202 lines", facts.Response.Total)
	}
	if facts.Response.First > 102 || facts.Response.Last < 102 {
		t.Error("the marked line is outside the extent the report claims")
	}
	if len(facts.Marks) != 1 || facts.Marks[0].Matches != 1 {
		t.Fatalf("marks = %+v, want one literal with one match", facts.Marks)
	}
	// The request carries no match, so nothing was trimmed out of it: the
	// extent has to say the view is the whole message, not that it is empty.
	if facts.Request.Shown != facts.Request.Total || facts.Request.Shown == 0 {
		t.Errorf("request extent = %+v, want the whole untrimmed message", facts.Request)
	}
}

// A single-side render produces one panel — a data-leak finding is the
// response alone, and the splitter would frame a pane that does not exist.
func TestHTTPSingleSide(t *testing.T) {
	facts := &HTTPFacts{}
	img, err := RenderHTTP(&HTTPConfig{Request: testReq, Facts: facts})
	if err != nil {
		t.Fatal(err)
	}
	if !facts.Request.Present || facts.Response.Present {
		t.Fatalf("present = req:%v res:%v, want req only", facts.Request.Present, facts.Response.Present)
	}
	pair, err := RenderHTTP(&HTTPConfig{Request: testReq, Response: testRes})
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() >= pair.Bounds().Dx() {
		t.Errorf("single panel %dpx >= pair %dpx; it is framing an absent pane",
			img.Bounds().Dx(), pair.Bounds().Dx())
	}

	facts2 := &HTTPFacts{}
	if _, err := RenderHTTP(&HTTPConfig{Response: testRes, Facts: facts2}); err != nil {
		t.Fatal(err)
	}
	if facts2.Request.Present || !facts2.Response.Present {
		t.Errorf("response-only present = req:%v res:%v", facts2.Request.Present, facts2.Response.Present)
	}
}

// An empty config renders nothing — guarding against a blank PNG at exit 0.
func TestHTTPEmptyConfigFails(t *testing.T) {
	if _, err := RenderHTTP(&HTTPConfig{}); err == nil {
		t.Fatal("empty request and response should refuse, not draw a blank pane")
	}
}

// A trim that drops line 1 drops the line naming where the message went: the
// crop note counts lines, this warning says what specifically is missing.
func TestHTTPStartLineWarning(t *testing.T) {
	spec, err := highlight.ParseLineSpec("3-5")
	if err != nil {
		t.Fatal(err)
	}
	var warned []string
	_, err = RenderHTTP(&HTTPConfig{
		Request: testReq, Response: testRes,
		RequestLines: spec,
		Warn:         func(m string) { warned = append(warned, m) },
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range warned {
		if strings.Contains(w, "request line") {
			found = true
		}
	}
	if !found {
		t.Errorf("no request-line warning in %v", warned)
	}
}

// --redact on HTTP hides the literal in whichever panel holds it and declares
// the count the report will carry.
func TestHTTPRedact(t *testing.T) {
	facts := &HTTPFacts{}
	_, err := RenderHTTP(&HTTPConfig{
		Request: testReq, Response: testRes,
		Redact: []string{"abc"}, Facts: facts,
	})
	if err != nil {
		t.Fatal(err)
	}
	if facts.Redacted == 0 {
		t.Error("no redaction box drawn for a literal in the capture")
	}
}

// extendDown stands in for a second Render with MinHeight. It is only correct
// while the two produce the same pixels, so pin that.
func TestExtendDownMatchesMinHeight(t *testing.T) {
	cfg := func() *Config {
		return &Config{
			Lines: tokenizeFor(t, "GET / HTTP/1.1\nHost: a\n"), Title: "Request",
			Theme: BurpTheme, Wrap: true, WrapWidth: 80, HTTPMode: true,
			HighlightLines: highlight.Marks{2: nil},
		}
	}
	base, err := Render(cfg())
	if err != nil {
		t.Fatal(err)
	}
	h := base.Bounds().Dy() + 137
	want := cfg()
	want.MinHeight = h
	wantImg, err := Render(want)
	if err != nil {
		t.Fatal(err)
	}
	got := extendDown(base, h)
	if !got.Bounds().Eq(wantImg.Bounds()) || !bytes.Equal(got.Pix, wantImg.Pix) {
		t.Fatal("extendDown differs from Render with MinHeight")
	}
}

// colourHTTP runs a message through the same two passes RenderHTTP does.
func colourHTTP(t *testing.T, msg string) []highlight.LineTokens {
	t.Helper()
	lines, _, err := tokenizeHTTP(msg, BurpStyle)
	if err != nil {
		t.Fatal(err)
	}
	return burpColourMessage(lines)
}

// hasToken reports whether line (1-based) holds a token with exactly this text
// in this colour.
func hasToken(lines []highlight.LineTokens, line int, text string, c color.NRGBA) bool {
	if line < 1 || line > len(lines) {
		return false
	}
	for _, tok := range lines[line-1].Tokens {
		if tok.Text == text && (color.NRGBA{tok.R, tok.G, tok.B, 0xff}) == c {
			return true
		}
	}
	return false
}

// chroma's http lexer gives up on the first line it does not know — an HTTP/2
// pseudo header, a WebDAV verb, a header with an empty value — and everything
// after it, the body included, comes out as one flat run. The header lines hide
// that because they are repainted anyway; the body is where the evidence is.
func TestBodyColouredWhenHTTPLexerRejectsALine(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		line int
		key  string
	}{
		{"http2 post", ":method: POST\n:path: /api/users\n:authority: a.test\ncontent-type: application/json\n\n" +
			`{"role":"admin","id":7}` + "\n", 6, `"role"`},
		{"webdav verb", "PROPFIND /dav HTTP/1.1\nHost: a\nContent-Type: application/xml\n\n<a>1</a>\n", 5, "<a>"},
		{"empty header value", "POST /x HTTP/1.1\r\nHost: a\r\nX-Empty:\r\nContent-Type: application/json\r\n\r\n" +
			`{"role":"admin"}`, 6, `"role"`},
		{"structured suffix", "HTTP/1.1 200 OK\nContent-Type: application/problem+json\n\n" +
			`{"title":"x"}`, 4, `"title"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := colourHTTP(t, tc.msg)
			if len(got) != tc.line {
				t.Fatalf("got %d lines, want %d: the line numbers must match the capture", len(got), tc.line)
			}
			if !hasToken(got, tc.line, tc.key, burpNameTag) {
				t.Errorf("body key %q is not tag-coloured: %+v", tc.key, got[tc.line-1].Tokens)
			}
		})
	}
}

// curl -i through a proxy prints the proxy's "200 Connection established"
// block before the real response, and a large upload gets "100 Continue". The
// real response's headers are still headers, and its body is typed by its own
// Content-Type, not by the interim block's (which has none).
func TestInterimStatusBlocks(t *testing.T) {
	proxy := "HTTP/1.1 200 Connection established\r\n\r\nHTTP/2 200\r\ncontent-type: application/json\r\n" +
		"set-cookie: sid=XYZ; Path=/; HttpOnly\r\n\r\n" + `{"token":"secret","user":"admin"}` + "\r\n"
	got := colourHTTP(t, proxy)
	if len(got) != 7 {
		t.Fatalf("got %d lines, want 7", len(got))
	}
	if toks := got[2].Tokens; len(toks) != 1 || toks[0].R != burpLineColour.R {
		t.Errorf("final status line = %+v, want one line-coloured run", toks)
	}
	if !hasToken(got, 5, "set-cookie:", burpLineColour) || !hasToken(got, 5, "sid", burpParamName) {
		t.Errorf("final headers painted as body: %+v", got[4].Tokens)
	}
	if !hasToken(got, 7, `"token"`, burpNameTag) {
		t.Errorf("final body is not json-coloured: %+v", got[6].Tokens)
	}

	// 103 Early Hints carries headers of its own; the final block still wins.
	cont := "HTTP/1.1 100 Continue\n\nHTTP/1.1 103 Early Hints\nLink: </a.css>; rel=preload\n\n" +
		"HTTP/1.1 201 Created\nContent-Type: application/x-www-form-urlencoded\n\nid=7&owner=victim\n"
	got = colourHTTP(t, cont)
	if len(got) != 9 {
		t.Fatalf("got %d lines, want 9", len(got))
	}
	if !hasToken(got, 4, "Link:", burpLineColour) {
		t.Errorf("early-hints header not a header: %+v", got[3].Tokens)
	}
	if !hasToken(got, 7, "Content-Type:", burpLineColour) {
		t.Errorf("final header not a header: %+v", got[6].Tokens)
	}
	if !hasToken(got, 9, "owner", burpParamName) {
		t.Errorf("final form body not form-coloured: %+v", got[8].Tokens)
	}

	// A final response whose body happens to start with a status line is still
	// body: only the interim shapes open a new header block.
	echo := "HTTP/1.1 200 OK\nContent-Type: text/plain\n\nHTTP/1.1 200 OK\nX-Inner: 1\n"
	got = colourHTTP(t, echo)
	if hasToken(got, 5, "X-Inner:", burpLineColour) {
		t.Errorf("an echoed response in the body was painted as headers: %+v", got[4].Tokens)
	}
}

// The status-line warning is about the status the image shows. After an
// interim block, that is the final status line, not line 1.
func TestStatusLineWarningAfterInterimBlock(t *testing.T) {
	proxy := "HTTP/1.1 200 Connection established\n\nHTTP/2 200\ncontent-type: application/json\n\n{\"a\":1}\n"
	for _, tc := range []struct {
		spec string
		warn bool
	}{{"3-6", false}, {"4-6", true}, {"1-2,4-6", true}} {
		spec, err := highlight.ParseLineSpec(tc.spec)
		if err != nil {
			t.Fatal(err)
		}
		var warned bool
		_, err = RenderHTTP(&HTTPConfig{
			Response: proxy, ResponseLines: spec,
			Warn: func(m string) { warned = warned || strings.Contains(m, "status line") },
		})
		if err != nil {
			t.Fatal(err)
		}
		if warned != tc.warn {
			t.Errorf("--ns %s: status-line warning = %v, want %v", tc.spec, warned, tc.warn)
		}
	}
}

// --context only replaces a panel's line range when that panel has a mark to
// center on. A panel without marks keeps its --nr/--ns, so warning that the
// range was ignored would contradict the image.
func TestContextOverrideWarningIsExact(t *testing.T) {
	reqSpec, err := highlight.ParseLineSpec("1-2")
	if err != nil {
		t.Fatal(err)
	}
	run := func(mark string) ([]string, *HTTPFacts) {
		var warned []string
		facts := &HTTPFacts{}
		_, err := RenderHTTP(&HTTPConfig{
			Request: testReq, Response: testRes,
			Mark: []string{mark}, Context: 1,
			RequestLines: reqSpec, ResponseLines: reqSpec,
			Warn:  func(m string) { warned = append(warned, m) },
			Facts: facts,
		})
		if err != nil {
			t.Fatal(err)
		}
		return warned, facts
	}
	has := func(ws []string, s string) bool {
		for _, w := range ws {
			if strings.Contains(w, s) {
				return true
			}
		}
		return false
	}

	// The mark is in the response only: --nr is applied, --ns is overridden.
	warned, facts := run(`"admin":true`)
	if has(warned, "overrides --nr") {
		t.Errorf("warned that --nr was ignored, but the request view is %d-%d: %v",
			facts.Request.First, facts.Request.Last, warned)
	}
	if facts.Request.First != 1 || facts.Request.Last != 2 {
		t.Errorf("request extent = %d-%d, want --nr's 1-2", facts.Request.First, facts.Request.Last)
	}
	if !has(warned, "--context overrides --ns; ignoring it") {
		t.Errorf("no --ns override warning in %v", warned)
	}

	// The mark is in the request only: the reverse.
	warned, _ = run("hunter2")
	if !has(warned, "--context overrides --nr; ignoring it") || has(warned, "overrides --ns") {
		t.Errorf("warnings = %v, want only the --nr override", warned)
	}
}

// Without a Content-Type the form guess is all there is, so it has to refuse
// lines that merely contain one '=': markup, script, prose and json. A form
// field's name is URL-encoded, so it never holds a raw space or markup.
func TestLooksFormEncoded(t *testing.T) {
	for line, want := range map[string]bool{
		"uid=admin&passw=x":             true,
		"a=1":                           true,
		"a=&b=":                         true,
		"q=hello world&x=%3Cy%3E":       true,
		`<a href="/admin">Admin</a>`:    false,
		"var isAdmin = true;":           false,
		"total = 5 items":               false,
		`{"next":"/x?a=1"}`:             false,
		"=orphan":                       false,
		"a=1&":                          false,
		"":                              false,
		`<input name="q" value="1">`:    false,
		"title=<b>x</b>&mode=preview":   true,
		"just some prose with no equal": false,
	} {
		if got := looksFormEncoded(line); got != want {
			t.Errorf("looksFormEncoded(%q) = %v, want %v", line, got, want)
		}
	}
}

// A capture saved without decoding draws as rows of replacement glyphs, which
// reads as "the server sent garbage" rather than "the analyst kept the wire
// bytes". The image stays faithful; the warning says what it shows.
func TestUnreadableBodyWarns(t *testing.T) {
	warnings := func(cfg *HTTPConfig) []string {
		var warned []string
		cfg.Warn = func(m string) { warned = append(warned, m) }
		if _, err := RenderHTTP(cfg); err != nil {
			t.Fatal(err)
		}
		return warned
	}
	has := func(ws []string, s string) bool {
		for _, w := range ws {
			if strings.Contains(w, s) {
				return true
			}
		}
		return false
	}
	gz := "HTTP/1.1 200 OK\nContent-Type: application/json\nContent-Encoding: gzip\n\n\x1f\x8b\x08\x00\x00\x00\x00\x00\x00\x03\xab\xae\x05\x00\x43\xbf\xa6\xa3\x02\x00\x00\x00"
	if w := warnings(&HTTPConfig{Request: testReq, Response: gz}); !has(w, "response body is compressed (Content-Encoding: gzip)") {
		t.Errorf("no gzip warning in %v", w)
	}
	// An already-replaced capture (the CLI's input normaliser re-encodes
	// invalid bytes as U+FFFD) is the same evidence problem.
	bin := "HTTP/1.1 200 OK\nContent-Type: application/octet-stream\n\nPK\x03\x04\x00\x00��"
	if w := warnings(&HTTPConfig{Response: bin}); !has(w, "response body is binary") {
		t.Errorf("no binary warning in %v", w)
	}
	reqBin := "POST /up HTTP/1.1\nHost: a\n\n\xff\xfe\x00raw"
	if w := warnings(&HTTPConfig{Request: reqBin}); !has(w, "request body is binary") {
		t.Errorf("no request binary warning in %v", w)
	}
	// A declared encoding on a header-only response, and a plain text body,
	// are not warned about.
	empty := "HTTP/1.1 304 Not Modified\nContent-Encoding: gzip\n\n"
	if w := warnings(&HTTPConfig{Request: testReq, Response: empty}); has(w, "body is") {
		t.Errorf("warned about an empty body: %v", w)
	}
	if w := warnings(&HTTPConfig{Request: testReq, Response: testRes}); has(w, "body is") {
		t.Errorf("warned about a text body: %v", w)
	}
}
