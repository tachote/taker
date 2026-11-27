package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
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
	rep := newReporter("json", "snap")
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
	if c.Chrome == "" {
		c.Chrome = "editor"
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

	rep := newReporter("json", "http")
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
	rep := newReporter("", "snap")
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

// splitCapture is what makes `taker http -f` accept a Burp "Save item" file:
// request then response in one capture, split on the status line.
func TestSplitCapture(t *testing.T) {
	req := "POST /x HTTP/1.1\nHost: a\n\n{}\n"
	res := "HTTP/1.1 200 OK\nContent-Length: 2\n\n{}\n"

	r1, r2 := splitCapture([]byte(req + res))
	if string(r1) != strings.TrimRight(req, "\n") || string(r2) != res {
		t.Errorf("pair split wrong:\nreq=%q\nres=%q", r1, r2)
	}
	if r1, r2 := splitCapture([]byte(res)); r1 != nil || string(r2) != res {
		t.Errorf("response-only file split = %q/%q", r1, r2)
	}
	if r1, r2 := splitCapture([]byte(req)); string(r1) != req || r2 != nil {
		t.Errorf("request-only file split = %q/%q", r1, r2)
	}
}

func TestCompileRegexRejectsBadPattern(t *testing.T) {
	if _, err := compileRegex("--mark-re", []string{"["}); err == nil {
		t.Error("bad pattern should be a usage error, not a render failure")
	}
	if _, err := compileRegex("--redact-re", []string{"("}); err == nil ||
		!strings.Contains(err.Error(), "--redact-re") {
		t.Error("the error should name the flag the pattern came from")
	}
}

// --dry-run validates the whole render — marks, trims, height — and reports
// the size the PNG would have, without the PNG.
func TestDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.py")
	if err := os.WriteFile(file, []byte("x = 1\ny = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.png")
	code, rep, _ := runJSON(t, func(r *reporter) int {
		return runSnap(snapCmd(SnapCmd{File: file, Output: out, DryRun: true, JSON: true}), r)
	})
	if code != exitOK || !rep.DryRun {
		t.Fatalf("dry run = %d, report %+v", code, rep)
	}
	if rep.Width == 0 || rep.Height == 0 {
		t.Error("dry run should still report the image's dimensions")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("dry run wrote the PNG it promised not to")
	}
	// "The same document": a preflight that omits the crop cannot be used to
	// check what the image would show. Compare against a real run's crop.
	trim := func(dry bool) *Crop {
		t.Helper()
		code, rep, _ := runJSON(t, func(r *reporter) int {
			return runSnap(snapCmd(SnapCmd{File: file, Output: out, Lines: "2", DryRun: dry, JSON: true}), r)
		})
		if code != exitOK {
			t.Fatalf("dry=%v run = %d", dry, code)
		}
		return rep.Crop
	}
	dry, real := trim(true), trim(false)
	if dry == nil || real == nil {
		t.Fatalf("crop: dry run %+v, real run %+v; want both present", dry, real)
	}
	if dry.First != 2 || dry.Last != 2 || dry.Shown != 1 || !reflect.DeepEqual(dry, real) {
		t.Errorf("dry-run crop = %+v, real-run crop = %+v; want both lines 2-2 of 2", dry, real)
	}
}

// The report carries the same provenance the PNG embeds: input hashes and the
// redaction count the footnote declares.
func TestReportCarriesProvenanceAndRedactions(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "s.go")
	src := "package a\nvar k = \"sekret-token\"\n"
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "o.png")
	code, rep, _ := runJSON(t, func(r *reporter) int {
		return runSnap(snapCmd(SnapCmd{File: file, Output: out, Redact: []string{"sekret-token"}, JSON: true}), r)
	})
	if code != exitOK {
		t.Fatalf("run = %d", code)
	}
	// Full needle plus its separator-split components: sekret-token splits
	// into "sekret" and "token", and each is a box the image really draws.
	if rep.Redactions != 3 {
		t.Errorf("redactions = %d, want 3 (needle + 2 fragments)", rep.Redactions)
	}
	// The hash covers the content AS MASKED — hashing the raw bytes would
	// ship a brute-force oracle for the needle the image hides. The %q twin
	// masks the quotes along with the needle.
	masked := strings.ReplaceAll(src, `"sekret-token"`, "<redacted>")
	if len(rep.SHA256) != 1 || rep.SHA256[0].Name != "source" || rep.SHA256[0].Sum != sha256Hex([]byte(masked)) {
		t.Errorf("sha256 = %+v, want the masked source's fingerprint", rep.SHA256)
	}
	if rep.Generated == "" {
		t.Error("no generated timestamp")
	}
	// The PNG embeds the same data: the image is self-describing when it
	// travels without the report.
	assertPNGAgreesWithReport(t, out, rep)
}

