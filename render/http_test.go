package render

import (
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

	out := composeHTTP(a, b, BurpTheme.UI)
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
	"Request", "Response", "RequestHL", "ResponseHL", "Mark", "MaxMarks", "Caption",
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
		"Content-Type: application/json\n" +
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
	got := burpBodyTokens(`    "user":"test",`, lexed)
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
