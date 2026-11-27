package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
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

func execCmd(c ExecCmd) *ExecCmd {
	c.Theme, c.Chrome, c.WrapWidth, c.JSON = "vscode-dark", "window", 80, true
	if c.Timeout == 0 {
		c.Timeout = 10
	}
	return &c
}

func needSh(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX sh and signals")
	}
}

// The status comes from how the wait ended. A command that exits 0 while a
// background process it started still holds the pipe exits 0: it used to
// keep taker until the deadline and be labelled "killed after Ns".
func TestExecStatusComesFromTheWait(t *testing.T) {
	needSh(t)
	dir := t.TempDir()
	start := time.Now()
	code, rep, stderr := runJSON(t, func(r *reporter) int {
		return runExec(execCmd(ExecCmd{
			Shell: "sleep 30 & echo hi", Timeout: 20, Output: filepath.Join(dir, "bg.png"),
		}), r)
	})
	if code != exitOK {
		t.Fatalf("exec = %d\n%s", code, stderr)
	}
	if took := time.Since(start); took > execPipeGrace+5*time.Second {
		t.Errorf("took %s: the background sleeper held the run", took)
	}
	if rep.Exec == nil || rep.Exec.TimedOut || rep.Exec.Exit == nil || *rep.Exec.Exit != 0 {
		t.Errorf("exec = %+v, want exit 0 and no timeout", rep.Exec)
	}
	if !hasWarning(rep, "a background process still held the output") {
		t.Errorf("no held-pipe warning in %q", rep.Warnings)
	}
}

// A signal is not an exit code: exit is null and the signal is named,
// where Go's -1 used to land in the footnote and the report.
func TestExecNamesTheSignal(t *testing.T) {
	needSh(t)
	dir := t.TempDir()
	code, rep, stderr := runJSON(t, func(r *reporter) int {
		return runExec(execCmd(ExecCmd{Shell: "echo before; kill -9 $$", Output: filepath.Join(dir, "s.png")}), r)
	})
	if code != exitOK {
		t.Fatalf("exec = %d\n%s", code, stderr)
	}
	if rep.Exec == nil || rep.Exec.Exit != nil || rep.Exec.Signal != "SIGKILL" || rep.Exec.TimedOut {
		t.Errorf("exec = %+v, want exit null, signal SIGKILL, no timeout", rep.Exec)
	}
}

// The deadline kills a command that is still running, and only that is a
// timeout.
func TestExecTimeoutKills(t *testing.T) {
	needSh(t)
	start := time.Now()
	run := runChild([]string{"sleep", "30"}, 300*time.Millisecond, 200*time.Millisecond)
	if run.startErr != nil || !run.timedOut || run.signal != "SIGKILL" {
		t.Errorf("run = %+v, want a SIGKILL timeout", run)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("took %s for a 300ms deadline", took)
	}
	// A command that is done well before the deadline is not timed out.
	run = runChild([]string{"sh", "-c", "exit 4"}, 5*time.Second, 200*time.Millisecond)
	if run.timedOut || run.held || run.state == nil || run.state.ExitCode() != 4 {
		t.Errorf("run = %+v, want exit 4", run)
	}
}

// The deadline is a timeout on every OS, Windows included: there a killed
// process's status says "exited 1", and reading the timeout off the status
// labelled a killed long-runner "exit 1". The command is this test binary,
// so the test needs no shell and runs on Windows CI too.
func TestExecTimeoutAnyOS(t *testing.T) {
	self := []string{os.Args[0], "-test.run=^TestExecHelperProcess$"}
	t.Setenv("TAKER_EXEC_HELPER", "sleep")
	start := time.Now()
	run := runChild(self, 2*time.Second, 200*time.Millisecond)
	if run.startErr != nil || !run.timedOut {
		t.Errorf("run = %+v, want a timeout", run)
	}
	if took := time.Since(start); took > 15*time.Second {
		t.Errorf("took %s for a 2s deadline", took)
	}
	t.Setenv("TAKER_EXEC_HELPER", "exit4")
	run = runChild(self, 30*time.Second, 200*time.Millisecond)
	if run.timedOut || run.state == nil || run.state.ExitCode() != 4 {
		t.Errorf("run = %+v, want exit 4 and no timeout", run)
	}
}

// TestExecHelperProcess is the command TestExecTimeoutAnyOS runs; without
// TAKER_EXEC_HELPER it does nothing.
func TestExecHelperProcess(t *testing.T) {
	switch os.Getenv("TAKER_EXEC_HELPER") {
	case "sleep":
		fmt.Println("started")
		time.Sleep(60 * time.Second)
		os.Exit(0)
	case "exit4":
		os.Exit(4)
	}
}

// --timeout 0 killed the command before it printed and drew the empty frame
// as "killed after 0s". A value past what time.Duration holds wrapped
// negative and did the same.
func TestExecTimeoutMustBePositive(t *testing.T) {
	ns := []int{0, -1}
	if strconv.IntSize == 64 {
		over := maxTimeout + 1 // a variable: the conversion must not be a constant on 32-bit
		ns = append(ns, int(over), int(2*over), int(over*10))
	}
	for _, n := range ns {
		code, rep, _ := runJSON(t, func(r *reporter) int {
			c := execCmd(ExecCmd{Args: []string{"echo", "hi"}, Output: filepath.Join(t.TempDir(), "o.png")})
			c.Timeout = n
			return runExec(c, r)
		})
		if code != exitUsage || rep.Error == nil || !strings.Contains(rep.Error.Message, "--timeout must be between 1 and") {
			t.Errorf("--timeout %d = %d %+v, want a usage error", n, code, rep.Error)
		}
	}
}

// Output past the cap cannot render, so exec stops reading, kills the
// command and refuses — at once, not after gigabytes and minutes.
func TestExecOutputCap(t *testing.T) {
	needSh(t)
	for _, tc := range []struct {
		argv []string
		over string
	}{
		{[]string{"yes"}, "10000 lines"},
		{[]string{"sh", "-c", "while :; do printf '%01024d' 0; done"}, "4 MiB"},
	} {
		start := time.Now()
		run := runChild(tc.argv, 20*time.Second, 200*time.Millisecond)
		if run.overflow != tc.over || run.timedOut {
			t.Errorf("%v: overflow %q timedOut %v, want %q", tc.argv, run.overflow, run.timedOut, tc.over)
		}
		if took := time.Since(start); took > 10*time.Second {
			t.Errorf("%v took %s", tc.argv, took)
		}
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "y.png")
	code, rep, _ := runJSON(t, func(r *reporter) int {
		return runExec(execCmd(ExecCmd{Args: []string{"yes"}, Output: out}), r)
	})
	if code != exitRefused || rep.Error == nil || !strings.Contains(rep.Error.Message, "passed 10000 lines") {
		t.Errorf("exec yes = %d %+v, want a refusal naming the cap", code, rep.Error)
	}
	if len(rep.Help) == 0 || !strings.Contains(rep.Help[0], "yes | head -n 200") {
		t.Errorf("help = %q, want the head hint", rep.Help)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a refused run wrote an image")
	}
}
