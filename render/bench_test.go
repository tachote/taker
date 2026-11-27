package render

import (
	"fmt"
	"strings"
	"testing"
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
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRenderHTTPSmall(b *testing.B) { benchmarkHTTP(b, 5) }
func BenchmarkRenderHTTPLarge(b *testing.B) { benchmarkHTTP(b, 300) }
