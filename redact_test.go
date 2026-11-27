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

// A decomposed accent is a combining mark, and deleting it changed names in
// the evidence ("Jos\u00e9" drawn as "Jose"). NFC composes it first; only a mark
// with no precomposed form is still removed — and counted.
func TestNormalizeZeroWidthComposesAccents(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		removed  int
	}{
		{"name = \"Jose\u0301\"", "name = \"Jos\u00e9\"", 0},
		{"n\u0303", "\u00f1", 0},
		{"plain ascii", "plain ascii", 0},
		{"q\u0307", "q", 1},                  // no precomposed q-dot-above
		{"to\u200bken\ufeff", "token", 2},    // zero-width splices still go
		{"Jose\u0301\u200b", "Jos\u00e9", 1}, // composed, then the ZWSP removed
	} {
		got, n := normalizeZeroWidth([]byte(tc.in))
		if string(got) != tc.want || n != tc.removed {
			t.Errorf("normalizeZeroWidth(%q) = %q, %d; want %q, %d", tc.in, got, n, tc.want, tc.removed)
		}
	}
}

// End to end: a decomposed name renders composed, with no removal warning,
// and a needle typed precomposed matches it.
func TestSnapKeepsDecomposedAccents(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "uni.py")
	if err := os.WriteFile(file, []byte("name = \"Jose\u0301\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, rep, _ := runJSON(t, func(r *reporter) int {
		return runSnap(snapCmd(SnapCmd{File: file, Output: filepath.Join(dir, "o.png"), JSON: true, Mark: []string{"Jos\u00e9"}}), r)
	})
	if code != exitOK {
		t.Fatalf("snap = %d", code)
	}
	if hasWarning(rep, "removed") {
		t.Errorf("warnings = %v, a composable accent is not an invisible character", rep.Warnings)
	}
	if len(rep.Marks) != 1 || rep.Marks[0].Matches != 1 {
		t.Errorf("marks = %+v, want the precomposed needle to match the decomposed text", rep.Marks)
	}
}

// --redact-keep must never draw a whole secret. A non-ASCII literal passed
// the old byte-length guard ("ñññññ" is 10 bytes, 5 characters), and a regex
// match shorter than keep passed every per-literal guard: both shipped in
// clear at exit 0 with no warning. Each mode now refuses with a usage error.
func TestRedactKeepNeverExposesAWholeMatch(t *testing.T) {
	const req = "GET / HTTP/1.1\nHost: a\nX-User: ñññññ\nX-T: abcdefghijkl\nX-R: tok_ab tok_abcdefghij\n\n"
	const text = "user=ñññññ\ntoken=abcdefghijkl\na=tok_ab\nb=tok_abcdefghij\n"
	multibyte := []string{"--redact", "ñññññ", "--redact", "abcdefghijkl", "--redact-keep", "5"}
	shortRe := []string{"--redact-re", "tok_[a-z]+", "--redact-keep", "6"}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"snap multibyte literal", append([]string{"snap", "in.txt"}, multibyte...)},
		{"snap short regex match", append([]string{"snap", "in.txt"}, shortRe...)},
		{"term multibyte literal", append([]string{"term", "in.txt"}, multibyte...)},
		{"term short regex match", append([]string{"term", "in.txt"}, shortRe...)},
		{"http multibyte literal", append([]string{"http", "-r", "req.http"}, multibyte...)},
		{"http short regex match", append([]string{"http", "-r", "req.http"}, shortRe...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range map[string]string{"in.txt": text, "req.http": req} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			code, stdout, _ := runTaker(t, dir, append(tc.args, "-o", "o.png", "--json")...)
			if code != exitUsage {
				t.Errorf("exit = %d, want 2: a secret would be drawn in full\n%s", code, stdout)
			}
			if !strings.Contains(stdout, "--redact-keep") {
				t.Errorf("the refusal does not name --redact-keep:\n%s", stdout)
			}
			if strings.Contains(stdout, "ñññ") || strings.Contains(stdout, "tok_ab") {
				t.Errorf("the report leaks a redacted value:\n%s", stdout)
			}
			if _, err := os.Stat(filepath.Join(dir, "o.png")); err == nil {
				t.Error("a PNG was written")
			}
		})
	}
}

// The "hides only N char(s)" warning counts characters too: "pässwörd1" under
// keep 8 hides one character, not the three a byte count claimed.
func TestRedactKeepWarningCountsCharacters(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, []byte("pw=pässwörd1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, rep, _ := runJSON(t, func(r *reporter) int {
		return runSnap(snapCmd(SnapCmd{File: file, Output: filepath.Join(dir, "o.png"), JSON: true,
			Redact: []string{"pässwörd1"}, RedactKeep: 8}), r)
	})
	if code != exitOK {
		t.Fatalf("snap = %d", code)
	}
	if !hasWarning(rep, "hides only 1 char(s)") {
		t.Errorf("warnings = %v, want 'hides only 1 char(s)'", rep.Warnings)
	}
}

