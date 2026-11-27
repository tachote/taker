package main

import (
	"bufio"
	"os"
	"strings"
	"testing"
	"time"
)

// The TOON document carries the same facts as the JSON one in the shapes
// agents read cheapest: scalars as key: value, uniform rows as tables,
// irregular arrays as - items, and strings quoted only when they must be.
func TestReportTOONEncoding(t *testing.T) {
	rep := newReporter("toon", "snap")
	rep.rep.OK = true
	rep.rep.Exit = 0
	rep.rep.Output = "/abs/out.png"
	rep.rep.Bytes = 16099
	rep.rep.Width = 573
	rep.rep.Command = `taker snap a.go -m "os.system"` // quotes force escaping
	rep.rep.Source = &Source{Kind: "file", Path: "src/a.go"}
	total := 68
	rep.rep.Crop = &Crop{First: 3, Last: 19, Shown: 14, Total: &total}
	rep.rep.Marks = []MarkReport{
		{Needle: "os.system", Matches: 4, Lines: 4, Kept: 4, Dropped: 0, Shown: 3},
		{Needle: "sha256", Matches: 1, Lines: 1, Kept: 1, Dropped: 0, Shown: 1},
	}
	rep.rep.SHA256 = []SourceHash{{Name: "source", Sum: "b94d"}}
	rep.rep.Warnings = []string{"--mark fell outside the view"}
	rep.helpf("rerun without --dry-run to write the PNG")

	got, err := rep.reportTOON()
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"tool: taker\n",
		"ok: true\n",
		// Tables collapse repeated keys — the point of the format.
		"marks[2]{needle,matches,lines,kept,dropped,shown}:\n  os.system,4,4,4,0,3\n  sha256,1,1,1,0,1\n",
		"sha256[1]{name,sum}:\n  source,b94d\n",
		// Nested objects indent under their key.
		"source:\n  kind: file\n  path: src/a.go\n",
		"crop:\n  first: 3\n",
		// Strings with : and quotes get quoted and escaped.
		`command: "taker snap a.go -m \"os.system\""` + "\n",
		`warnings[1]: "--mark fell outside the view"` + "\n",
		"help[1]: rerun without --dry-run to write the PNG\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("TOON missing %q\n---\n%s", want, got)
		}
	}
	// Nothing JSON-shaped leaks into the agent format.
	if strings.Contains(got, "{\"") || strings.Contains(got, "\": ") {
		t.Errorf("TOON still carries JSON syntax:\n%s", got)
	}
}

