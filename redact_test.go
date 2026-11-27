package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withStdin hands fn a stdin that holds data and then ends, the way
// `cat file | taker` does.
func withStdin(t *testing.T, data string, fn func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(data); err != nil {
		t.Fatal(err)
	}
	w.Close()
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old; r.Close() }()
	fn()
}

// A secret spliced by an ANSI escape is one value on a terminal. Piped on
// stdin it has to be normalized like a file argument is, or the box misses it
// and the image shows the secret under a "matched nothing" warning.
func TestSnapStdinStripsANSIBeforeRedacting(t *testing.T) {
	const secret = "Sup3rD4ta!"
	out := filepath.Join(t.TempDir(), "s.png")
	var code int
	var rep Report
	withStdin(t, "pass=Sup3r\x1b[31mD4ta!\x1b[0m\nok\n", func() {
		code, rep, _ = runJSON(t, func(r *reporter) int {
			return runSnap(snapCmd(SnapCmd{Output: out, JSON: true, Redact: []string{secret}}), r)
		})
	})
	if code != exitOK {
		t.Fatalf("snap = %d, report %+v", code, rep)
	}
	if rep.Redactions != 1 {
		t.Errorf("redactions = %d, want 1 (the spliced secret boxed)", rep.Redactions)
	}
	if hasWarning(rep, "matched nothing") {
		t.Errorf("warnings = %v, the needle should have matched", rep.Warnings)
	}
	if !hasWarning(rep, "stripped 2 ANSI escape sequences") {
		t.Errorf("warnings = %v, want the declared ANSI strip", rep.Warnings)
	}
	// Same bytes as the file path would hash: normalized, then masked.
	if len(rep.SHA256) != 1 || rep.SHA256[0].Sum != sha256Hex([]byte("pass=<redacted>\nok\n")) {
		t.Errorf("sha256 = %+v, want the normalized, masked input's sum", rep.SHA256)
	}
	if strings.Contains(rep.Command, secret) {
		t.Errorf("command leaks the secret: %s", rep.Command)
	}
}
