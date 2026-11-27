package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The trap list is exec's own flags, read off kong: -n, --lines, -v and
// --version are not exec flags, and a hand-kept list that named them refused
// `head -n 2` and `grep -v` as taker flags.
func TestExecFlagNamesComeFromTheModel(t *testing.T) {
	names := execFlagNames()
	for _, f := range []string{"-o", "--output", "--sh", "--timeout", "-m", "--mark", "--redact-keep", "-T", "--json", "--dry-run"} {
		if !names[f] {
			t.Errorf("%s is an exec flag but not in the trap list", f)
		}
	}
	for _, f := range []string{"-n", "--lines", "-v", "--version", "-h", "--help", "--list-themes"} {
		if names[f] {
			t.Errorf("%s is not an exec flag but is in the trap list", f)
		}
	}
}

func TestOwnArgs(t *testing.T) {
	for _, tc := range []struct {
		argv, pass, want []string
	}{
		{[]string{"exec", "-o", "x.png", "--", "ls", "-m"}, []string{"--", "ls", "-m"}, []string{"exec", "-o", "x.png"}},
		{[]string{"exec", "id", "-o", "x.png", "-m"}, []string{"id", "-o", "x.png", "-m"}, []string{"exec"}},
		// A mismatch (a test calling runExec directly) keeps the whole argv.
		{[]string{"-test.run=X"}, []string{"id", "a", "b"}, []string{"-test.run=X"}},
	} {
		if got := ownArgs(tc.argv, tc.pass); strings.Join(got, " ") != strings.Join(tc.want, " ") {
			t.Errorf("ownArgs(%q, %q) = %q, want %q", tc.argv, tc.pass, got, tc.want)
		}
	}
}

// The command's own flags are the command's business: none of these is a
// taker flag in the wrong place, an empty taker needle, or a program named
// after a flag.
func TestExecArgvIsTheCommands(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name string
		args []string
		code int
		want string // a substring of error.message, for the failures
	}{
		{"head -n", []string{"exec", "--dry-run", "--json", "head", "-n", "1", "/dev/null"}, exitOK, ""},
		{"grep -v", []string{"exec", "--dry-run", "--json", "grep", "-v", "x", "/dev/null"}, exitOK, ""},
		{"-m after --", []string{"exec", "--dry-run", "--json", "--", "echo", "-m"}, exitOK, ""},
		{"trap still fires", []string{"exec", "--json", "echo", "-o", "x.png", "-m"}, exitUsage, `"-o" is a taker flag sitting inside the command`},
		{"unknown flag", []string{"exec", "--json", "--bogus"}, exitUsage, `unknown flag "--bogus" for taker exec`},
		{"list-themes", []string{"exec", "--json", "--list-themes"}, exitUsage, `unknown flag "--list-themes" for taker exec`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runTaker(t, dir, tc.args...)
			if code != tc.code {
				t.Fatalf("exit = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tc.code, stdout, stderr)
			}
			var rep Report
			if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
				t.Fatalf("not a json report: %v\n%s", err, stdout)
			}
			if tc.want != "" && (rep.Error == nil || !strings.Contains(rep.Error.Message, tc.want)) {
				t.Errorf("error = %+v, want %q", rep.Error, tc.want)
			}
			if tc.name == "list-themes" && !strings.Contains(strings.Join(rep.Help, "\n"), "taker --list-themes") {
				t.Errorf("help = %q, want the taker --list-themes hint", rep.Help)
			}
		})
	}
}

// The recorded command must split back into the words that ran. sh is the
// judge: it reads the quoted line and prints each word it sees.
func TestShellQuoteRoundTrips(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	words := []string{
		"taker", "plain", "two words", "", "it's", "it's $HOME", "a\\b", "say \"hi\"",
		"`id`", "$(id)", "wow!", "it's!", "tab\there", "x=1", "-L", "--redact=a b", "~user",
		"#hash", "semi;colon", "a|b", "glob*", "<redacted>", "ünï",
	}
	line := shellQuote(words)
	out, err := exec.Command("sh", "-c", "set -- "+line+`; for a; do printf '%s\0' "$a"; done`).Output()
	if err != nil {
		t.Fatalf("sh rejected %q: %v", line, err)
	}
	got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	if strings.Join(got, "|") != strings.Join(words, "|") || len(got) != len(words) {
		t.Errorf("%q split back into\n%q\nwant\n%q", line, got, words)
	}
	// A plain command still reads as typed.
	if got := shellQuote([]string{"grep", "-c", "x", "a.go"}); got != "grep -c x a.go" {
		t.Errorf("plain argv quoted as %q", got)
	}
}

