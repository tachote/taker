package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

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
	Tool    string `json:"tool"`
	Version string `json:"version"`
	Mode    string `json:"mode"`
	OK      bool   `json:"ok"`
	Exit    int    `json:"exit"`
	Output  string `json:"output,omitempty"`
	Bytes   int64  `json:"bytes,omitempty"`
	Width   int    `json:"width,omitempty"`
	Height  int    `json:"height,omitempty"`
	// Command is the argv line that produced the image and Generated the UTC
	// time — provenance a reviewer can check without trusting the caption.
	Command   string       `json:"command,omitempty"`
	Generated string       `json:"generated,omitempty"`
	DryRun    bool         `json:"dry_run,omitempty"`
	Source    *Source      `json:"source,omitempty"`
	Crop      *Crop        `json:"crop,omitempty"`
	Panels    *Panels      `json:"panels,omitempty"`
	SHA256    []SourceHash `json:"sha256,omitempty"`
	// Redactions is how many black boxes the image carries. A value of zero
	// means nothing was masked; a consumer can require non-zero for captures
	// that are supposed to hide credentials.
	Redactions int           `json:"redactions"`
	Marks      []MarkReport  `json:"marks,omitempty"`
	Themes     []ThemeReport `json:"themes,omitempty"`
	// Exec carries what the executed command did — taker's own exit says the
	// evidence was produced, this says what the command under test answered.
	Exec     *ExecReport `json:"exec,omitempty"`
	Warnings []string    `json:"warnings"`
	// Help carries the next command that unblocks or completes this run, per
	// AXI's contextual disclosure: a consumer that hit a usage wall, a trim
	// that ate the mark, or a dry run gets the fix instead of a manual.
	Help  []string     `json:"help,omitempty"`
	Error *ReportError `json:"error,omitempty"`
}

