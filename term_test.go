package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// termCmd fills the defaults the flag tags would, so the test calls what the
// CLI would call.
func termCmd(c TermCmd) *TermCmd {
	if c.Theme == "" {
		c.Theme = "vscode-dark"
	}
	if c.Chrome == "" {
		c.Chrome = "window"
	}
	if c.Title == "" {
		c.Title = "Terminal"
	}
	if c.WrapWidth == 0 {
		c.WrapWidth = 80
	}
	if c.Output == "" {
		c.Output = "output.png"
	}
	return &c
}

func writeTranscript(t *testing.T, body string) (dir, file string) {
	t.Helper()
	dir = t.TempDir()
	file = filepath.Join(dir, "session.log")
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, file
}

func hasWarning(rep Report, sub string) bool {
	for _, w := range rep.Warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

// A written transcript reports what it shows: every line, a known total, the
// transcript's fingerprint, and a PNG on disk.
func TestTermReportDescribesTheImage(t *testing.T) {
	dir, file := writeTranscript(t, "$ id\nuid=0(root) gid=0(root)\n")
	out := filepath.Join(dir, "o.png")
	code, rep, _ := runJSON(t, func(r *reporter) int {
		return runTerm(termCmd(TermCmd{File: file, Output: out, JSON: true}), r)
	})
	if code != exitOK || !rep.OK {
		t.Fatalf("term = %d, report %+v", code, rep)
	}
	if rep.Source == nil || rep.Source.Kind != "transcript" || rep.Source.Path != file {
		t.Errorf("source = %+v, want the transcript it read", rep.Source)
	}
	if rep.Crop == nil || rep.Crop.First != 1 || rep.Crop.Last != 2 || rep.Crop.Shown != 2 ||
		rep.Crop.Total == nil || *rep.Crop.Total != 2 {
		t.Errorf("crop = %+v, want lines 1-2 of 2", rep.Crop)
	}
	if len(rep.SHA256) != 1 || rep.SHA256[0].Name != "transcript" {
		t.Errorf("sha256 = %+v, want the transcript fingerprint", rep.SHA256)
	}
	if rep.Redactions != 0 || len(rep.Warnings) != 0 {
		t.Errorf("redactions=%d warnings=%v, want a clean run", rep.Redactions, rep.Warnings)
	}
	if fi, err := os.Stat(out); err != nil || fi.Size() != rep.Bytes {
		t.Errorf("bytes = %d, want the size of the PNG written", rep.Bytes)
	}
}

// --dry-run validates and sizes the image without writing it.
func TestTermDryRunWritesNothing(t *testing.T) {
	dir, file := writeTranscript(t, "$ id\nuid=0(root) gid=0(root)\n")
	out := filepath.Join(dir, "o.png")
	code, rep, _ := runJSON(t, func(r *reporter) int {
		return runTerm(termCmd(TermCmd{File: file, Output: out, DryRun: true, JSON: true}), r)
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
}

// doneDry promises the preflight checked "everything the image would claim —
// marks, crops, height", so the crop belongs in a dry-run report too.
func TestTermDryRunCarriesTheCrop(t *testing.T) {
	_, file := writeTranscript(t, "$ id\nuid=0(root) gid=0(root)\n")
	_, rep, _ := runJSON(t, func(r *reporter) int {
		return runTerm(termCmd(TermCmd{File: file, DryRun: true, JSON: true}), r)
	})
	if rep.Crop == nil || rep.Crop.Shown != 2 {
		t.Errorf("crop = %+v, want lines 1-2 of 2", rep.Crop)
	}
}

// Escape codes draw nothing on a real terminal; stripping them changes the
// bytes, so the run says it did.
func TestTermWarnsWhenItStripsANSI(t *testing.T) {
	_, file := writeTranscript(t, "$ id\n\x1b[31muid=0(root)\x1b[0m\n")
	code, rep, _ := runJSON(t, func(r *reporter) int {
		return runTerm(termCmd(TermCmd{File: file, DryRun: true, JSON: true}), r)
	})
	if code != exitOK {
		t.Fatalf("term = %d", code)
	}
	if !hasWarning(rep, "stripped 2 ANSI escape sequences") {
		t.Errorf("warnings = %v, want the ANSI strip caveat", rep.Warnings)
	}
}

// A mark that found nothing looks like a correct image; the warning is the
// only thing that tells them apart.
func TestTermMarkMissIsAWarning(t *testing.T) {
	_, file := writeTranscript(t, "$ id\nuid=0(root)\n")
	code, rep, _ := runJSON(t, func(r *reporter) int {
		return runTerm(termCmd(TermCmd{File: file, DryRun: true, JSON: true, Mark: []string{"not-in-output"}}), r)
	})
	if code != exitOK {
		t.Fatalf("term = %d", code)
	}
	if len(rep.Marks) != 1 || rep.Marks[0].Matches != 0 {
		t.Errorf("marks = %+v, want one zero-match mark", rep.Marks)
	}
	if !hasWarning(rep, "matched nothing") {
		t.Errorf("warnings = %v, want the zero-match caveat", rep.Warnings)
	}
}

// --redact boxes the value in the image and keeps it out of every channel:
// report, stderr, PNG metadata. A redact that missed is a warning, and the
// warning masks the needle it names.
func TestTermRedactHidesTheValueEverywhere(t *testing.T) {
	const secret = "tok_Zq81LmX0"
	const missed = "NOPEmissing9931"
	dir, file := writeTranscript(t, "$ env\nTOKEN="+secret+"\n")
	out := filepath.Join(dir, "o.png")
	code, rep, stderr := runJSON(t, func(r *reporter) int {
		r.rep.Command = "taker term session.log --redact " + secret + " --redact=" + missed
		return runTerm(termCmd(TermCmd{
			File: file, Output: out, JSON: true,
			Redact: []string{secret, missed}, Label: "token " + secret,
		}), r)
	})
	if code != exitOK {
		t.Fatalf("term = %d", code)
	}
	if rep.Redactions == 0 {
		t.Error("redactions = 0, want the token boxed")
	}
	if !hasWarning(rep, "matched nothing") {
		t.Errorf("warnings = %v, want the missed-redact caveat", rep.Warnings)
	}
	for _, s := range []string{secret, missed} {
		if strings.Contains(rep.Command, s) {
			t.Errorf("report command carries %q: %s", s, rep.Command)
		}
		if strings.Contains(stderr, s) {
			t.Errorf("stderr carries %q:\n%s", s, stderr)
		}
		for _, w := range rep.Warnings {
			if strings.Contains(w, s) {
				t.Errorf("warning carries %q: %s", s, w)
			}
		}
	}
	png, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(png, []byte(secret)) {
		t.Error("the PNG's metadata embeds the redacted literal")
	}
}

// A missing transcript is an input error with its own exit code, not a
// render failure.
func TestTermMissingFileIsInputError(t *testing.T) {
	code, rep, _ := runJSON(t, func(r *reporter) int {
		return runTerm(termCmd(TermCmd{File: filepath.Join(t.TempDir(), "nope.log"), JSON: true}), r)
	})
	if code != exitInput || rep.Error == nil || rep.Error.Kind != kindInput {
		t.Errorf("missing transcript = %d %+v, want an input error", code, rep.Error)
	}
}

// A terminal sets tab stops every 8 columns, so a table ls or ps aligned
// there must stay aligned in the image. Snap's 4 shifted every column.
func TestTranscriptTabsStopEvery8(t *testing.T) {
	theme, err := resolveTheme("vscode-dark", "a transcript")
	if err != nil {
		t.Fatal(err)
	}
	lines, err := tokenizeTranscript("ab\tc\n12345678\td\n", "", theme)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ab      c", "12345678        d"}
	for i, l := range lines {
		var b strings.Builder
		for _, tok := range l.Tokens {
			b.WriteString(tok.Text)
		}
		if b.String() != want[i] {
			t.Errorf("line %d = %q, want %q", i+1, b.String(), want[i])
		}
	}
}

// A transcript has no --context or line range, so the height refusal and the
// tall-image warning must name what does shorten it — and the report must
// carry a next command, the way exec's output cap does.
func TestTerminalHeightHintFitsTheMode(t *testing.T) {
	var long, mid strings.Builder
	for i := 1; i <= 2000; i++ {
		fmt.Fprintf(&long, "line %d\n", i)
		if i <= 300 {
			fmt.Fprintf(&mid, "line %d\n", i)
		}
	}
	_, file := writeTranscript(t, long.String())
	code, rep, _ := runJSON(t, func(r *reporter) int {
		return runTerm(termCmd(TermCmd{File: file, DryRun: true, JSON: true}), r)
	})
	if code != exitRefused || rep.Error == nil {
		t.Fatalf("term of 2000 lines = %d %+v, want a refusal", code, rep.Error)
	}
	if strings.Contains(rep.Error.Message, "--context") || !strings.Contains(rep.Error.Message, "trim the transcript") {
		t.Errorf("error.message = %q, want the transcript hint and no --context", rep.Error.Message)
	}
	if !strings.Contains(strings.Join(rep.Help, "\n"), "head -n 200") {
		t.Errorf("help = %q, want a head -n 200 recipe", rep.Help)
	}

	_, file = writeTranscript(t, mid.String())
	code, rep, _ = runJSON(t, func(r *reporter) int {
		return runTerm(termCmd(TermCmd{File: file, DryRun: true, JSON: true}), r)
	})
	if code != exitOK || !hasWarning(rep, "tall") || hasWarning(rep, "--context") {
		t.Errorf("term of 300 lines = %d, warnings %q: want a tall warning without --context", code, rep.Warnings)
	}

	if runtime.GOOS == "windows" {
		return
	}
	code, rep, _ = runJSON(t, func(r *reporter) int {
		return runExec(execCmd(ExecCmd{Shell: "seq 1 2000", DryRun: true}), r)
	})
	if code != exitRefused || rep.Error == nil || strings.Contains(rep.Error.Message, "--context") {
		t.Fatalf("exec seq 1 2000 = %d %+v, want a refusal without --context", code, rep.Error)
	}
	if !strings.Contains(strings.Join(rep.Help, "\n"), "taker exec --sh 'seq 1 2000 | head -n 200'") {
		t.Errorf("help = %q, want the exec --sh head recipe", rep.Help)
	}
}