// `taker exec` runs the command and the report says so — source.kind, the
// command line as the path, and the rendered transcript.
func TestExecRunsAndReports(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "o.png")
	code, rep, _ := runJSON(t, func(r *reporter) int {
		return runExec(&ExecCmd{
			Args: []string{"echo", "hello-evidence"}, Output: out, Timeout: 5,
			Theme: "vscode-dark", Chrome: "window", WrapWidth: 80, JSON: true,
		}, r)
	})
	if code != exitOK {
		t.Fatalf("exec = %d, report %+v", code, rep)
	}
	if rep.Source == nil || rep.Source.Kind != "exec" || rep.Source.Path != "echo hello-evidence" {
		t.Errorf("source = %+v", rep.Source)
	}
	if len(rep.SHA256) == 0 || rep.SHA256[0].Name != "transcript" {
		t.Errorf("no transcript fingerprint in %+v", rep.SHA256)
	}
	if _, err := os.Stat(out); err != nil {
		t.Error("no PNG written")
	}
}

func TestExecRequiresACommand(t *testing.T) {
	code, rep, _ := runJSON(t, func(r *reporter) int {
		return runExec(&ExecCmd{Theme: "vscode-dark", Chrome: "window", WrapWidth: 80, JSON: true}, r)
	})
	if code != exitUsage || rep.Error == nil {
		t.Errorf("exec without a command = %d %+v, want a usage error", code, rep.Error)
	}
}

// A single-side HTTP run only reports the panel it rendered: an absent pane is
// different from a rendered pane whose extent is zero.
func TestHTTPPanelsAreOptional(t *testing.T) {
	dir := t.TempDir()
	req := filepath.Join(dir, "r.http")
	if err := os.WriteFile(req, []byte("GET / HTTP/1.1\nHost: x\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "o.png")
	code, rep, _ := runJSON(t, func(r *reporter) int {
		return runHTTP(&HTTPCmd{Request: req, Output: out, JSON: true}, r)
	})
	if code != exitOK {
		t.Fatalf("run = %d", code)
	}
	if rep.Panels == nil || rep.Panels.Request == nil || rep.Panels.Response != nil {
		t.Errorf("panels = %+v, want request only", rep.Panels)
	}
}

// The echoed command masks --redact values even on paths where parsing never
// produced a secrets slice — a flag that failed to parse still leaked its
// argument into the recorded argv.
// kong.Must panics on a malformed struct tag — a help string with an invalid
// escape once made the binary unstartable while every test stayed green.
// Building the parser here is the regression tripwire.
func TestCLIParses(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("kong tag panic: %v", r)
		}
	}()
	parser := newParser()
	if parser == nil {
		t.Fatal("nil parser")
	}
}

