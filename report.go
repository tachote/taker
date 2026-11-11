package main

import (
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"os"
	"path/filepath"

	"github.com/tachote/taker/highlight"
)

// Exit codes are part of this tool's contract, not an implementation detail: the
// consumers of --json branch on them instead of reading prose, so each class
// keeps its number even when a message changes. Everything unclassified stays 1,
// which keeps the older promise — non-zero means the evidence was not produced —
// true for every failure.
const (
	exitOK      = 0 // the PNG is on disk
	exitFail    = 1 // the evidence could not be written: output path, encoding, disk
	exitUsage   = 2 // the command itself was wrong: unknown flag, bad value, bad combination
	exitInput   = 3 // the input cannot be used as given: unreadable, empty, a directory, no such revision
	exitRefused = 4 // a documented limit refused the render: too tall, nothing matched
)

// Error kinds, mirroring the exit codes for consumers that prefer a name.
const (
	kindUsage   = "usage"
	kindInput   = "input"
	kindRefused = "refused"
	kindOutput  = "output"
)

// Report is the document --json writes to stdout. It answers the questions a
// consumer actually has — which file, from which revision, what the image shows,
// what each mark found, what was refused — so an agent can assert on the run
// instead of reading the messages a human would read.
type Report struct {
	Tool     string        `json:"tool"`
	Version  string        `json:"version"`
	Mode     string        `json:"mode"`
	OK       bool          `json:"ok"`
	Exit     int           `json:"exit"`
	Output   string        `json:"output,omitempty"`
	Bytes    int64         `json:"bytes,omitempty"`
	Width    int           `json:"width,omitempty"`
	Height   int           `json:"height,omitempty"`
	Source   *Source       `json:"source,omitempty"`
	Crop     *Crop         `json:"crop,omitempty"`
	Panels   *Panels       `json:"panels,omitempty"`
	Marks    []MarkReport  `json:"marks,omitempty"`
	Themes   []ThemeReport `json:"themes,omitempty"`
	Warnings []string      `json:"warnings"`
	Error    *ReportError  `json:"error,omitempty"`
}

// Source says where the rendered bytes came from. Rev is the short commit when
// the code was read out of a revision with --git, which is the only case where
// the image can prove which version it shows.
type Source struct {
	Kind string `json:"kind"`
	Path string `json:"path,omitempty"`
	Rev  string `json:"rev,omitempty"`
}

// Crop is the extent of the view the image shows, in the source's own numbering.
// Total is null when the input did not start at line 1: the tool does not know
// how long the real file is, and a wrong total is worse than none.
type Crop struct {
	First int  `json:"first"`
	Last  int  `json:"last"`
	Shown int  `json:"shown"`
	Total *int `json:"total"`
}

// Panels carries one Crop per pane for `taker http`. Unlike an excerpt, a whole
// message is always in hand, so Total is never null there.
type Panels struct {
	Request  Crop `json:"request"`
	Response Crop `json:"response"`
}

// MarkReport is one --mark literal's outcome. Kept and Dropped are what the
// image shows versus what a limit left out, so a caller can tell a mark that
// landed where it wanted from one that only half landed.
type MarkReport struct {
	Needle  string `json:"needle"`
	Matches int    `json:"matches"`
	Lines   int    `json:"lines"`
	Kept    int    `json:"kept"`
	Dropped int    `json:"dropped"`
}

// ThemeReport is one palette, for --list-themes --json.
type ThemeReport struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	HTTPOnly bool   `json:"httpOnly"`
}

// ReportError names why the evidence was not produced.
type ReportError struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// reporter owns the two output channels of one run: the human lines on stderr,
// and the report on stdout that --json switches on. Every way out of a command
// goes through it, so a consumer that asked for JSON always gets a document —
// including the runs that failed, which are the ones it most needs to classify.
type reporter struct {
	out      io.Writer // stdout: the report, the theme list, nothing else
	machines bool      // --json
	rep      Report
}

func newReporter(jsonOut bool, mode string) *reporter {
	return &reporter{
		out:      os.Stdout,
		machines: jsonOut,
		rep: Report{
			Tool:     "taker",
			Version:  version,
			Mode:     mode,
			Warnings: []string{},
		},
	}
}

// note prints a human line that the report does not need to carry.
func (r *reporter) note(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "taker: "+format+"\n", args...)
}

// warn prints a line that changes how the image has to be read — a clamped
// width, an ignored flag, a mark that landed outside the view, a character the
// font cannot draw — and records it, so a consumer sees the same caveats the
// analyst does. A warning is not a failure.
//
// The label is uniform on purpose: a reader scanning a terminal, a log or the
// report's warnings array has to be able to tell a caveat from an ordinary line,
// and the same text travels in all three.
func (r *reporter) warn(format string, args ...any) {
	msg := fmt.Sprintf("warning: "+format, args...)
	r.note("%s", msg)
	r.rep.Warnings = append(r.rep.Warnings, "taker: "+msg)
}

// fail reports why the evidence was not produced and returns the exit code for
// the caller to hand to os.Exit. Failing without a report would leave a
// consumer grepping prose for the reason it already asked for in JSON.
func (r *reporter) fail(code int, kind, format string, args ...any) int {
	msg := fmt.Sprintf(format, args...)
	r.note("%s", msg)
	r.rep.OK = false
	r.rep.Exit = code
	r.rep.Error = &ReportError{Kind: kind, Message: msg}
	return r.emit(code)
}

// done reads the image back off disk and records what was actually written —
// not what the renderer was asked for, which is the difference a report exists
// to keep.
func (r *reporter) done(path string) int {
	r.rep.OK = true
	r.rep.Exit = exitOK
	if abs, err := filepath.Abs(path); err == nil {
		r.rep.Output = abs
	} else {
		r.rep.Output = path
	}
	if fi, err := os.Stat(path); err == nil {
		r.rep.Bytes = fi.Size()
	}
	if f, err := os.Open(path); err == nil {
		if cfg, err := png.DecodeConfig(f); err == nil {
			r.rep.Width, r.rep.Height = cfg.Width, cfg.Height
		}
		f.Close()
	}
	return r.emit(exitOK)
}

// emit writes the report when --json was asked for, and always returns the code
// so callers can pass it straight to os.Exit.
func (r *reporter) emit(code int) int {
	if !r.machines {
		return code
	}
	buf, err := json.MarshalIndent(r.rep, "", "  ")
	if err != nil {
		r.note("could not encode the json report: %v", err)
		return code
	}
	fmt.Fprintln(r.out, string(buf))
	return code
}

// recordMarks puts the per-literal counts in the report, and says them out loud:
// a mark is a guess about where the finding is, and an image where that guess
// landed on 25 lines looks exactly as correct as one where it landed on the
// vulnerable line. The count is the only thing that tells them apart.
func recordMarks(rep *reporter, stats []highlight.MarkStats) {
	for _, st := range stats {
		rep.note("%s", st.Message())
		rep.rep.Marks = append(rep.rep.Marks, MarkReport{
			Needle:  st.Literal,
			Matches: st.Matches,
			Lines:   st.Lines,
			Kept:    st.Matches - st.Dropped,
			Dropped: st.Dropped,
		})
	}
}

// cropOf turns a view's extent into the report's shape. A total of zero means
// the input was an excerpt, and the field goes out as null rather than as a
// number nobody measured.
func cropOf(ext highlight.ViewExtent, total int) Crop {
	c := Crop{First: ext.First, Last: ext.Last, Shown: ext.Shown}
	if total > 0 {
		c.Total = &total
	}
	return c
}
