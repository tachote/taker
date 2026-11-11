package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStderr swaps the process's stderr for a pipe while fn runs, so a test
// can assert on the human channel. The machine channel is a bytes.Buffer handed
// to the reporter instead.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	w.Close()
	os.Stderr = old
	return <-done
}

// runJSON runs a command in-process with --json on and returns the exit code,
// the decoded report, and whatever went to stderr.
func runJSON(t *testing.T, fn func(*reporter) int) (int, Report, string) {
	t.Helper()
	rep := newReporter(true, "snap")
	var out bytes.Buffer
	rep.out = &out

	var code int
	stderr := captureStderr(t, func() { code = fn(rep) })

	var report Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("stdout is not a single json report: %v\n%s", err, out.String())
	}
	if strings.TrimSpace(out.String()) != strings.TrimSpace(strings.TrimSuffix(out.String(), "\n")) {
		t.Error("stdout carries more than the report")
	}
	return code, report, stderr
}

// snapCmd builds the command a parsed invocation would produce. The defaults
// live in the flag tags, so calling the command directly skips them: a test that
// forgets this is testing a call the CLI cannot make.
func snapCmd(c SnapCmd) *SnapCmd {
	if c.Theme == "" {
		c.Theme = "vscode-dark"
	}
	if c.FirstLine == 0 {
		c.FirstLine = 1
	}
	return &c
}

func TestSnapReportDescribesTheImage(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "auth.py")
	code := "def login():\n    user = request.args['user']\n    return db.query(user)\n"
	if err := os.WriteFile(file, []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.png")

	code_, rep, _ := runJSON(t, func(r *reporter) int {
		return runSnap(snapCmd(SnapCmd{File: file, JSON: true, Mark: []string{"db.query"}, Context: 1, Output: out}), r)
	})

	if code_ != exitOK || !rep.OK || rep.Exit != exitOK {
		t.Fatalf("run = %d, report ok=%v exit=%d", code_, rep.OK, rep.Exit)
	}
	if rep.Mode != "snap" || rep.Tool != "taker" {
		t.Errorf("mode/tool = %q/%q", rep.Mode, rep.Tool)
	}
	// Read back off disk, not copied from the request.
	if rep.Bytes == 0 || rep.Width == 0 || rep.Height == 0 {
		t.Errorf("report does not describe a file on disk: %+v", rep)
	}
	if fi, err := os.Stat(out); err != nil || fi.Size() != rep.Bytes {
		t.Errorf("bytes = %d, want the size of the file written", rep.Bytes)
	}
	if rep.Source == nil || rep.Source.Kind != "file" || rep.Source.Path != file {
		t.Errorf("source = %+v, want the file it read", rep.Source)
	}
	// The whole file was read, so its length is known even though the view was
	// trimmed to the context around the mark: --context 1 around line 3 is 2-3.
	if rep.Crop == nil || rep.Crop.Total == nil || *rep.Crop.Total != 3 {
		t.Fatalf("crop = %+v, want a known total of three lines", rep.Crop)
	}
	if rep.Crop.First != 2 || rep.Crop.Last != 3 || rep.Crop.Shown != 2 {
		t.Errorf("crop = %+v, want the 2-3 window the context asked for", rep.Crop)
	}
	if len(rep.Marks) != 1 || rep.Marks[0].Needle != "db.query" || rep.Marks[0].Matches != 1 {
		t.Errorf("marks = %+v, want the literal that was boxed", rep.Marks)
	}
	if rep.Crop.First > 3 || rep.Crop.Last < 3 {
		t.Errorf("crop %+v does not contain the marked line 3", rep.Crop)
	}
}

// An excerpt has no known length, and the report says so instead of publishing a
// number nobody measured: null is a fact, 0 would be a lie.
func TestExcerptReportLeavesTotalUnknown(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.png")

	_, rep, _ := runJSON(t, func(r *reporter) int {
		return runSnap(snapCmd(SnapCmd{
			File: filepath.Join(dir, "x.py"), JSON: true, FirstLine: 70,
			Mark: []string{"nothing here"}, Output: out,
		}), r)
	})
	_ = rep

	// The file does not exist: the report has to say that instead.
	if rep.Error == nil || rep.Error.Kind != kindInput {
		t.Fatalf("report = %+v, want an input error", rep)
	}

	if err := os.WriteFile(filepath.Join(dir, "x.py"), []byte("a = 1\nb = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, rep, _ = runJSON(t, func(r *reporter) int {
		return runSnap(snapCmd(SnapCmd{
			File: filepath.Join(dir, "x.py"), JSON: true, FirstLine: 70,
			Highlight: "71", Context: 1, Output: out,
		}), r)
	})
	if rep.Crop == nil {
		t.Fatal("no crop in the report")
	}
	if rep.Crop.Total != nil {
		t.Errorf("total = %d, want null: the excerpt's real length is unknown", *rep.Crop.Total)
	}
	if rep.Crop.First != 70 {
		t.Errorf("first = %d, want the real first line of the excerpt", rep.Crop.First)
	}
}