func TestScrubRedactArgs(t *testing.T) {
	for in, want := range map[string]string{
		`taker snap f.py --redact hunter2 -o x.png`:           `taker snap f.py --redact <redacted> -o x.png`,
		`taker snap f.py --redact=hunter2 -o x.png`:           `taker snap f.py --redact <redacted> -o x.png`,
		`taker term t.log -- --redact aa-bb-99`:               `taker term t.log -- --redact <redacted>`,
		`taker http -f x --redact-re 'eyJ[^ ]+' -s r`:         `taker http -f x --redact-re <redacted> -s r`,
		`taker snap f.py --mark hunter2 -o x.png`:             `taker snap f.py --mark hunter2 -o x.png`,
		`taker snap f.py --redact-keep 4 --redact a -o x.png`: `taker snap f.py --redact-keep 4 --redact <redacted> -o x.png`,
	} {
		if got := scrubRedactArgs(in); got != want {
			t.Errorf("scrubRedactArgs(%q) = %q, want %q", in, got, want)
		}
	}
}

// --redact hides the value in the image; it must also hide it in the
// provenance — a literal left in taker:command or the report's command field
// would leak the very thing the boxes mask.
func TestScrubSecrets(t *testing.T) {
	rep := newReporter("", "snap")
	rep.setSecrets([]string{"hunter2"})
	rep.rep.Command = `taker snap f.py --redact hunter2 -o out.png`
	rep.rep.Source = &Source{Kind: "exec", Path: "curl -H 'Bearer hunter2' x"}
	rep.metaKV = append(rep.metaKV,
		[2]string{"taker:command", rep.rep.Command},
		[2]string{"taker:exec", "curl -H 'Bearer hunter2' x"},
		[2]string{"taker:sha256", "abc123"})
	rep.rep.Warnings = append(rep.rep.Warnings, `taker: warning: --redact "hunter2" matched nothing`)
	rep.scrubSecrets()

	for _, got := range []string{
		rep.rep.Command, rep.rep.Source.Path,
		rep.metaKV[0][1], rep.metaKV[1][1], rep.rep.Warnings[0],
	} {
		if strings.Contains(got, "hunter2") {
			t.Errorf("secret survives in %q", got)
		}
		if !strings.Contains(got, "<redacted>") {
			t.Errorf("no <redacted> marker in %q", got)
		}
	}
	if rep.metaKV[2][1] != "abc123" {
		t.Error("scrubbing touched an unrelated chunk")
	}
}

// The same fact end-to-end: the PNG's metadata bytes carry no redact literal.
func TestRedactDoesNotLeakIntoPNG(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "c.py")
	secret := "wJalrXUtnFEMI7EXAMPLEKEY"
	if err := os.WriteFile(file, []byte("SECRET = \""+secret+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "o.png")
	code, rep, _ := runJSON(t, func(r *reporter) int {
		return runSnap(snapCmd(SnapCmd{File: file, Output: out, Redact: []string{secret}, JSON: true}), r)
	})
	if code != exitOK {
		t.Fatalf("run = %d", code)
	}
	if strings.Contains(rep.Command, secret) {
		t.Error("report command carries the redacted literal")
	}
	png, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(png, []byte(secret)) {
		t.Error("PNG metadata embeds the redacted literal — the box hides it, the tEXt leaks it")
	}
}

// A mark that matched nothing is a caveat: it lands in warnings[] so a
// pipeline sees it, not only on a stderr line nobody reads.
func TestMarkMatchedNothingLandsInWarnings(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.go")
	if err := os.WriteFile(file, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, rep, _ := runJSON(t, func(r *reporter) int {
		return runSnap(snapCmd(SnapCmd{File: file, Output: filepath.Join(dir, "o.png"), Mark: []string{"zz-not-here"}, JSON: true}), r)
	})
	if code != exitOK {
		t.Fatalf("run = %d", code)
	}
	if len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], "matched nothing") {
		t.Errorf("warnings = %v, want the zero-match caveat", rep.Warnings)
	}
}

