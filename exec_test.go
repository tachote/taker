package main

import (
	"encoding/json"
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