// Every way of failing is classified, and the classification travels in the
// document so a consumer never has to read the message to know what to fix.
func TestFailuresCarryTheirKindAndCode(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "ok.py")
	if err := os.WriteFile(good, []byte("a = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		want int
		kind string
		cmd  SnapCmd
	}{
		{"unreadable input", exitInput, kindInput, SnapCmd{File: filepath.Join(dir, "missing.py")}},
		{"line range matches nothing", exitRefused, kindRefused, SnapCmd{File: good, Lines: "500-600"}},
		{"unknown theme", exitUsage, kindUsage, SnapCmd{File: good, Theme: "no-such-theme"}},
		{"http-only theme for code", exitUsage, kindUsage, SnapCmd{File: good, Theme: "burp"}},
		{"first-line below one", exitUsage, kindUsage, SnapCmd{File: good, FirstLine: -1}},
		{"file and git at once", exitUsage, kindUsage, SnapCmd{File: good, Git: "HEAD:x.py"}},
		{"context with nothing to center on", exitUsage, kindUsage, SnapCmd{File: good, Context: 3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := tc.cmd
			cmd.JSON = true
			cmd.Output = filepath.Join(dir, "never.png")

			code, rep, stderr := runJSON(t, func(r *reporter) int { return runSnap(snapCmd(cmd), r) })

			if code != tc.want || rep.Exit != tc.want {
				t.Errorf("code = %d (report %d), want %d", code, rep.Exit, tc.want)
			}
			if rep.OK {
				t.Error("report claims success on a failed run")
			}
			if rep.Error == nil || rep.Error.Kind != tc.kind {
				t.Errorf("error = %+v, want kind %q", rep.Error, tc.kind)
			}
			if rep.Error != nil && rep.Error.Message == "" {
				t.Error("error carries no message")
			}
			// The human channel still says what went wrong.
			if !strings.Contains(stderr, "taker: ") {
				t.Errorf("nothing on stderr: %q", stderr)
			}
			if _, err := os.Stat(filepath.Join(dir, "never.png")); err == nil {
				t.Error("a failed run wrote an image")
			}
		})
	}
}

// The warnings the renderer produces are the ones that change how the image has
// to be read, so they have to reach the document too — a consumer reading --json
// has no stderr to notice them on.
func TestRenderWarningsReachTheReport(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "x.py")
	if err := os.WriteFile(file, []byte("a=1\nSECRET=2\nSECRET=3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, rep, stderr := runJSON(t, func(r *reporter) int {
		return runSnap(snapCmd(SnapCmd{
			File: file, JSON: true, Lines: "1",
			Mark: []string{"SECRET"}, Output: filepath.Join(dir, "o.png"),
		}), r)
	})
	if len(rep.Warnings) != 1 {
		t.Fatalf("warnings = %q, want the marks left outside the view", rep.Warnings)
	}
	if !strings.Contains(rep.Warnings[0], "not in the") {
		t.Errorf("warning = %q", rep.Warnings[0])
	}
	if !strings.Contains(stderr, rep.Warnings[0]) {
		t.Errorf("the report and stderr disagree:\n report: %q\n stderr: %q", rep.Warnings[0], stderr)
	}
}

// Whatever the report says about a panel has to be what the panel shows: the
// numbers come from the render, not from re-deriving the trim here.
func TestHTTPReportDescribesBothPanels(t *testing.T) {
	dir := t.TempDir()
	req := filepath.Join(dir, "req.http")
	res := filepath.Join(dir, "res.http")
	if err := os.WriteFile(req, []byte("POST /x HTTP/1.1\nHost: a\n\nbody=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var resBody strings.Builder
	resBody.WriteString("HTTP/1.1 200 OK\n\n")
	for i := 1; i <= 100; i++ {
		resBody.WriteString("line\n")
	}
	if err := os.WriteFile(res, []byte(resBody.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	rep := newReporter(true, "http")
	var out bytes.Buffer
	rep.out = &out

	var code int
	captureStderr(t, func() {
		code = runHTTP(&HTTPCmd{
			Request: req, Response: res, JSON: true,
			ResHL: "50", Context: 2, Output: filepath.Join(dir, "o.png"),
		}, rep)
	})

	if code != exitOK {
		t.Fatalf("code = %d, want 0 (report: %+v)", code, rep.rep.Error)
	}
	var report Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Panels == nil {
		t.Fatal("no panels in the http report")
	}
	// The response is 102 lines long; two lines of context around line 50.
	if p := report.Panels.Response; p.First != 48 || p.Last != 52 || p.Total == nil || *p.Total != 102 {
		t.Errorf("response panel = %+v, want 48-52 of 102", p)
	}
	// The request has no highlight, so nothing was trimmed out of it.
	if p := report.Panels.Request; p.Total == nil || p.Shown != *p.Total {
		t.Errorf("request panel = %+v, want the whole untrimmed message", p)
	}
}

// Without --json the machine channel stays silent: the older contract (a human
// runs it, a human reads it) keeps working, and nothing is printed twice.
func TestWithoutJSONStdoutIsSilent(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "x.py")
	if err := os.WriteFile(file, []byte("a = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := newReporter(false, "snap")
	var out bytes.Buffer
	rep.out = &out

	var code int
	stderr := captureStderr(t, func() {
		code = runSnap(snapCmd(SnapCmd{File: file, Output: filepath.Join(dir, "o.png")}), rep)
	})

	if code != exitOK {
		t.Fatalf("code = %d", code)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want nothing without --json", out.String())
	}
	if !strings.Contains(stderr, "wrote") {
		t.Errorf("the human channel lost the confirmation: %q", stderr)
	}
}