// -f fed a non-HTTP file is an input error, not a confident Request pane.
// A capture with no response section renders the request with a warning.
func TestHTTPFValidation(t *testing.T) {
	dir := t.TempDir()
	junk := filepath.Join(dir, "nmap.txt")
	if err := os.WriteFile(junk, []byte("$ nmap -sV host\nPORT STATE\n80/tcp open\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, rep, _ := runJSON(t, func(r *reporter) int {
		return runHTTP(&HTTPCmd{File: junk, Output: filepath.Join(dir, "o.png"), JSON: true}, r)
	})
	if code != exitInput {
		t.Fatalf("garbage -f = %d, want input error", code)
	}

	only := filepath.Join(dir, "req.http")
	if err := os.WriteFile(only, []byte("GET /x HTTP/1.1\nHost: a\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, rep, _ = runJSON(t, func(r *reporter) int {
		return runHTTP(&HTTPCmd{File: only, Output: filepath.Join(dir, "o2.png"), JSON: true}, r)
	})
	if code != exitOK {
		t.Fatalf("request-only -f = %d", code)
	}
	found := false
	for _, w := range rep.Warnings {
		if strings.Contains(w, "no response section") {
			found = true
		}
	}
	if !found {
		t.Errorf("no 'no response section' warning in %v", rep.Warnings)
	}
}

// exec's provenance gets the same scrub: a token in --sh that --redact hides
// must not survive in source.path, the report command, or the taker:exec
// chunk embedded in the PNG.
func TestExecScrubsShellSecrets(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "o.png")
	code, rep, _ := runJSON(t, func(r *reporter) int {
		return runExec(&ExecCmd{
			Shell:  "echo Bearer topsecretvalue",
			Redact: []string{"topsecretvalue"},
			Output: out, Timeout: 5,
			Theme: "vscode-dark", Chrome: "window", WrapWidth: 80, JSON: true,
		}, r)
	})
	if code != exitOK {
		t.Fatalf("run = %d", code)
	}
	if rep.Source == nil || strings.Contains(rep.Source.Path, "topsecretvalue") {
		t.Errorf("source.path leaks the redacted literal: %+v", rep.Source)
	}
	if strings.Contains(rep.Command, "topsecretvalue") {
		t.Error("report command leaks the literal")
	}
	png, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(png, []byte("topsecretvalue")) {
		t.Error("the taker:exec chunk embeds the redacted literal")
	}
}

// The PNG's own taker:sha256 chunk must equal the report's sum. The hash is
// computed over content that is already masked, so it cannot carry a secret —
// scrubbing it a second time only destroyed the provenance: a hex --redact-re
// wiped it, and a short fragment cut a hole in the middle of it.
func TestPNGHashSurvivesRedaction(t *testing.T) {
	cases := []struct {
		name string
		cmd  SnapCmd
	}{
		{"hex pattern", SnapCmd{RedactRe: []string{`[0-9a-f]{32}`}}},
		{"short fragment", SnapCmd{Redact: []string{"k-e3-9f-a1-0c-d4"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "k.py")
			src := "api_key = \"9e107d9d372bb6826bd81d3542a419d6\"\nk = \"k-e3-9f-a1-0c-d4\"\n"
			if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(dir, "k.png")
			cmd := tc.cmd
			cmd.File, cmd.Output, cmd.JSON = file, out, true
			code, rep, _ := runJSON(t, func(r *reporter) int { return runSnap(snapCmd(cmd), r) })
			if code != exitOK || len(rep.SHA256) != 1 {
				t.Fatalf("run = %d, sha256 = %+v", code, rep.SHA256)
			}
			data, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if got := pngTextChunks(t, data)["taker:sha256"]; got != rep.SHA256[0].Sum {
				t.Errorf("PNG taker:sha256 = %q, report says %q", got, rep.SHA256[0].Sum)
			}
		})
	}
}