// A failure document is the one output a consumer can least afford to lose:
// error.kind/error.message arrive as data, not prose, even without --json.
func TestTOONErrorDocument(t *testing.T) {
	rep := newReporter("toon", "http")
	got := ""
	rep.out = &stringsWriter{&got}
	rep.helpf("taker http -f <capture> for a combined 'Save item' file")
	code := rep.fail(exitUsage, kindUsage, "nothing to render: pass -r, -s or -f")
	if code != exitUsage {
		t.Fatalf("fail returned %d", code)
	}
	for _, want := range []string{
		"ok: false\n",
		"exit: 2\n",
		"help[1]: taker http -f <capture>",
		"error:\n  kind: usage\n",
		`message: "nothing to render: pass -r, -s or -f"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("error doc missing %q\n---\n%s", want, got)
		}
	}
}

// The version probe is AXI's fast path: all three bare flags answer, nothing
// else is one.
func TestIsVersionProbe(t *testing.T) {
	for _, args := range [][]string{
		{"-v"}, {"-V"}, {"--version"},
	} {
		if !isVersionProbe(args) {
			t.Errorf("%v not recognised as a version probe", args)
		}
	}
	for _, args := range [][]string{
		nil, {"-v", "extra"}, {"version"}, {"-x"}, {"snap", "-v"},
	} {
		if isVersionProbe(args) {
			t.Errorf("%v wrongly recognised as a version probe", args)
		}
	}
}

type stringsWriter struct{ s *string }

func (w *stringsWriter) Write(p []byte) (int, error) {
	*w.s += string(p)
	return len(p), nil
}

// --toon through the real command path: same document, agent encoding.
func TestToonEndToEnd(t *testing.T) {
	dir := t.TempDir()
	file := dir + "/a.go"
	if err := os.WriteFile(file, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := newReporter("toon", "snap")
	var out strings.Builder
	rep.out = &out
	code := runSnap(snapCmd(SnapCmd{File: file, Output: dir + "/o.png"}), rep)
	if code != exitOK {
		t.Fatalf("run = %d", code)
	}
	got := out.String()
	for _, want := range []string{"tool: taker\n", "ok: true\n", "mode: snap\n", "sha256[1]{name,sum}:"} {
		if !strings.Contains(got, want) {
			t.Errorf("toon report missing %q\n---\n%s", want, got)
		}
	}
}

// The format selector keeps --json untouched and lets --toon be exclusive.
func TestFormatOf(t *testing.T) {
	if got := formatOf(true, false); got != "json" {
		t.Errorf("formatOf(true,false) = %q", got)
	}
	if got := formatOf(false, true); got != "toon" {
		t.Errorf("formatOf(false,true) = %q", got)
	}
	if got := formatOf(false, false); got != "" {
		t.Errorf("formatOf(false,false) = %q", got)
	}
}

// --list-themes answers as data in whichever format was asked for — and a
// successful listing is ok:true, not a run that happened to exit 0.
func TestListThemesMarksOK(t *testing.T) {
	for _, format := range []string{"json", "toon"} {
		rep := newReporter(format, "snap")
		var out strings.Builder
		rep.out = &out
		if code := listThemes(rep); code != exitOK {
			t.Fatalf("%s: listThemes = %d", format, code)
		}
		if !rep.rep.OK {
			t.Errorf("%s: themes report has ok:false on a successful run", format)
		}
		if !strings.Contains(out.String(), "vscode-dark") {
			t.Errorf("%s: theme list missing vscode-dark", format)
		}
	}
}

// A taker flag inside the command position is a usage error, not a silent
// render to the default output path — the trap `exec id -o out.png` used to
// spring. After -- the same tokens are the command's business and pass.
func TestExecFlagInsideCommandIsUsageError(t *testing.T) {
	rep := newReporter("", "exec")
	var out strings.Builder
	rep.out = &out
	code := runExec(&ExecCmd{
		Args: []string{"id", "-o", "/tmp/trap.png"}, Output: "ignored.png",
		Timeout: 5, Theme: "vscode-dark", Chrome: "window", WrapWidth: 80,
	}, rep)
	if code != exitUsage {
		t.Fatalf("exec id -o out.png = %d, want usage error", code)
	}
	if !strings.Contains(out.String(), `is a taker flag sitting inside the command`) {
		t.Errorf("error doc does not name the offending flag:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "taker exec <taker-flags> -- id") {
		t.Errorf("no self-correcting hint:\n%s", out.String())
	}

	// An unknown flag in front of -- is kong's passthrough swallowing it,
	// not a taker flag inside the command: name it as the unknown flag it
	// is, and point at the help instead of a nonsense rewrite.
	rep = newReporter("", "exec")
	out.Reset()
	rep.out = &out
	code = runExec(&ExecCmd{
		Args: []string{"-n", "1", "--", "echo", "hi"}, Output: "ignored.png",
		Timeout: 5, Theme: "vscode-dark", Chrome: "window", WrapWidth: 80,
	}, rep)
	if code != exitUsage {
		t.Fatalf("exec -n 1 -- echo hi = %d, want usage error", code)
	}
	if !strings.Contains(out.String(), `unknown flag \"-n\" for taker exec`) ||
		!strings.Contains(out.String(), "taker exec --help") ||
		strings.Contains(out.String(), "sitting inside") {
		t.Errorf("error doc does not name the unknown flag:\n%s", out.String())
	}
}

// The child's exit code is part of the evidence: the PNG's footnote shows it
// and the report now carries it as data — ok:true describes the render,
// exec.exit describes the command.
func TestExecReportCarriesChildExit(t *testing.T) {
	dir := t.TempDir()
	code, rep, _ := runJSON(t, func(r *reporter) int {
		return runExec(&ExecCmd{
			Args: []string{"--", "sh", "-c", "exit 3"}, Output: dir + "/o.png",
			Timeout: 5, Theme: "vscode-dark", Chrome: "window", WrapWidth: 80, JSON: true,
		}, r)
	})
	if code != exitOK {
		t.Fatalf("run = %d", code)
	}
	if rep.Exec == nil || rep.Exec.Exit == nil || *rep.Exec.Exit != 3 {
		t.Fatalf("exec report = %+v, want child exit 3", rep.Exec)
	}
}

// probeStdin distinguishes a live producer from a pipe nobody will write:
// buffered data returns instantly, an empty open pipe trips the grace period,
// and a pipe closed with nothing in it is empty input, not a slow one.
func TestProbeStdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := w.WriteString("x"); err != nil {
		t.Fatal(err)
	}
	w.Close()
	if got := probeStdin(bufio.NewReader(r), 200*time.Millisecond); got != stdinData {
		t.Errorf("data already in the pipe = %v, want ready", got)
	}

	r2, w2, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	defer w2.Close() // write end held open, nothing written — the agent's pipe
	start := time.Now()
	if got := probeStdin(bufio.NewReader(r2), 120*time.Millisecond); got != stdinStalled {
		t.Errorf("an empty open pipe = %v, want stalled", got)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("the probe should not block on an empty pipe")
	}

	r3, w3, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r3.Close()
	w3.Close()
	start = time.Now()
	if got := probeStdin(bufio.NewReader(r3), 2*time.Second); got != stdinEOF {
		t.Errorf("a closed empty pipe = %v, want EOF", got)
	}
	if time.Since(start) > time.Second {
		t.Error("EOF should be reported at once, not after the grace period")
	}
}

// A string a TOON decoder would read as a number has to be quoted, including
// one too large for a float64: "1e999" is still numeric syntax, and an
// unquoted --mark 1e999 came back as a number instead of the needle.
func TestTOONQuotesNumericLookingStrings(t *testing.T) {
	for _, s := range []string{"1e999", "-1e999", "007", "42", "3.14", "1E5"} {
		if got := toonStr(s); got != `"`+s+`"` {
			t.Errorf("toonStr(%q) = %s, want it quoted", s, got)
		}
	}
	for _, s := range []string{"os.system", "v1.2.3", "e999", "1e"} {
		if got := toonStr(s); got != s {
			t.Errorf("toonStr(%q) = %s, want it bare", s, got)
		}
	}
}

// The document already ends every line with a newline; emit must not add a
// second one — a trailing blank line is an extra empty record to a reader
// that splits on lines.
func TestTOONEndsWithOneNewline(t *testing.T) {
	rep := newReporter("toon", "snap")
	got := ""
	rep.out = &stringsWriter{&got}
	rep.rep.OK = true
	rep.emit(exitOK)
	if !strings.HasSuffix(got, "\n") || strings.HasSuffix(got, "\n\n") {
		t.Errorf("toon document should end in exactly one newline: %q", got[max(0, len(got)-12):])
	}
}