// The recorded command replays: quoted words, the binary's base name
// instead of a path into the analyst's home, and every needle masked before
// it is quoted.
func TestCommandLineQuotesAndDropsThePath(t *testing.T) {
	rep := newReporter("", "snap")
	rep.argv = []string{"/Users/someone/.local/bin/taker", "-L", "two words", "--redact", "it's-s3cr3t", "-m", "x=1", "-o", "o.png"}
	rep.setSecretsAll([]string{"it's-s3cr3t"}, []string{"it's-s3cr3t"})
	want := "taker -L 'two words' --redact <redacted> -m x=1 -o o.png"
	if rep.rep.Command != want {
		t.Errorf("command = %q, want %q", rep.rep.Command, want)
	}
}

// exec's prompt line, source.path and taker:exec chunk show the command
// that ran: "func main" is one argument, not two.
func TestExecPromptIsQuoted(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "o.png")
	code, rep, stderr := runJSON(t, func(r *reporter) int {
		return runExec(&ExecCmd{
			Args: []string{"--", "grep", "-c", "func main", filepath.Join(dir, "a.go")}, Output: out,
			Timeout: 5, Theme: "vscode-dark", Chrome: "window", WrapWidth: 80, JSON: true,
			Mark: []string{"grep -c 'func main'"},
		}, r)
	})
	if code != exitOK {
		t.Fatalf("exec = %d\n%s", code, stderr)
	}
	want := "grep -c 'func main' " + filepath.Join(dir, "a.go")
	if rep.Source == nil || rep.Source.Path != want {
		t.Errorf("source = %+v, want path %q", rep.Source, want)
	}
	// The mark finds the quoted text, so the image draws the quotes too.
	if len(rep.Marks) != 1 || rep.Marks[0].Matches != 1 {
		t.Errorf("marks = %+v, want the quoted prompt line matched once", rep.Marks)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := pngTextChunks(t, data)["taker:exec"]; got != want {
		t.Errorf("taker:exec = %q, want %q", got, want)
	}
}

// A --redact value that holds a quote stays masked in every channel: the
// quoting must not split it where the scrub or the image looks for it. A
// value the quoting would split anyway is refused, not drawn in clear.
func TestExecRedactHoldingAQuote(t *testing.T) {
	dir := t.TempDir()
	const secret = "it's-s3cr3t"
	code, stdout, stderr := runTaker(t, dir, "exec", "--json", "-o", "o.png", "--redact", secret, "--", "echo", secret)
	if code != exitOK {
		t.Fatalf("exit = %d\n%s\n%s", code, stdout, stderr)
	}
	png, err := os.ReadFile(filepath.Join(dir, "o.png"))
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string][]byte{"stdout": []byte(stdout), "stderr": []byte(stderr), "png": png} {
		if bytes.Contains(b, []byte("s3cr3t")) {
			t.Errorf("%s carries the redacted value", name)
		}
	}
	var rep Report
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Redactions < 2 {
		t.Errorf("redactions = %d, want the prompt and the output both boxed", rep.Redactions)
	}

	const split = "a'b$c9zz"
	code, stdout, stderr = runTaker(t, dir, "exec", "--json", "-o", "p.png", "--redact", split, "--", "echo", split)
	if code != exitUsage {
		t.Fatalf("split needle: exit = %d, want %d\n%s", code, exitUsage, stdout)
	}
	if strings.Contains(stdout+stderr, "c9zz") {
		t.Errorf("the refused value leaked:\n%s\n%s", stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "p.png")); err == nil {
		t.Error("a refused run wrote an image")
	}
}