// Fragments shorter than three bytes carry almost no secret entropy and match
// ordinary code: "sk" from sk-ab12cdef9 used to black out ask, task and skip,
// and the caption came out as ta<redacted> <redacted>ip. Evidence that hides
// non-secret code is not faithful, so only the documented len >= 3 rule applies.
func TestShortFragmentsAreNotSecrets(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "app.py")
	src := "def ask(task):\n    token = \"sk-ab12cdef9\"\n    return os.environ[\"skip\"], task\n"
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	var caption string
	code, rep, _ := runJSON(t, func(r *reporter) int {
		c := runSnap(snapCmd(SnapCmd{
			File: file, Output: filepath.Join(dir, "a.png"), JSON: true,
			Redact: []string{"sk-ab12cdef9"}, Label: "task skip",
		}), r)
		caption = r.scrub("task skip")
		return c
	})
	if code != exitOK {
		t.Fatalf("run = %d", code)
	}
	// The needle and its one long component "ab12cdef9" — nothing else.
	if rep.Redactions != 2 {
		t.Errorf("redactions = %d, want 2 (needle + ab12cdef9)", rep.Redactions)
	}
	if caption != "task skip" {
		t.Errorf("caption scrubbed to %q, want it intact", caption)
	}
}

// bigSource is a few hundred KB of ordinary code holding one token — the
// size of a vendored or minified file an analyst renders an excerpt of. addInput
// scrubs the whole input, so the scrub must stay linear in it.
func bigSource(lines int) string {
	var b strings.Builder
	for i := 0; i < lines; i++ {
		if i == lines/2 {
			b.WriteString("    token = \"tok_9F3KQ7ZP2M\"  # the one secret\n")
			continue
		}
		b.WriteString("    result = compute_value(alpha, beta, gamma)  # filler\n")
	}
	return b.String()
}

func BenchmarkScrubRegex(b *testing.B) {
	r := newReporter("", "snap")
	r.setSecrets([]string{"hunter2-literal"})
	r.secretRes = []*regexp.Regexp{regexp.MustCompile(`tok_[A-Z0-9]+`)}
	s := bigSource(5000) // ~270 KB
	b.SetBytes(int64(len(s)))
	b.ReportAllocs()
	for range b.N {
		r.scrub(s)
	}
}

func BenchmarkScrubLiterals(b *testing.B) {
	r := newReporter("", "snap")
	r.setSecrets([]string{"tok_9F3KQ7ZP2M", "hunter2-literal", `a"quoted\secret`})
	s := bigSource(5000)
	b.SetBytes(int64(len(s)))
	b.ReportAllocs()
	for range b.N {
		r.scrub(s)
	}
}

