package render

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tachote/taker/highlight"
)

// benchCapture is a realistic proxy capture: a short authenticated request and
// a long JSON response, the shape most HTTP evidence takes.
func benchCapture(bodyLines int) (req, res string) {
	req = "GET /api/v1/users?page=1 HTTP/1.1\nHost: api.example.com\n" +
		"Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiI3In0.x8Kq\n" +
		"Cookie: session=abc123; theme=dark\nAccept: application/json\n\n"
	var b strings.Builder
	b.WriteString("HTTP/1.1 200 OK\nContent-Type: application/json\nCache-Control: no-store\n\n[\n")
	for i := 0; i < bodyLines; i++ {
		fmt.Fprintf(&b, `  {"id":%d,"email":"user%d@example.com","role":"user","tenant_id":"acme"},`+"\n", i, i)
	}
	b.WriteString("]\n")
	return req, b.String()
}

func benchmarkHTTP(b *testing.B, bodyLines int) {
	req, res := benchCapture(bodyLines)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := RenderHTTP(&HTTPConfig{
			Request: req, Response: res,
			Mark: []string{"user7@example.com"}, Caption: "bench",
			// The large capture is past the height warning on purpose; printing
			// it on every iteration buries the numbers.
			Warn: func(string) {},
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRenderHTTPSmall(b *testing.B) { benchmarkHTTP(b, 5) }
func BenchmarkRenderHTTPLarge(b *testing.B) { benchmarkHTTP(b, 300) }

// benchSnapLines is a 200-line source file: long enough that per-pixel work
// over the whole canvas (chrome, shadow, strips) dominates, which is where a
// regression like a shadow that blends every pixel under the window shows up.
func benchSnapLines(b *testing.B) []highlight.LineTokens {
	b.Helper()
	var src strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&src, "func handler%d(w http.ResponseWriter, r *http.Request) { token := r.Header.Get(\"X-Token\") }\n", i)
	}
	lines, err := highlight.Tokenize(src.String(), "go", "x.go", DefaultTheme.Chroma)
	if err != nil {
		b.Fatal(err)
	}
	return lines
}

func benchmarkSnap(b *testing.B, chrome bool) {
	lines := benchSnapLines(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cfg := &Config{Lines: lines, Title: "x.go", Theme: DefaultTheme, noWarn: true,
			HighlightLines: highlight.Marks{50: {{}}, 51: {{}}}}
		if chrome {
			cfg.WindowChrome, cfg.Shadow = true, true
			cfg.Caption = "the handler trusts the client-supplied token"
			cfg.SourceRef = "src/handlers/x.go @ 9f344fad66cf"
			cfg.TotalLines = 400
		}
		if _, err := Render(cfg); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRenderSnapPlain(b *testing.B)  { benchmarkSnap(b, false) }
func BenchmarkRenderSnapChrome(b *testing.B) { benchmarkSnap(b, true) }