// SourceHash names one rendered input and its fingerprint: "source" for a
// code file, "request"/"response" for an HTTP pane, "transcript" for `taker
// exec` output. The same pairs travel inside the PNG as tEXt chunks.
type SourceHash struct {
	Name string `json:"name"`
	Sum  string `json:"sum"`
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

// Panels carries one Crop per rendered pane for `taker http`. A pane is absent
// when the run only had one message — `taker http -r req.http` produces no
// response key — so a consumer can tell a trimmed panel from one that never
// existed. Unlike an excerpt, a whole message is always in hand, so Total is
// never null there.
type Panels struct {
	Request  *Crop `json:"request,omitempty"`
	Response *Crop `json:"response,omitempty"`
}

// MarkReport is one --mark literal's outcome, counted over the whole input.
// Kept and Dropped split the matches by --max-marks: the ones it kept versus
// the ones it left out. Shown is how many kept matches sit on a line the image
// shows — a --lines or --context trim can leave a kept match out of the view,
// and kept=1 with no box in the image is the claim a consumer must not trust.
// It comes last so a TOON table's existing columns keep their positions.
type MarkReport struct {
	Needle  string `json:"needle"`
	Matches int    `json:"matches"`
	Lines   int    `json:"lines"`
	Kept    int    `json:"kept"`
	Dropped int    `json:"dropped"`
	Shown   int    `json:"shown"`
}

// ExecReport records the child command's outcome for `taker exec` — the
// footnote in the PNG shows it, and the report carries the same fact as data,
// so a consumer asserting "the PoC ran" is not guessing from a screenshot.
type ExecReport struct {
	Exit     *int `json:"exit"`
	TimedOut bool `json:"timed_out,omitempty"`
	// Signal names the signal that ended the command (SIGKILL, SIGSEGV),
	// where exit is null: a signal is not an exit code, and the -1 Go
	// reports for it read like one.
	Signal string `json:"signal,omitempty"`
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
// and the report on stdout. format picks the stdout encoding — "" for none on
// success, "json" for --json, "toon" for --toon — but a failure always emits a
// document, because a caller that greps an empty stdout for why a run died has
// nothing to act on. Every way out of a command goes through it, so a consumer
// always gets something it can classify.
type reporter struct {
	out    io.Writer // stdout: the report, the theme list, nothing else
	format string    // "", "json" or "toon"
	rep    Report
	metaKV [][2]string // PNG text chunks beyond the always-present set
	// secrets are the --redact literals of this run: the image hides them,
	// so every string that leaves the process — warnings, help, errors,
	// provenance — hides them too. Set as early as the flags are known.
	secrets []string
	// secretRes are the compiled --redact-re patterns: a pattern's literal
	// text is already a secret, but its MATCHES are secrets it does not
	// name — a token caught by the regex in a filename only stays hidden if
	// the pattern itself scrubs the strings it touches.
	secretRes []*regexp.Regexp
	// argv is the raw command line, when main knows it. Command is rebuilt
	// from it on every scrub — each word masked before it is quoted — so a
	// secret that holds a quote cannot hide from the scrub behind the quoting.
	argv []string
	// fragOnly holds the needles' separator-split components — for the pixel
	// redaction, so a needle spliced by quotes or escapes leaves no readable
	// fragment behind. The image masks them only as whole tokens (see
	// redactFragRes).
	fragOnly []string
}

func newReporter(format, mode string) *reporter {
	return &reporter{
		out:    os.Stdout,
		format: format,
		rep: Report{
			Tool:      "taker",
			Version:   version,
			Mode:      mode,
			Generated: time.Now().UTC().Format(time.RFC3339),
			Warnings:  []string{},
		},
	}
}

// addInput records one input's fingerprint in the report and in the PNG's own
// metadata, so the image and the JSON say the same thing about its bytes.
// The hash covers the content AFTER the declared needles are masked — hashing
// the raw bytes would ship a brute-force oracle: the image shows the file's
// whole structure, so sha256(visible % candidate) would verify guesses at
// low-entropy secrets. An auditor reproduces the hash by masking first.
func (r *reporter) addInput(name string, data []byte) {
	sum := sha256Hex([]byte(r.scrub(string(data))))
	r.rep.SHA256 = append(r.rep.SHA256, SourceHash{Name: name, Sum: sum})
	key := "taker:sha256"
	if name != "source" {
		key += ":" + name
	}
	r.metaKV = append(r.metaKV, [2]string{key, sum})
}

// setSecrets registers the --redact literals; every channel that formats a
// message from then on scrubs them — including Command, which was already
// recorded, so it is scrubbed right here.
func (r *reporter) setSecrets(secrets []string) { r.setSecretsAll(secrets, nil) }

// setSecretsAll registers literals and patterns; fragments come from the
// literals only — a pattern like eyJ[...]+ split on '-' would paint boxes
// over arbitrary code text.
func (r *reporter) setSecretsAll(secrets, literals []string) {
	// Dedupe, then also register each needle's separator-split components
	// (len >= 3): a second-stage parser that fails echoes only a fragment —
	// strconv.Atoi on "ZX9-liveroot-8842" reports parsing "ZX9" — and the
	// full needle never matches the fragment it echoes.
	seen := map[string]bool{}
	var expanded []string
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			expanded = append(expanded, s)
		}
	}
	var frags []string
	for _, sec := range secrets {
		add(sec)
	}
	for _, sec := range literals {
		for _, part := range strings.FieldsFunc(sec, func(r rune) bool {
			return strings.ContainsRune("-:=,/.; \t", r)
		}) {
			if len(part) >= 3 && part != sec {
				add(part)
				frags = append(frags, part)
			}
		}
	}
	r.secrets = expanded
	// fragOnly is the component list without the full needles — the pixel
	// redaction masks them so a needle spliced by quotes or escapes does not
	// leave a readable fragment behind.
	r.fragOnly = frags
	r.scrubSecrets()
}

// redactArgRe masks the VALUE after --redact/--redact-re in an echoed command
// line. It does not need a successful parse to know what to hide — a flag that
// failed to parse still leaked its value into the recorded argv, and the
// secrets slice is empty on that path.
// A single space or "=" separates flag and value; an empty argv token leaves
// a double space in the joined command, and requiring one exact space keeps
// the next flag from being eaten as a "value".
var redactArgRe = regexp.MustCompile(`(--redact(?:-re)?)(?:=| )("[^"]*"|'[^']*'|\S+)`)