// scrubSuffixRef is the scrub that asked each pattern for a match anchored at
// every byte of the remaining string — correct for context-free patterns but
// quadratic. It stays here as the reference the linear scrub must agree with.
func scrubSuffixRef(r *reporter, s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], "<redacted>") {
			b.WriteString("<redacted>")
			i += len("<redacted>")
			continue
		}
		t, best := s[i:], 0
		for _, sec := range r.secrets {
			if strings.HasPrefix(t, sec) && len(sec) > best {
				best = len(sec)
			}
			if q := strconv.Quote(sec); strings.HasPrefix(t, q) && len(q) > best {
				best = len(q)
			}
		}
		for _, re := range r.secretRes {
			if loc := re.FindStringIndex(t); loc != nil && loc[0] == 0 && loc[1] > best {
				best = loc[1]
			}
		}
		if best > 0 {
			b.WriteString("<redacted>")
			i += best
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// The linear scrub masks what the per-byte scan masked for patterns that do
// not look at context, including the overlaps where a literal or a marker ends
// inside a pattern match and the match's tail must still go. Where the two
// differ, want says what the linear scrub must produce instead, and why.
func TestScrubMatchesSuffixScan(t *testing.T) {
	cases := []struct {
		lits []string
		res  []string
		in   string
		want string
	}{
		{[]string{"hunter2"}, []string{`tok_[A-Z0-9]+`}, "a hunter2 b tok_AB12 c \"hunter2\" tok_ d", ""},
		{nil, []string{`[0-9a-f]{32}`}, "k = 9e107d9d372bb6826bd81d3542a419d6 and 9e107d9d372bb6826bd81d3542a419d6ff", ""},
		// A literal that is a prefix of a longer pattern match: the tail goes too.
		{[]string{"tok_AB"}, []string{`tok_[A-Z0-9]+`}, "x tok_AB12CD y", ""},
		// A literal that starts before a pattern match and ends inside it: the
		// per-byte scan resumed after the literal, found no "tok_" there and
		// left the match's tail _ZZ9 readable. The whole match is the secret.
		{[]string{"xx-tok"}, []string{`tok_[A-Z0-9]+`}, "a xx-tok_ZZ9 b", "a <redacted><redacted> b"},
		// A pattern that runs into an existing marker keeps masking past it.
		{nil, []string{`\S+`}, "<redacted>abc def", ""},
		{[]string{"a"}, []string{`a+`}, "baaab aaa", ""},
		{nil, []string{`(?s)BEGIN.*?END`}, "pre BEGIN\nkey\nEND post", ""},
		{[]string{"sekret-token"}, nil, `var k = "sekret-token"` + "\n", ""},
	}
	for _, tc := range cases {
		r := newReporter("", "snap")
		r.setSecrets(tc.lits)
		for _, p := range tc.res {
			r.secretRes = append(r.secretRes, regexp.MustCompile(p))
		}
		got, want := r.scrub(tc.in), tc.want
		if want == "" {
			want = scrubSuffixRef(r, tc.in)
		}
		if got != want {
			t.Errorf("scrub(%q) lits=%q res=%q\n got %q\nwant %q", tc.in, tc.lits, tc.res, got, want)
		}
		if again := r.scrub(got); again != got {
			t.Errorf("scrub is not idempotent: %q -> %q", got, again)
		}
	}
}

// A pattern now sees its real context, the same per-line context the image
// matches it in: ^ is a line start, not wherever the scan happens to be. The
// per-byte scan treated every position as a start of text, so it masked
// mid-line text the image left visible — and both must agree on what is a
// secret, or the input hash and the image tell different stories.
func TestScrubPatternContextMatchesTheImage(t *testing.T) {
	r := newReporter("", "snap")
	r.secretRes = []*regexp.Regexp{regexp.MustCompile(`^Authorization: \S+`)}
	in := "GET / HTTP/1.1\nAuthorization: Bearer-abc\nX-Note: see Authorization: x\n"
	want := "GET / HTTP/1.1\n<redacted>\nX-Note: see Authorization: x\n"
	if got := r.scrub(in); got != want {
		t.Errorf("scrub = %q, want %q", got, want)
	}
}

// The scrub runs over every input in full, so it must stay linear: the
// per-byte scan took ~10 s on this input.
func TestScrubIsLinear(t *testing.T) {
	r := newReporter("", "snap")
	r.secretRes = []*regexp.Regexp{regexp.MustCompile(`tok_[A-Z0-9]+`)}
	s := bigSource(5000)
	start := time.Now()
	got := r.scrub(s)
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("scrub of %d bytes took %v", len(s), d)
	}
	if strings.Contains(got, "tok_9F3KQ7ZP2M") || strings.Count(got, "<redacted>") != 1 {
		t.Error("the one token was not masked exactly once")
	}
}

// The JSON report prints <, > and & as themselves: the README promises values
// "replaced by <redacted>", and a consumer grepping for that marker — or for
// the && of the shell line it ran — found \u003credacted\u003e instead.
func TestJSONReportDoesNotEscapeHTML(t *testing.T) {
	rep := newReporter("json", "exec")
	var out bytes.Buffer
	rep.out = &out
	rep.rep.Command = `taker exec --sh cat a.sh && echo "<ok>" --redact hunter2`
	rep.setSecrets([]string{"hunter2"})
	rep.rep.OK = true
	rep.emit(exitOK)
	got := out.String()
	for _, want := range []string{"<redacted>", `&& echo \"<ok>\"`} {
		if !strings.Contains(got, want) {
			t.Errorf("json report lacks the literal %q:\n%s", want, got)
		}
	}
	if !strings.HasSuffix(got, "}\n") || strings.HasSuffix(got, "\n\n") {
		t.Errorf("json report should end in exactly one newline: %q", got[max(0, len(got)-8):])
	}
	var back Report
	if err := json.Unmarshal(out.Bytes(), &back); err != nil || back.Command != rep.rep.Command {
		t.Errorf("unescaped report does not decode back: %v %q", err, back.Command)
	}
}