// A --redact literal's fragments are masked only as whole tokens: "app" from
// app.example.test must not black out the middle of "application/json", and
// only the literal the analyst typed earns a "matched nothing" warning — not
// each part split from it.
func TestRedactFragmentsMaskWholeTokensOnly(t *testing.T) {
	dir := t.TempDir()
	req := filepath.Join(dir, "r.http")
	body := "POST /x HTTP/1.1\nHost: app.example.test\nContent-Type: application/json\n\n{\"err\":\"parsing \\\"app\\\"\"}\n"
	if err := os.WriteFile(req, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	code, rep, _ := runJSON(t, func(r *reporter) int {
		return runHTTP(&HTTPCmd{Request: req, Output: filepath.Join(dir, "o.png"), JSON: true, DryRun: true,
			Redact: []string{"app.example.test"}}, r)
	})
	if code != exitOK {
		t.Fatalf("http = %d", code)
	}
	// Host line: the literal plus its three parts, overlapping it (4). Body:
	// the echoed "app" (1). "application" stays readable — boxing it made 6.
	if rep.Redactions != 5 {
		t.Errorf("redactions = %d, want 5 — 'application' must stay readable", rep.Redactions)
	}

	code, rep, _ = runJSON(t, func(r *reporter) int {
		return runHTTP(&HTTPCmd{Request: req, JSON: true, DryRun: true,
			Redact: []string{"nothere-zzz.qq"}}, r)
	})
	if code != exitOK {
		t.Fatalf("http = %d", code)
	}
	n := 0
	for _, w := range rep.Warnings {
		if strings.Contains(w, "matched nothing") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("warnings = %v, want one 'matched nothing' for the one literal typed", rep.Warnings)
	}
}

// snap and term share the rule.
func TestRedactFragmentsSnapAndTerm(t *testing.T) {
	const text = "host = \"app.example.test\"\nctype = \"application/json\"\nerr: parsing \"app\"\n"
	dir, file := writeTranscript(t, text)
	for _, run := range []func(r *reporter) int{
		func(r *reporter) int {
			return runSnap(snapCmd(SnapCmd{File: file, Output: filepath.Join(dir, "s.png"), DryRun: true, JSON: true,
				Redact: []string{"app.example.test", "nothere-zzz.qq"}}), r)
		},
		func(r *reporter) int {
			return runTerm(termCmd(TermCmd{File: file, Output: filepath.Join(dir, "t.png"), DryRun: true, JSON: true,
				Redact: []string{"app.example.test", "nothere-zzz.qq"}}), r)
		},
	} {
		code, rep, _ := runJSON(t, run)
		if code != exitOK {
			t.Fatalf("run = %d", code)
		}
		// Line 1: the literal plus its three parts, overlapping it (4).
		// Line 3: the echoed "app" (1). Boxing "application" made 6.
		if rep.Redactions != 5 {
			t.Errorf("redactions = %d, want 5 — 'application' must stay readable", rep.Redactions)
		}
		n := 0
		for _, w := range rep.Warnings {
			if strings.Contains(w, "matched nothing") {
				n++
			}
		}
		if n != 1 {
			t.Errorf("warnings = %v, want exactly one 'matched nothing'", rep.Warnings)
		}
	}
}

func TestTokenRe(t *testing.T) {
	for _, tc := range []struct {
		frag, text string
		want       bool
	}{
		{"app", "application/json", false},
		{"app", "Host: app.example", true},
		{"ZX9", `parsing "ZX9": invalid`, true},
		{"ZX9", "xZX9y", false},
		{"!bang", "a !bang", true}, // punctuation edge: no \b demanded there
		{"tok", "tok_id", false},
	} {
		if got := tokenRe(tc.frag).MatchString(tc.text); got != tc.want {
			t.Errorf("tokenRe(%q) on %q = %v, want %v", tc.frag, tc.text, got, tc.want)
		}
	}
}

// A --redact-re match in a file name is a secret too. Failures that came
// before the pattern was compiled skipped the regex scrub, so the name
// travelled in clear through command, source.path and error.message.
func TestRedactReScrubsEarlyFailures(t *testing.T) {
	const name = "tok_ABC123"
	for _, tc := range []struct {
		label string
		args  []string
	}{
		{"bad --first-line", []string{"snap", name + ".py", "--redact-re", "tok_[A-Z0-9]+", "--first-line", "0", "-o", "x.png", "--json"}},
		{"missing file", []string{"snap", "tok_ZZZ999.py", "--redact-re", "tok_[A-Z0-9]+", "-o", "x.png", "--json"}},
		{"json and toon", []string{"snap", name + ".py", "--redact-re", "tok_[A-Z0-9]+", "--json", "--toon"}},
		{"term missing file", []string{"term", "tok_ZZZ999.log", "--redact-re", "tok_[A-Z0-9]+", "--json"}},
		{"http bad flag combo", []string{"http", "-f", name + ".http", "-r", name + ".http", "--redact-re", "tok_[A-Z0-9]+", "--json"}},
		// A parse error is the earliest failure: kong dies before any
		// run function compiles the pattern.
		{"unknown flag", []string{name + ".py", "--redact-re", "tok_[A-Z0-9]+", "--bogus", "-o", "x.png", "--json"}},
		{"unexpected argument", []string{"http", name + ".http", "--redact-re", "tok_[A-Z0-9]+", "-o", "x.png", "--json"}},
		{"unexpected argument, = form", []string{"http", name + ".http", "--redact-re=tok_[A-Z0-9]+", "--json"}},
	} {
		t.Run(tc.label, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, name+".py"), []byte("x = 1\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			code, stdout, stderr := runTaker(t, dir, tc.args...)
			if code == exitOK {
				t.Fatalf("exit 0, want a failure\n%s", stdout)
			}
			for _, leak := range []string{name, "tok_ZZZ999"} {
				if strings.Contains(stdout, leak) || strings.Contains(stderr, leak) {
					t.Errorf("%q leaked\nstdout:\n%s\nstderr:\n%s", leak, stdout, stderr)
				}
			}
		})
	}
}

// A pattern that does not compile is still a usage error naming the flag.
func TestRedactReBadPatternIsUsage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.py"), []byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := runTaker(t, dir, "a.py", "--redact-re", "tok_(", "--json")
	if code != exitUsage || !strings.Contains(stdout, "parse --redact-re") {
		t.Errorf("exit = %d, want 2 naming --redact-re\n%s", code, stdout)
	}
}
