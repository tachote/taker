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

// term and exec hash what they matched and drew, like snap and http. The raw
// file still holds the ANSI-spliced secret in clear, so its sum — which is
// also `sha256sum <file>` — would confirm a guess at the secret.
func TestTermHashCoversTheNormalizedTranscript(t *testing.T) {
	raw := "pass=Sup3r\x1b[31mD4ta!\x1b[0m\nok\n"
	dir, file := writeTranscript(t, raw)
	code, rep, _ := runJSON(t, func(r *reporter) int {
		return runTerm(termCmd(TermCmd{
			File: file, Output: filepath.Join(dir, "o.png"), JSON: true, DryRun: true,
			Redact: []string{"Sup3rD4ta!"},
		}), r)
	})
	if code != exitOK || rep.Redactions != 1 {
		t.Fatalf("term = %d, redactions = %d, want the spliced secret boxed", code, rep.Redactions)
	}
	if len(rep.SHA256) != 1 {
		t.Fatalf("sha256 = %+v", rep.SHA256)
	}
	if got := rep.SHA256[0].Sum; got == sha256Hex([]byte(raw)) {
		t.Fatal("the transcript hash is sha256(raw file): a guessing oracle for the redacted secret")
	}
	if got, want := rep.SHA256[0].Sum, sha256Hex([]byte("pass=<redacted>\nok\n")); got != want {
		t.Errorf("sha256 = %s, want %s (normalized, then masked)", got, want)
	}
}

// exec hashes its transcript the same way: escapes stripped before masking.
func TestExecHashCoversTheNormalizedTranscript(t *testing.T) {
	code, rep, _ := runJSON(t, func(r *reporter) int {
		return runExec(&ExecCmd{
			Shell:  `printf 'pass=Sup3r\033[31mD4ta!\033[0m\n'`,
			Redact: []string{"Sup3rD4ta!"}, DryRun: true, Timeout: 5,
			Theme: "vscode-dark", Chrome: "window", WrapWidth: 80, JSON: true,
		}, r)
	})
	if code != exitOK || rep.Redactions != 1 {
		t.Fatalf("exec = %d, redactions = %d", code, rep.Redactions)
	}
	cmdLine := `$ printf 'pass=Sup3r\033[31mD4ta!\033[0m\n'` + "\n"
	if got, want := rep.SHA256[0].Sum, sha256Hex([]byte(cmdLine+"pass=<redacted>\n")); got != want {
		t.Errorf("sha256 = %s, want %s (normalized, then masked)", got, want)
	}
}

// -o naming an input file renames a PNG over the evidence it was made from.
// Every mode refuses it, including through a symlink, and the input survives.
func TestOutputMustNotOverwriteTheInput(t *testing.T) {
	const req = "GET / HTTP/1.1\nHost: a\n\n"
	const res = "HTTP/1.1 200 OK\nContent-Type: text/plain\n\nok\n"
	for _, tc := range []struct {
		name  string
		files map[string]string
		link  [2]string // symlink name -> target, when set
		args  []string
	}{
		{"http -r", map[string]string{"r.http": req}, [2]string{}, []string{"http", "-r", "r.http", "-o", "r.http"}},
		{"http -s", map[string]string{"r.http": req, "s.http": res}, [2]string{}, []string{"http", "-r", "r.http", "-s", "s.http", "-o", "s.http"}},
		{"http -f", map[string]string{"c.http": req + res}, [2]string{}, []string{"http", "-f", "c.http", "-o", "./c.http"}},
		{"term", map[string]string{"t.log": "$ id\nuid=0\n"}, [2]string{}, []string{"term", "t.log", "-o", "t.log"}},
		{"snap via symlink", map[string]string{"b.py": "print(1)\n"}, [2]string{"blink.py", "b.py"}, []string{"blink.py", "-o", "b.py"}},
		{"term via symlink", map[string]string{"t.log": "$ id\n"}, [2]string{"tlink.log", "t.log"}, []string{"term", "tlink.log", "-o", "t.log"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.link[0] != "" {
				if err := os.Symlink(tc.link[1], filepath.Join(dir, tc.link[0])); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			code, stdout, _ := runTaker(t, dir, append(tc.args, "--json")...)
			if code != exitUsage || !strings.Contains(stdout, "-o must differ from the input file") {
				t.Errorf("exit = %d, want 2 with the overwrite refusal\n%s", code, stdout)
			}
			for name, body := range tc.files {
				got, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil || string(got) != body {
					t.Errorf("%s was changed: %q, %v", name, got, err)
				}
			}
		})
	}
}