// A detached PNG has to prove its version by machine, not only in footnote
// pixels: the revision and the source path travel as metadata next to the
// hash, and they say what the report says.
func TestPNGCarriesSourceAndRevision(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "auth.py"), []byte("def login():\n    return 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-qm", "add auth")

	out := filepath.Join(dir, "g.png")
	code, rep, _ := runJSON(t, func(r *reporter) int {
		return runSnap(snapCmd(SnapCmd{Git: "HEAD:auth.py", RepoDir: dir, Output: out, JSON: true}), r)
	})
	if code != exitOK || rep.Source == nil || rep.Source.Rev == "" {
		t.Fatalf("run = %d, source = %+v", code, rep.Source)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	meta := pngTextChunks(t, data)
	if meta["taker:git-revision"] != rep.Source.Rev {
		t.Errorf("taker:git-revision = %q, report rev = %q", meta["taker:git-revision"], rep.Source.Rev)
	}
	if meta["taker:source"] != "git:auth.py" {
		t.Errorf("taker:source = %q, want git:auth.py", meta["taker:source"])
	}

	// A file input names its path; a redacted needle in it stays masked in
	// the metadata exactly as it is in the report.
	file := filepath.Join(dir, "hunter2-dump.py")
	if err := os.WriteFile(file, []byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out = filepath.Join(dir, "f.png")
	code, rep, _ = runJSON(t, func(r *reporter) int {
		return runSnap(snapCmd(SnapCmd{File: file, Output: out, Redact: []string{"hunter2"}, JSON: true}), r)
	})
	if code != exitOK {
		t.Fatalf("run = %d", code)
	}
	if data, err = os.ReadFile(out); err != nil {
		t.Fatal(err)
	}
	meta = pngTextChunks(t, data)
	if got := meta["taker:source"]; got != "file:"+rep.Source.Path || strings.Contains(got, "hunter2") {
		t.Errorf("taker:source = %q, report path = %q", got, rep.Source.Path)
	}
	if _, ok := meta["taker:git-revision"]; ok {
		t.Error("a plain file claims a git revision")
	}
}

// assertPNGAgreesWithReport checks the contract between the two channels:
// every taker:sha256[:name] chunk in the image equals the report's entry for
// that input, and the image carries no hash the report does not. A test that
// only looked for the bytes "taker:sha256" let a wiped sum pass.
func assertPNGAgreesWithReport(t *testing.T, pngPath string, rep Report) {
	t.Helper()
	data, err := os.ReadFile(pngPath)
	if err != nil {
		t.Fatal(err)
	}
	meta := pngTextChunks(t, data)
	want := map[string]string{}
	for _, h := range rep.SHA256 {
		key := "taker:sha256"
		if h.Name != "source" {
			key += ":" + h.Name
		}
		want[key] = h.Sum
	}
	if len(want) == 0 {
		t.Fatal("the report carries no input hash")
	}
	for key, sum := range want {
		if meta[key] != sum {
			t.Errorf("PNG %s = %q, report says %q", key, meta[key], sum)
		}
	}
	for key := range meta {
		if strings.HasPrefix(key, "taker:sha256") && want[key] == "" {
			t.Errorf("PNG carries %s, which the report does not", key)
		}
	}
	if meta["taker:version"] != rep.Version || meta["taker:generated"] != rep.Generated {
		t.Errorf("PNG version/generated = %q/%q, report %q/%q",
			meta["taker:version"], meta["taker:generated"], rep.Version, rep.Generated)
	}
}

// The PNG and the report agree in every mode, with and without a pattern
// that matches hex — the shape every hash has.
func TestPNGAndReportAgreeInEveryMode(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	code := write("k.py", "api_key = \"9e107d9d372bb6826bd81d3542a419d6\"\n")
	req := write("req.http", "GET /x HTTP/1.1\nHost: a\nX-Key: 9e107d9d372bb6826bd81d3542a419d6\n\n")
	res := write("res.http", "HTTP/1.1 200 OK\n\n{\"ok\":true}\n")

	for _, re := range [][]string{nil, {`[0-9a-f]{32}`, `[0-9a-f]{8,}`}} {
		name := "plain"
		if re != nil {
			name = "redact-re"
		}
		runs := map[string]func(r *reporter, out string) int{
			"snap": func(r *reporter, out string) int {
				return runSnap(snapCmd(SnapCmd{File: code, Output: out, RedactRe: re, JSON: true}), r)
			},
			"http": func(r *reporter, out string) int {
				return runHTTP(&HTTPCmd{Request: req, Response: res, Output: out, RedactRe: re, JSON: true}, r)
			},
			"exec": func(r *reporter, out string) int {
				return runExec(&ExecCmd{
					Args: []string{"echo", "9e107d9d372bb6826bd81d3542a419d6"}, Output: out, Timeout: 5,
					RedactRe: re, Theme: "vscode-dark", Chrome: "window", WrapWidth: 80, JSON: true,
				}, r)
			},
		}
		for mode, run := range runs {
			t.Run(mode+"/"+name, func(t *testing.T) {
				out := filepath.Join(dir, mode+"-"+name+".png")
				code, rep, _ := runJSON(t, func(r *reporter) int { return run(r, out) })
				if code != exitOK {
					t.Fatalf("run = %d, error %+v", code, rep.Error)
				}
				assertPNGAgreesWithReport(t, out, rep)
			})
		}
	}
}

var updateGolden = flag.Bool("update", false, "rewrite testdata/report.golden.* from the current output")

// The report's field order is a contract (toon.go says so): a consumer that
// reads TOON tables or diffs JSON depends on it, and the README drifted from
// it once already. This pins one realistic run byte for byte in both
// encodings. Values that legitimately vary — the temp dir, the PNG's size and
// dimensions, which follow the renderer — are normalized out; run
// `go test -run TestReportGolden -update` after a deliberate schema change.
func TestReportGolden(t *testing.T) {
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	// The report's output path is absolute, joined onto the working directory
	// as the OS reports it (/private/var for /var on macOS) — normalize that.
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	src := "def login(user):\n    key = \"sekret-token\"\n    return db.query(user)\n"
	if err := os.WriteFile("auth.py", []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	volatile := regexp.MustCompile(`(?m)("?(?:bytes|width|height)"?: )\d+`)

	for _, format := range []string{"json", "toon"} {
		os.Remove("out.png") // each run writes fresh, without an overwrite warning
		rep := newReporter(format, "snap")
		var out bytes.Buffer
		rep.out = &out
		rep.rep.Version = "golden"
		rep.rep.Generated = "2026-01-01T00:00:00Z"
		rep.rep.Command = "taker snap auth.py -o out.png -m db.query -m nowhere --redact sekret-token --" + format
		captureStderr(t, func() {
			runSnap(snapCmd(SnapCmd{
				File: "auth.py", Output: "out.png", Context: 1,
				Mark: []string{"db.query", "nowhere"}, Redact: []string{"sekret-token"},
			}), rep)
		})
		got := strings.ReplaceAll(out.String(), root, "$DIR")
		got = volatile.ReplaceAllString(got, "${1}0")

		golden := filepath.Join(wd, "testdata", "report.golden."+format)
		if *updateGolden {
			if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("%v (run with -update to create it)", err)
		}
		if got != string(want) {
			t.Errorf("%s report drifted from %s:\n--- got\n%s\n--- want\n%s", format, golden, got, want)
		}
	}
}