func scrubRedactArgs(s string) string {
	return redactArgRe.ReplaceAllString(s, "${1} <redacted>")
}

// scrub replaces each secret with a marker — the marker is itself evidence:
// the reader sees a value was given and hidden, not that it was absent.
// scrub masks every needle in one left-to-right pass. Sequential ReplaceAll
// corrupted on itself twice over: the emitted <redacted> contains characters
// a later needle can match — w<red<a<cted> — and a needle that is a prefix of
// another would mask the shorter one first. A single scan emitting the marker
// and skipping the match does neither, and it also covers the %q-quoted form
// a warning produces for a needle holding " or \.
func (r *reporter) scrub(s string) string {
	if len(r.secrets) == 0 && len(r.secretRes) == 0 {
		return s
	}
	m := r.matcherFor(s)
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		// The marker is opaque: scrubbing runs several times over the same
		// document — format-time, the mid-run pass, emit — and a needle that
		// matched inside <redacted> would nest markers every pass. Skipping
		// it is what makes re-scrubbing idempotent.
		if strings.HasPrefix(s[i:], "<redacted>") {
			b.WriteString("<redacted>")
			i += len("<redacted>")
			continue
		}
		if n := m.matchLen(s, i); n > 0 {
			b.WriteString("<redacted>")
			i += n
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// scrubMatcher is what one scrub call knows about its input, computed once
// up front. Asking each pattern for a match anchored at every byte rescanned
// the whole remaining string per byte — quadratic, 10 s for a 270 KB file
// with one --redact-re — and quoting every literal per byte cost almost as
// much again in allocations.
type scrubMatcher struct {
	lits  []string // the literals and their %q-quoted twins
	spans [][2]int // every non-empty pattern match in the input, by start
	next  int      // first span whose start the scan has not reached
	reach int      // furthest end of any span starting at or before the scan
}

// matcherFor finds the pattern matches on the whole input instead of on a
// suffix, so a pattern keeps its real context: \b and ^ see the byte before
// the match, which a suffix hid. Matches are also taken line by line — the
// image matches each line on its own, so ^ and $ there mean line start and
// end, and a secret the image masks has to be masked here too, or the input
// hash would cover it in clear.
func (r *reporter) matcherFor(s string) *scrubMatcher {
	m := &scrubMatcher{}
	for _, sec := range r.secrets {
		if sec != "" {
			m.lits = append(m.lits, sec, strconv.Quote(sec))
		}
	}
	add := func(re *regexp.Regexp, text string, off int) {
		for _, loc := range re.FindAllStringIndex(text, -1) {
			// A zero-width match covers nothing, so a degenerate regex
			// cannot consume text.
			if loc[1] > loc[0] {
				m.spans = append(m.spans, [2]int{off + loc[0], off + loc[1]})
			}
		}
	}
	for _, re := range r.secretRes {
		add(re, s, 0)
		if !strings.Contains(s, "\n") {
			continue
		}
		for off := 0; off < len(s); {
			n := strings.IndexByte(s[off:], '\n')
			if n < 0 {
				n = len(s) - off
			}
			add(re, s[off:off+n], off)
			off += n + 1
		}
	}
	slices.SortFunc(m.spans, func(a, b [2]int) int { return a[0] - b[0] })
	return m
}

// matchLen returns how many bytes from s[i] one needle covers — the longest
// literal, its %q-quoted twin, or the rest of any pattern match that covers
// position i. Covering, not only starting at i: a literal or a marker can end
// in the middle of a pattern match, and the match's tail is still part of the
// secret. Calls must come with i non-decreasing, as scrub's scan makes them.
func (m *scrubMatcher) matchLen(s string, i int) int {
	best := 0
	for _, lit := range m.lits {
		if len(lit) > best && strings.HasPrefix(s[i:], lit) {
			best = len(lit)
		}
	}
	for m.next < len(m.spans) && m.spans[m.next][0] <= i {
		m.reach = max(m.reach, m.spans[m.next][1])
		m.next++
	}
	if m.reach-i > best {
		best = m.reach - i
	}
	return best
}

// scrubSecrets replaces each literal in the recorded provenance — the
// report's command, source path, and the PNG's tEXt chunks. A value the
// image hides must not travel verbatim in the metadata of the same file, or
// the redaction leaks through the channel meant to make it defensible.
// Warnings and help are scrubbed at format time, so they never carry the
// literal regardless of ordering.
func (r *reporter) scrubSecrets() {
	if r.argv != nil {
		r.rep.Command = r.commandLine()
	} else {
		r.rep.Command = r.scrubMeta(r.rep.Command)
	}
	if r.rep.Source != nil {
		r.rep.Source.Path = r.scrub(r.rep.Source.Path)
	}
	for i, kv := range r.metaKV {
		// The input hashes are computed over content addInput already masked,
		// so they cannot carry a secret. Scrubbing them again only destroys
		// provenance: a hex --redact-re wipes the sum, a short fragment cuts
		// a hole in it, and the PNG stops agreeing with the report.
		if strings.HasPrefix(kv[0], "taker:sha256") {
			continue
		}
		r.metaKV[i][1] = r.scrubMeta(kv[1])
	}
	for i, w := range r.rep.Warnings {
		r.rep.Warnings[i] = r.scrub(w)
	}
	for i, m := range r.rep.Marks {
		r.rep.Marks[i].Needle = r.scrub(m.Needle)
	}
	r.rep.Output = r.scrub(r.rep.Output)
}

// scrubMeta masks one provenance value. ANSI escapes inside a recorded
// command split a needle the same way they do inside a file — normalize
// before masking or `mR9!^[[31mkXv2` keeps both halves readable in the PNG's
// own text chunk.
func (r *reporter) scrubMeta(v string) string {
	b, _ := stripANSI([]byte(v))
	return scrubRedactArgs(r.scrub(string(b)))
}

// commandLine is the recorded invocation: the program's base name, then each
// argument masked and shell-quoted. Words joined with bare spaces lost their
// quoting — `-L "two words"` came back as two arguments — and the record must
// replay what ran. The base name, not argv[0] as typed: an absolute path to
// the binary leaks the analyst's filesystem into every image, the same leak
// the snap title already trims, and it says nothing about what ran.
func (r *reporter) commandLine() string {
	if len(r.argv) == 0 {
		return ""
	}
	argv := slices.Clone(r.argv)
	argv[0] = filepath.Base(argv[0])
	return r.quoteScrubbed(argv)
}

// quoteScrubbed masks each word, then quotes it, then masks the line. Word
// first: quoting escapes a ' or a $, which splits a needle that holds one,
// and the line-level scrub would no longer see it. The line pass still
// catches a needle that spans two words.
func (r *reporter) quoteScrubbed(args []string) string {
	words := make([]string, len(args))
	for i, a := range args {
		b, _ := stripANSI([]byte(a))
		words[i] = r.scrub(string(b))
	}
	for i, w := range words {
		words[i] = quoteWord(w, true)
	}
	return r.scrubMeta(strings.Join(words, " "))
}

// shellQuote joins argv into one POSIX shell line that splits back into the
// same words.
func shellQuote(args []string) string {
	words := make([]string, len(args))
	for i, a := range args {
		words[i] = quoteWord(a, false)
	}
	return strings.Join(words, " ")
}

// quoteWord quotes one word only when it needs it, so a plain command still
// reads as typed. A word with no ' goes in single quotes, where nothing is
// special. A word with a ' goes in double quotes, escaping only " $ ` and \:
// closing the quote around an escaped ' splits a needle such as it's-s3cret
// in the prompt line, and the image could no longer find it to black it out.
// A ! forces that form after all, because an interactive bash expands !
// inside double quotes. With masked set, the <redacted> marker counts as
// plain: a masked word replays nothing either way, and quoting the marker
// only adds noise.
func quoteWord(s string, masked bool) string {
	if s == "" {
		return "''"
	}
	plain := s
	if masked {
		plain = strings.ReplaceAll(s, "<redacted>", "")
	}
	if !strings.ContainsFunc(plain, needsQuote) {
		return s
	}
	switch {
	case !strings.Contains(s, "'"):
		return "'" + s + "'"
	case !strings.Contains(s, "!"):
		var b strings.Builder
		b.WriteByte('"')
		for _, c := range s {
			if strings.ContainsRune("\"$`\\", c) {
				b.WriteByte('\\')
			}
			b.WriteRune(c)
		}
		b.WriteByte('"')
		return b.String()
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// needsQuote is any character outside the set a POSIX shell reads literally
// in a bare word.
func needsQuote(c rune) bool {
	switch {
	case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		return false
	}
	return !strings.ContainsRune("_@%+=:,./-", c)
}

// pngMeta is the metadata the PNG carries beyond version, time and command:
// the input hashes, and where the input came from — the same source path and
// revision the report states. Without them a detached image proves its
// version only in footnote pixels, which no tool can check. An exec run is
// left out of taker:source: taker:exec already names its command line.
func (r *reporter) pngMeta() [][2]string {
	meta := slices.Clone(r.metaKV)
	if src := r.rep.Source; src != nil {
		if src.Path != "" && src.Kind != "exec" {
			meta = append(meta, [2]string{"taker:source", r.scrubMeta(src.Kind + ":" + src.Path)})
		}
		if src.Rev != "" {
			meta = append(meta, [2]string{"taker:git-revision", src.Rev})
		}
	}
	return meta
}

// helpf records the command that fixes or completes the current situation.
// Call it before fail() — the hints travel inside the error document.
func (r *reporter) helpf(format string, args ...any) {
	r.rep.Help = append(r.rep.Help, r.scrub(fmt.Sprintf(format, args...)))
}

// note prints a human line that the report does not need to carry — scrubbed
// like every channel, because stderr is what people pipe into logs.
func (r *reporter) note(format string, args ...any) {
	fmt.Fprint(os.Stderr, "taker: "+r.scrub(fmt.Sprintf(format, args...))+"\n")
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
	msg := r.scrub(fmt.Sprintf(format, args...))
	r.note("warning: %s", msg)
	r.rep.Warnings = append(r.rep.Warnings, msg)
}

// fail reports why the evidence was not produced and returns the exit code for
// the caller to hand to os.Exit. Failing without a report would leave a
// consumer grepping prose for the reason it already asked for in JSON.
func (r *reporter) fail(code int, kind, format string, args ...any) int {
	msg := r.scrub(fmt.Sprintf(format, args...))
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
	if r.scrub(r.rep.Output) != r.rep.Output {
		r.warn("output path contains a redacted needle — the report shows the masked form")
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

// doneDry reports a render that was validated but deliberately not written:
// the caller checked everything the image would claim — marks, crops, height —
// without producing the file. --dry-run exists so an agent can preflight a run
// before deciding it is worth uploading.
func (r *reporter) doneDry(img image.Image) int {
	r.rep.OK = true
	r.rep.Exit = exitOK
	r.rep.DryRun = true
	b := img.Bounds()
	r.rep.Width, r.rep.Height = b.Dx(), b.Dy()
	r.helpf("rerun without --dry-run to write the PNG")
	r.note("dry run: would write a %dx%d image", b.Dx(), b.Dy())
	return r.emit(exitOK)
}

// emit writes the report in the format that was asked for, and always returns
// the code so callers can pass it straight to os.Exit. A failed run emits a
// TOON document even with no format flag: the error is the one piece of output
// a consumer can least afford to lose to stderr.
func (r *reporter) emit(code int) int {
	// Last line of defense: every exit path ends here, so nothing that a
	// validation failure could leave behind — a literal in Command set before
	// secrets were known, a warning carrying a secret — survives into the
	// document. Scrubbing is idempotent; ordering never decides safety.
	r.scrubSecrets()
	switch {
	case r.format == "json":
		// No HTML escaping: this is not going into a web page, and the
		// escaped form turns every <redacted> marker and every && of a shell
		// line into \u003c… noise a consumer greps past.
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(r.rep); err != nil {
			r.note("could not encode the json report: %v", err)
			return code
		}
		r.out.Write(buf.Bytes()) // Encode already ended it with a newline
	case r.format == "toon" || !r.rep.OK:
		text, err := r.reportTOON()
		if err != nil {
			r.note("could not encode the toon report: %v", err)
			return code
		}
		fmt.Fprint(r.out, text) // every TOON line already ends in a newline
	}
	return code
}

// recordMarks puts the per-literal counts in the report, and says them out loud:
// a mark is a guess about where the finding is, and an image where that guess
// landed on 25 lines looks exactly as correct as one where it landed on the
// vulnerable line. The count is the only thing that tells them apart.
func recordMarks(rep *reporter, stats []highlight.MarkStats) {
	for _, st := range stats {
		if st.Matches == 0 {
			// A mark that landed nowhere is a caveat the consumer must see —
			// through warnings, not just a stderr line a pipeline never reads.
			// Message() already carries its "warning: " prefix.
			msg := st.Message()
			rep.note("%s", msg)
			rep.rep.Warnings = append(rep.rep.Warnings, strings.TrimPrefix(msg, "warning: "))
		} else {
			rep.note("%s", st.Message())
		}
		rep.rep.Marks = append(rep.rep.Marks, MarkReport{
			Needle:  st.Literal,
			Matches: st.Matches,
			Lines:   st.Lines,
			Kept:    st.Matches - st.Dropped,
			Dropped: st.Dropped,
		})
		if st.Dropped > 0 {
			rep.helpf("raise --max-marks or narrow %q to box the %d matches left out", st.Literal, st.Dropped)
		}
	}
}

// markShown fills marks[].shown once the view is known. recordMarks runs
// before the trim — --context centers on its matches — so the entries it
// appended from base on get their shown counts here, in the same order.
func markShown(rep *reporter, base int, stats []highlight.MarkStats, shown func(i int, st highlight.MarkStats) int) {
	for i, st := range stats {
		if base+i < len(rep.rep.Marks) {
			rep.rep.Marks[base+i].Shown = shown(i, st)
		}
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

// redactFragRes returns the pixel-mask fragments as patterns that match a
// fragment only where it stands as a whole token — but only when keep is off:
// --redact-keep shows a span's leading chars, and a kept fragment would show
// its own. Keep mode is already a weakened guarantee the user asked for.
//
// Whole tokens, because the echo a fragment exists for is bounded by
// punctuation (strconv reports parsing "ZX9"), while the same letters inside
// a longer word are ordinary text: "app" from app.example.test blacked out
// the middle of every "application/json", and evidence that hides what is not
// secret is not faithful. A fragment glued to other letters by the target
// ("xZX9y") stays visible; that is the trade this rule makes.
func (r *reporter) redactFragRes(keep int) []*regexp.Regexp {
	if keep > 0 || len(r.fragOnly) == 0 {
		return nil
	}
	out := make([]*regexp.Regexp, 0, len(r.fragOnly))
	for _, f := range r.fragOnly {
		out = append(out, tokenRe(f))
	}
	return out
}

// tokenRe matches s where no word character touches it. The boundary is put
// only on an edge that is itself an ASCII word character: \b next to "!" or
// "ñ" would demand a word character outside, and a fragment that starts with
// punctuation would never match at all. An edge left unbounded only masks
// more.
func tokenRe(s string) *regexp.Regexp {
	pat := regexp.QuoteMeta(s)
	if isWordByte(s[0]) {
		pat = `\b` + pat
	}
	if isWordByte(s[len(s)-1]) {
		pat += `\b`
	}
	return regexp.MustCompile(pat)
}

func isWordByte(b byte) bool {
	return b == '_' || '0' <= b && b <= '9' || 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z'
}
