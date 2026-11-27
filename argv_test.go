package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// takerMainEnv makes the test binary act as the taker CLI. The parse-error
// path and mode selection live in main(), which ends in os.Exit, so the only
// faithful way to test them is to run main in a child process.
const takerMainEnv = "TAKER_TEST_RUN_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(takerMainEnv) == "1" {
		os.Args = append([]string{"taker"}, os.Args[1:]...)
		main()
		os.Exit(exitOK)
	}
	os.Exit(m.Run())
}

// runTaker runs `taker args...` in dir and returns the exit code and both
// channels. stdin is empty, so nothing waits on a terminal.
func runTaker(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), takerMainEnv+"=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, stdout.String(), stderr.String()
	case errors.As(err, &ee):
		return ee.ExitCode(), stdout.String(), stderr.String()
	}
	t.Fatalf("run taker %v: %v", args, err)
	return 0, "", ""
}

// argvNeedleValues is the only thing that keeps a needle out of the echoed
// command when kong rejects the argv: the parsed secrets do not exist yet.
func TestArgvNeedleValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"space form", []string{"f.py", "--redact", "S3CRET", "-o", "x.png"}, []string{"S3CRET"}},
		{"equals form", []string{"--redact=S3CRET", "--bogus"}, []string{"S3CRET"}},
		{"regex flag", []string{"--redact-re", "tok_[a-z]+"}, []string{"tok_[a-z]+"}},
		{"short mark", []string{"-m", "needle", "f.py"}, []string{"needle"}},
		{"long mark both forms", []string{"--mark", "a1", "--mark-re=b2"}, []string{"a1", "b2"}},
		// After -- the tokens are the executed command's, not taker's.
		{"stops at --", []string{"exec", "--redact", "S3CRET", "--", "curl", "--redact", "theirs"}, []string{"S3CRET"}},
		// An empty =value names nothing to hide; validateRedact refuses it.
		{"empty equals", []string{"--redact=", "f.py"}, nil},
		{"flag without value", []string{"f.py", "--redact"}, nil},
		// File paths and option values stay readable: the echoed command
		// remains a replayable record.
		{"non-needle flags", []string{"-o", "out.png", "--title", "t", "secret.py"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := argvNeedleValues(tc.args); !slices.Equal(got, tc.want) {
				t.Errorf("argvNeedleValues(%q) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

// unbalancedRE is the tell that kong's comma split cut a pattern in half.
func TestUnbalancedRE(t *testing.T) {
	for p, want := range map[string]bool{
		`.{0`:       true,
		`[a`:        true,
		`(foo`:      true,
		`\\{`:       true, // an escaped backslash, then a real brace
		`\{`:        false,
		`a\[b`:      false,
		`.{0,3}`:    false,
		`[a,b]`:     false,
		`(x|y)+`:    false,
		`tok_[0-9]`: false,
	} {
		if got := unbalancedRE(p); got != want {
			t.Errorf("unbalancedRE(%q) = %v, want %v", p, got, want)
		}
	}
}

// Kong splits flag lists on commas, so compileRegex must put a split pattern
// back together and leave whole patterns alone. A wrong rejoin is not
// cosmetic: two --redact-re patterns fused into one match neither secret, and
// the image ships with both visible.
func TestCompileRegexCommaRejoin(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []string
		want []string
	}{
		{"split repeat", []string{`.{0`, `3}x`}, []string{`.{0,3}x`}},
		{"split class", []string{`[a`, `b]+`}, []string{`[a,b]+`}},
		{"split group", []string{`(a`, `b)`}, []string{`(a,b)`}},
		{"separate patterns", []string{`tok_[0-9]+`, `key-[a-f]+`}, []string{`tok_[0-9]+`, `key-[a-f]+`}},
		{"escaped brace stays whole", []string{`a\{b`, `c+`}, []string{`a\{b`, `c+`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := compileRegex("--redact-re", tc.in)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, re := range res {
				got = append(got, re.String())
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("compileRegex(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A bracket inside a character class is not an opener: `[^)]+` is a whole
// pattern. unbalancedRE counts it anyway, so the next --redact-re is glued on
// with a comma and neither pattern matches what it was given for.
func TestCompileRegexKeepsClassBracketPatternsApart(t *testing.T) {
	for _, in := range [][]string{
		{`=[^)]+`, `sekrit[0-9]+`},
		{`[(]tok`, `sekrit[0-9]+`},
		{`x[{]`, `sekrit[0-9]+`},
		{`[]a]+`, `sekrit[0-9]+`},
		{`[^]a]+`, `sekrit[0-9]+`},
		{`[[:alpha:]]+`, `sekrit[0-9]+`},
		{`[\]]+`, `sekrit[0-9]+`},
	} {
		res, err := compileRegex("--redact-re", in)
		if err != nil {
			t.Fatal(err)
		}
		if len(res) != 2 {
			t.Errorf("compileRegex(%q): got %d patterns %v, want the two that were given", in, len(res), res)
		}
	}
	// A class kong cut in half is still rejoined.
	res, err := compileRegex("--redact-re", []string{`[a`, `)]+`})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].String() != `[a,)]+` {
		t.Errorf("split class with a bracket member = %v, want [a,)]+", res)
	}
}

// An unknown flag dies in kong before any secret is registered. The error
// document must still be the requested format, carry exit 2, and keep the
// --redact value out of both channels.
func TestParseErrorMasksNeedles(t *testing.T) {
	const secret = "SECRETVALUE123"
	for _, tc := range []struct {
		name string
		args []string
		mode string
	}{
		{"space form", []string{"--redact", secret, "--bogus", "--json"}, "snap"},
		{"equals form", []string{"--redact=" + secret, "--bogus", "--json"}, "snap"},
		{"regex flag", []string{"--redact-re", secret + "[0-9]*", "--bogus", "--json"}, "snap"},
		{"term mode", []string{"term", "t.log", "--redact", secret, "--bogus", "--json"}, "term"},
		{"http mode", []string{"http", "--redact=" + secret, "--bogus", "--json"}, "http"},
		// kong's own message repeats a value it refused: only the argv
		// needle list can mask it, scrubRedactArgs never sees the error.
		{"value kong echoes", []string{"--redact", "--" + secret, "--json"}, "snap"},
		// Mark needles are masked on this path too: the parsed split
		// between marks and redacts does not exist yet.
		{"mark value", []string{"-m", secret, "--bogus", "--json"}, "snap"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runTaker(t, t.TempDir(), tc.args...)
			if code != exitUsage {
				t.Errorf("exit = %d, want %d (usage)", code, exitUsage)
			}
			if strings.Contains(stdout, secret) || strings.Contains(stderr, secret) {
				t.Fatalf("the --redact value leaked\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
			}
			var rep Report
			if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
				t.Fatalf("stdout is not a json report: %v\n%s", err, stdout)
			}
			if rep.OK || rep.Exit != exitUsage || rep.Error == nil || rep.Error.Kind != kindUsage {
				t.Errorf("report = ok:%v exit:%d error:%+v, want a usage failure", rep.OK, rep.Exit, rep.Error)
			}
			if rep.Mode != tc.mode {
				t.Errorf("mode = %q, want %q", rep.Mode, tc.mode)
			}
			// The marker says a value was given and hidden, not that the
			// flag was absent.
			if !strings.Contains(rep.Command, "<redacted>") {
				t.Errorf("command %q does not show that a value was masked", rep.Command)
			}
		})
	}
}

// Without --json a failure still emits a document (TOON), and the needle stays
// out of it too.
func TestParseErrorMasksNeedlesWithoutFormatFlag(t *testing.T) {
	const secret = "SECRETVALUE123"
	code, stdout, stderr := runTaker(t, t.TempDir(), "term", "--redact="+secret, "--bogus")
	if code != exitUsage {
		t.Errorf("exit = %d, want %d", code, exitUsage)
	}
	if strings.Contains(stdout, secret) || strings.Contains(stderr, secret) {
		t.Fatalf("the --redact value leaked\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(stdout, "kind: usage") {
		t.Errorf("no TOON error document on stdout:\n%s", stdout)
	}
}

// On the success path the echoed command is provenance: the report and the
// PNG both carry it, and neither may carry the value the boxes hide.
func TestSuccessPathMasksNeedlesInCommand(t *testing.T) {
	const secret = "tok_Zq81LmX0"
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "t.log"), []byte("$ env\nTOKEN="+secret+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runTaker(t, dir, "term", "t.log", "--redact="+secret, "-o", "o.png", "--json")
	if code != exitOK {
		t.Fatalf("exit = %d\n%s\n%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, secret) || strings.Contains(stderr, secret) {
		t.Fatalf("the --redact value leaked\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	png, err := os.ReadFile(filepath.Join(dir, "o.png"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(png, []byte(secret)) {
		t.Error("the PNG's metadata embeds the redacted literal")
	}
	var rep Report
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Mode != "term" || rep.Redactions == 0 {
		t.Errorf("mode=%q redactions=%d, want term with the secret boxed", rep.Mode, rep.Redactions)
	}
}

// A comma is ordinary text in a --mark literal: function calls, CSV and JSON
// values hold them. Kong's default list split turned -m "foo(a, b)" into two
// needles, two boxes and no warning — a silent mis-mark.
func TestMarkKeepsItsCommas(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"a.py":   "foo(a, b)\nbar\n",
		"t.log":  "$ run\nfoo(a, b)\n",
		"r.http": "POST / HTTP/1.1\r\nHost: x\r\n\r\nfoo(a, b)",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		args    []string
		matches int
	}{
		{[]string{"a.py"}, 1},
		{[]string{"term", "t.log"}, 1},
		{[]string{"http", "-r", "r.http"}, 1},
		// The prompt line echoes the command, so the literal is there twice.
		{[]string{"exec", "--sh", "echo 'foo(a, b)'"}, 2},
	} {
		args := tc.args
		t.Run(args[0], func(t *testing.T) {
			full := append(slices.Clone(args), "-m", "foo(a, b)", "--dry-run", "--json")
			code, stdout, stderr := runTaker(t, dir, full...)
			if code != exitOK {
				t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
			}
			var rep Report
			if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
				t.Fatalf("not a json report: %v\n%s", err, stdout)
			}
			if len(rep.Marks) != 1 || rep.Marks[0].Needle != "foo(a, b)" || rep.Marks[0].Matches != tc.matches {
				t.Errorf("marks = %+v, want one needle \"foo(a, b)\" with %d match(es)", rep.Marks, tc.matches)
			}
		})
	}
}
