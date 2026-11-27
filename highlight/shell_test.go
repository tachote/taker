package highlight

import (
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2/styles"
)

func TestShellLineKeepsTextAndColoursRoles(t *testing.T) {
	style := styles.Get("monokai")
	cases := []string{
		`curl -s -H "A: $T" https://x/y | jq .r && FOO=bar ls -la --color=never 2>/dev/null; (cd /tmp || exit 1) > o.txt`,
		`for f in *.txt; do wc -l "$f"; done`,
		`echo 'it'\''s' # comment`,
		``,
	}
	for _, c := range cases {
		toks, ok := ShellLine(c, style)
		if !ok {
			t.Fatalf("ShellLine(%q) not ok", c)
		}
		var b strings.Builder
		for _, tk := range toks {
			b.WriteString(tk.Text)
		}
		if b.String() != c {
			t.Errorf("text changed: got %q want %q", b.String(), c)
		}
	}

	toks, _ := ShellLine(`FOO=bar grep -rn x | wc -l`, style)
	colour := map[string][3]uint8{}
	for _, tk := range toks {
		colour[tk.Text] = [3]uint8{tk.R, tk.G, tk.B}
	}
	if colour["grep"] != colour["wc"] {
		t.Errorf("commands in two positions got different colours: grep %v wc %v", colour["grep"], colour["wc"])
	}
	if colour["grep"] == colour["x"] {
		t.Errorf("command and argument share a colour %v", colour["x"])
	}
	if colour["-rn"] != colour["-l"] || colour["-rn"] == colour["x"] {
		t.Errorf("flags not coloured as flags: -rn %v -l %v arg %v", colour["-rn"], colour["-l"], colour["x"])
	}
	if colour["bar"] == colour["grep"] {
		t.Errorf("the value of FOO= was coloured as a command")
	}

	toks, _ = ShellLine(`for f in a; do ls; done`, style)
	for _, tk := range toks {
		if tk.Text == "f" && [3]uint8{tk.R, tk.G, tk.B} == colour["grep"] {
			t.Errorf("the loop variable was coloured as a command")
		}
	}
	if _, ok := ShellLine("a\nb", style); ok {
		t.Errorf("a two-line input must not be ok")
	}
}
