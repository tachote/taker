package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeInput puts body in dir/name and returns the path.
func writeInput(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func warningsWith(rep Report, sub string) int {
	n := 0
	for _, w := range rep.Warnings {
		if strings.Contains(w, sub) {
			n++
		}
	}
	return n
}

// An unknown --lang used to be dropped without a word, so a typo rendered the
// code as plain text and looked like a tool limitation.
func TestSnapWarnsOnUnknownLang(t *testing.T) {
	dir := t.TempDir()
	file := writeInput(t, dir, "a.go", "package a\nfunc F() {}\n")
	for _, tc := range []struct {
		lang string
		warn bool
	}{{"pyhton", true}, {"python", false}, {"", false}} {
		code, rep, _ := runJSON(t, func(r *reporter) int {
			return runSnap(snapCmd(SnapCmd{File: file, Lang: tc.lang, DryRun: true, JSON: true}), r)
		})
		if code != exitOK {
			t.Fatalf("--lang %q = %d", tc.lang, code)
		}
		got := hasWarning(rep, "is not a language taker knows")
		if got != tc.warn {
			t.Errorf("--lang %q: warned = %v, want %v (%v)", tc.lang, got, tc.warn, rep.Warnings)
		}
		// The warning says what was used instead: the .go name picked Go.
		if tc.warn && !hasWarning(rep, "highlighted as Go instead") {
			t.Errorf("--lang %q: warnings %v do not name the fallback lexer", tc.lang, rep.Warnings)
		}
	}
}

// Invalid bytes draw as U+FFFD, a real glyph, so the image loses their values
// silently. The warning counts them on the raw bytes and names the line in the
// numbering the gutter shows.
func TestInvalidUTF8IsWarned(t *testing.T) {
	dir := t.TempDir()
	bad := "a = 1\nb = \"\xff\xfe\"\n"

	t.Run("snap", func(t *testing.T) {
		file := writeInput(t, dir, "a.py", bad)
		_, rep, _ := runJSON(t, func(r *reporter) int {
			return runSnap(snapCmd(SnapCmd{File: file, FirstLine: 40, DryRun: true, JSON: true}), r)
		})
		if !hasWarning(rep, "2 invalid UTF-8 byte(s) in "+file+" (first on line 41)") {
			t.Errorf("warnings = %v, want 2 bytes first on line 41", rep.Warnings)
		}
		clean := writeInput(t, dir, "c.py", "a = 'é'\n")
		_, rep, _ = runJSON(t, func(r *reporter) int {
			return runSnap(snapCmd(SnapCmd{File: clean, DryRun: true, JSON: true}), r)
		})
		if hasWarning(rep, "invalid UTF-8") {
			t.Errorf("valid UTF-8 warned: %v", rep.Warnings)
		}
	})

	t.Run("term", func(t *testing.T) {
		file := writeInput(t, dir, "t.log", "$ cat x\n"+bad)
		_, rep, _ := runJSON(t, func(r *reporter) int {
			return runTerm(termCmd(TermCmd{File: file, DryRun: true, JSON: true}), r)
		})
		if !hasWarning(rep, "(first on line 3)") {
			t.Errorf("warnings = %v, want the invalid bytes on line 3", rep.Warnings)
		}
	})

	t.Run("http", func(t *testing.T) {
		// One bad byte in a header, two in the body: the header gets this
		// warning, the body keeps the renderer's own binary-body warning
		// and is not counted twice.
		req := writeInput(t, dir, "r.http", "POST /a HTTP/1.1\r\nHost: x\xff.test\r\n\r\nk=\xfe\xfd\r\n")
		_, rep, _ := runJSON(t, func(r *reporter) int {
			return runHTTP(&HTTPCmd{Request: req, DryRun: true, JSON: true}, r)
		})
		if !hasWarning(rep, "1 invalid UTF-8 byte(s) in the request's start line or headers (first on line 2)") {
			t.Errorf("warnings = %v, want the header byte", rep.Warnings)
		}
		if warningsWith(rep, "invalid UTF-8 byte(s)") != 1 || !hasWarning(rep, "request body is binary") {
			t.Errorf("warnings = %v, want one header warning plus the body's binary warning", rep.Warnings)
		}
		body := writeInput(t, dir, "b.http", "POST /a HTTP/1.1\r\nHost: x.test\r\n\r\nk=\xfe\r\n")
		_, rep, _ = runJSON(t, func(r *reporter) int {
			return runHTTP(&HTTPCmd{Request: body, DryRun: true, JSON: true}, r)
		})
		if hasWarning(rep, "invalid UTF-8 byte(s)") {
			t.Errorf("a body-only bad byte got the header warning: %v", rep.Warnings)
		}
	})
}

// An empty or whitespace-only file rendered a blank pane at exit 0; http and
// term already refuse empty input, and the README lists "empty" under exit 3.
func TestSnapRefusesEmptyInput(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"empty.go": "", "ws.go": "\n\n  \t\n", "bom.go": "\ufeff\n",
	} {
		file := writeInput(t, dir, name, body)
		out := filepath.Join(dir, name+".png")
		code, rep, _ := runJSON(t, func(r *reporter) int {
			return runSnap(snapCmd(SnapCmd{File: file, Output: out, JSON: true}), r)
		})
		if code != exitInput || rep.Error == nil || !strings.Contains(rep.Error.Message, "is empty") {
			t.Errorf("%s = %d (%v), want exit 3 'is empty'", name, code, rep.Error)
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Errorf("%s: a PNG was written for empty input", name)
		}
	}
	// stdin at EOF is empty, not slow: exit 3, not the 300ms usage error.
	code, stdout, _ := runTaker(t, dir, "-o", filepath.Join(dir, "s.png"), "--json")
	if code != exitInput || !strings.Contains(stdout, "stdin is empty") {
		t.Errorf("empty stdin = %d\n%s\nwant exit 3 'stdin is empty'", code, stdout)
	}
}
