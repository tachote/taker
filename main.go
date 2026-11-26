package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/alecthomas/kong"
	"github.com/tachote/taker/highlight"
	"github.com/tachote/taker/render"
)

// SnapCmd renders a single code file.
type SnapCmd struct {
	File       string   `arg:"" optional:"" help:"Source file to render (reads stdin if omitted)."`
	Lang       string   `short:"l" help:"Language for syntax highlighting (auto-detected if omitted)."`
	Lines      string   `short:"n" help:"Lines to include, e.g. '1,5,10-20'. Ignored when --context is set."`
	FirstLine  int      `name:"first-line" default:"1" help:"Line number of the input's first line. Set it when piping an excerpt, so the gutter and --highlight agree with the real file."`
	Git        string   `name:"git" placeholder:"<rev>:<path>" help:"Render <path> at <rev> instead of the working-tree copy, e.g. HEAD:src/auth/login.py."`
	Highlight  string   `short:"H" help:"Regions to highlight: '15', '13,15', '10-20', or '15:20-45' for columns."`
	Mark       []string `short:"m" help:"Highlight every occurrence of this text. Repeatable."`
	MarkRe     []string `name:"mark-re" help:"Highlight every match of this regular expression. Repeatable."`
	MaxMarks   int      `name:"max-marks" default:"0" help:"Keep at most N matches per --mark literal, earliest first (default: all)."`
	Redact     []string `name:"redact" help:"Hide every occurrence of this text under a black box (secrets, tokens, PII). Repeatable; a comma inside a value splits it into two needles - escape it with a backslash-comma."`
	RedactKeep int      `name:"redact-keep" default:"0" help:"Leave the first N characters of each --redact match visible (e.g. 4 gives sk_l…████)."`
	RedactRe   []string `name:"redact-re" help:"Hide every regex match under a black box. Repeatable; a comma inside a value splits it into two needles - escape it with a backslash-comma."`
	Label      string   `short:"L" help:"Caption strip stating what the image proves."`
	Context    int      `short:"c" default:"0" help:"Include N context lines around each highlighted line (requires --highlight or --mark)."`
	Wrap       bool     `short:"w" help:"Wrap long lines at a column boundary."`
	WrapWidth  int      `name:"wrap-width" default:"0" help:"Columns per row (default: 100, minimum 20). Implies --wrap."`
	Output     string   `short:"o" default:"output.png" help:"Output PNG file path."`
	Title      string   `short:"t" help:"Title shown in the tab bar (defaults to file basename)."`
	Theme      string   `short:"T" default:"vscode-dark" help:"Color theme. Use --list-themes to see options."`
	Chrome     string   `name:"chrome" default:"editor" help:"Window style: 'editor' (bare pane) or 'window' (title bar with traffic lights)."`
	Shadow     bool     `name:"shadow" help:"Lift the window off the canvas with a drop shadow."`
	RepoDir    string   `name:"repo-dir" help:"Repository directory for --git (default: current directory)."`
	DryRun     bool     `name:"dry-run" help:"Validate and report without writing the PNG."`
	ListThemes bool     `name:"list-themes" help:"Print available themes and exit."`
	JSON       bool     `name:"json" help:"Write a machine-readable report to stdout. Human messages stay on stderr."`
	TOON       bool     `name:"toon" help:"Write the report as TOON — same document, ~40% fewer tokens for agents."`
}

// HTTPCmd renders HTTP request/response evidence. Both sides, one side, or a
// single combined capture (what Burp's "Copy to file" produces) are accepted.
type HTTPCmd struct {
	Request    string   `short:"r" help:"File containing the HTTP request."`
	Response   string   `short:"s" help:"File containing the HTTP response."`
	File       string   `short:"f" help:"Combined capture file holding request then response (Burp 'Save item' format); the response starts at the first HTTP/ status line."`
	ReqHL      string   `name:"hr" help:"Regions to highlight in request: '8-9' or '8:20-45' for columns."`
	ResHL      string   `name:"hs" help:"Regions to highlight in response: '4' or '4:12-60' for columns."`
	Mark       []string `short:"m" help:"Highlight every occurrence of this text in either panel. Repeatable."`
	MarkRe     []string `name:"mark-re" help:"Highlight every regex match in either panel. Repeatable."`
	MaxMarks   int      `name:"max-marks" default:"0" help:"Keep at most N matches per --mark literal, earliest first (default: all)."`
	Redact     []string `name:"redact" help:"Hide every occurrence of this text under a black box. Repeatable; a comma inside a value splits it into two needles - escape it with a backslash-comma."`
	RedactKeep int      `name:"redact-keep" default:"0" help:"Leave the first N characters of each --redact match visible."`
	RedactRe   []string `name:"redact-re" help:"Hide every regex match under a black box. Repeatable; a comma inside a value splits it into two needles - escape it with a backslash-comma."`
	Label      string   `short:"L" help:"Caption strip stating what the image proves."`
	ReqLines   string   `name:"nr" help:"Lines to include from the request, e.g. '1-20'."`
	ResLines   string   `name:"ns" help:"Lines to include from the response, e.g. '1-30'."`
	Context    int      `short:"c" default:"0" help:"Include N context lines around each highlighted line; applies per panel."`
	WrapWidth  int      `name:"wrap-width" default:"0" help:"Columns per panel (default: 90, minimum 20)."`
	Output     string   `short:"o" default:"output.png" help:"Output PNG file path."`
	DryRun     bool     `name:"dry-run" help:"Validate and report without writing the PNG."`
	JSON       bool     `name:"json" help:"Write a machine-readable report to stdout. Human messages stay on stderr."`
	TOON       bool     `name:"toon" help:"Write the report as TOON — same document, ~40% fewer tokens for agents."`
}

// ExecCmd runs a command and renders the terminal session — the prompt line
// plus stdout/stderr — as evidence. The proof of an RCE is `id` and its output
// in the same frame, not a screenshot of the analyst's own terminal.
type ExecCmd struct {
	Args       []string `arg:"" name:"command" passthrough:"" optional:"" help:"Command to run. Put it after -- so its flags stay its own."`
	Shell      string   `name:"sh" help:"Run this command line through sh -c (for pipes and quoting)."`
	Timeout    int      `name:"timeout" default:"15" help:"Seconds before the command is killed."`
	Output     string   `short:"o" default:"output.png" help:"Output PNG file path."`
	Label      string   `short:"L" help:"Caption strip stating what the image proves."`
	Mark       []string `short:"m" help:"Highlight every occurrence of this text. Repeatable."`
	MarkRe     []string `name:"mark-re" help:"Highlight every regex match. Repeatable."`
	MaxMarks   int      `name:"max-marks" default:"0" help:"Keep at most N matches per --mark literal."`
	Redact     []string `name:"redact" help:"Hide every occurrence of this text under a black box. Repeatable; a comma inside a value splits it into two needles - escape it with a backslash-comma."`
	RedactKeep int      `name:"redact-keep" default:"0" help:"Leave the first N characters of each --redact match visible."`
	RedactRe   []string `name:"redact-re" help:"Hide every regex match under a black box. Repeatable; a comma inside a value splits it into two needles - escape it with a backslash-comma."`
	WrapWidth  int      `name:"wrap-width" default:"80" help:"Columns per row before output wraps (default: 80, minimum 20)."`
	Theme      string   `short:"T" default:"vscode-dark" help:"Color theme. Use --list-themes to see options."`
	Title      string   `short:"t" default:"Terminal" help:"Title shown in the window bar."`
	Chrome     string   `name:"chrome" default:"window" help:"Window style: 'window' (default, like a terminal) or 'editor'."`
	Shadow     bool     `name:"shadow" help:"Lift the window off the canvas with a drop shadow."`
	DryRun     bool     `name:"dry-run" help:"Run the command and validate without writing the PNG."`
	JSON       bool     `name:"json" help:"Write a machine-readable report to stdout. Human messages stay on stderr."`
	TOON       bool     `name:"toon" help:"Write the report as TOON — same document, ~40% fewer tokens for agents."`
}

// TermCmd renders a saved terminal transcript (typescript, copied output)
// through the same terminal look as exec, without running anything.
type TermCmd struct {
	File       string   `arg:"" required:"" help:"Transcript file to render."`
	Output     string   `short:"o" default:"output.png" help:"Output PNG file path."`
	Label      string   `short:"L" help:"Caption strip stating what the image proves."`
	Mark       []string `short:"m" help:"Highlight every occurrence of this text. Repeatable."`
	MarkRe     []string `name:"mark-re" help:"Highlight every regex match. Repeatable."`
	MaxMarks   int      `name:"max-marks" default:"0" help:"Keep at most N matches per --mark literal."`
	Redact     []string `name:"redact" help:"Hide every occurrence of this text under a black box. Repeatable; a comma inside a value splits it into two needles - escape it with a backslash-comma."`
	RedactKeep int      `name:"redact-keep" default:"0" help:"Leave the first N characters of each --redact match visible."`
	RedactRe   []string `name:"redact-re" help:"Hide every regex match under a black box. Repeatable; a comma inside a value splits it into two needles - escape it with a backslash-comma."`
	WrapWidth  int      `name:"wrap-width" default:"80" help:"Columns per row before output wraps."`
	Theme      string   `short:"T" default:"vscode-dark" help:"Color theme. Use --list-themes to see options."`
	Title      string   `short:"t" default:"Terminal" help:"Title shown in the window bar."`
	Chrome     string   `name:"chrome" default:"window" help:"Window style: 'window' (default) or 'editor'."`
	Shadow     bool     `name:"shadow" help:"Lift the window off the canvas with a drop shadow."`
	DryRun     bool     `name:"dry-run" help:"Validate and report without writing the PNG."`
	JSON       bool     `name:"json" help:"Write a machine-readable report to stdout. Human messages stay on stderr."`
	TOON       bool     `name:"toon" help:"Write the report as TOON — same document, ~40% fewer tokens for agents."`
}

var cli struct {
	Snap    SnapCmd `cmd:"" default:"withargs" help:"Render a code file as a PNG screenshot."`
	HTTP    HTTPCmd `cmd:"http" help:"Render HTTP request/response evidence."`
	Exec    ExecCmd `cmd:"exec" help:"Run a command and render its terminal output as evidence. Example: taker exec -o out.png -- id"`
	Term    TermCmd `cmd:"term" help:"Render a saved terminal transcript as evidence."`
	Version bool    `short:"v" name:"version" help:"Print version and exit."`
}

// version is set by GoReleaser via -ldflags "-X main.version=..."
var version = "dev"

// isVersionProbe answers AXI's version fast path: agents and harnesses probe
// -v, -V and --version constantly, so a bare version flag prints and exits
// before kong even parses. Flags in trailing positions fall through to the
// CLI's normal handling.
func isVersionProbe(args []string) bool {
	if len(args) != 1 {
		return false
	}
	switch args[0] {
	case "-v", "-V", "--version":
		return true
	}
	return false
}

// formatOf resolves which stdout encoding the run asked for. Both set is a
// conflict the caller resolves in main, where a usage error can still name
// the two flags.
func formatOf(jsonOut, toonOut bool) string {
	switch {
	case jsonOut:
		return "json"
	case toonOut:
		return "toon"
	}
	return ""
}

func main() {
	if isVersionProbe(os.Args[1:]) {
		fmt.Println(version)
		return
	}

	// Show help when called with no arguments and stdin is a terminal.
	if len(os.Args) == 1 {
		fi, err := os.Stdin.Stat()
		if err != nil || (fi.Mode()&os.ModeCharDevice) != 0 {
			rep := newReporter("", "snap")
			rep.helpf("taker snap <file> -o out.png — or pipe the source on stdin")
			os.Exit(rep.fail(exitUsage, kindUsage, "no input"))
		}
	}

	parser := newParser()
	ctx, parseErr := parser.Parse(os.Args[1:])
	if parseErr != nil {
		// kong's Parse wrapper exits 1 on a bad flag and says nothing on
		// stdout. The contract is 2 for a usage error and a document an agent
		// can act on, so the error goes through the same reporter as a bad
		// input. The mode is guessed from argv: the command graph did not
		// get built.
		mode := "snap"
		if len(os.Args) > 1 {
			switch os.Args[1] {
			case "http", "exec", "term":
				mode = os.Args[1]
			}
		}
		// Honor the format flag even though parsing died — a caller that
		// asked for JSON gets JSON on every path. Only needle-flag values
		// are secrets to mask; file paths and option values stay replayable.
		format := ""
		for _, a := range os.Args[1:] {
			if a == "--json" {
				format = "json"
			} else if a == "--toon" {
				format = "toon"
			}
		}
		rep := newReporter(format, mode)
		rep.rep.Command = strings.Join(os.Args, " ")
		rep.secrets = argvNeedleValues(os.Args[1:])
		hint := "taker --help"
		if mode != "snap" {
			hint = "taker " + mode + " --help"
		}
		rep.helpf("run '%s' for the valid flags", hint)
		os.Exit(rep.fail(exitUsage, kindUsage, "%v", parseErr))
	}

	if cli.Version {
		fmt.Println(version)
		os.Exit(exitOK)
	}

	// ctx.Command() returns the full path including positional args —
	// "exec <command>" — so match on the leading word, not the whole string.
	sel := ctx.Command()
	mode, jsonOut, toonOut := "snap", cli.Snap.JSON, cli.Snap.TOON
	switch {
	case strings.HasPrefix(sel, "http"):
		mode, jsonOut, toonOut = "http", cli.HTTP.JSON, cli.HTTP.TOON
	case strings.HasPrefix(sel, "exec"):
		mode, jsonOut, toonOut = "exec", cli.Exec.JSON, cli.Exec.TOON
	case strings.HasPrefix(sel, "term"):
		mode, jsonOut, toonOut = "term", cli.Term.JSON, cli.Term.TOON
	}
	rep := newReporter(formatOf(jsonOut, toonOut), mode)
	// The exact argv is provenance: the report carries it and the PNG embeds
	// it, so the image cannot be claimed to come from a simpler command than
	// ran.
	rep.rep.Command = strings.Join(os.Args, " ")
	// Parse succeeded, so the needles are already known — register them before
	// any failure path below can echo the command.
	switch mode {
	case "http":
		rep.setSecrets(slices.Concat(cli.HTTP.Redact, cli.HTTP.RedactRe))
	case "exec":
		rep.setSecrets(slices.Concat(cli.Exec.Redact, cli.Exec.RedactRe))
	case "term":
		rep.setSecrets(slices.Concat(cli.Term.Redact, cli.Term.RedactRe))
	default:
		rep.setSecrets(slices.Concat(cli.Snap.Redact, cli.Snap.RedactRe))
	}
	if jsonOut && toonOut {
		rep.helpf("pick one: --json or --toon")
		os.Exit(rep.fail(exitUsage, kindUsage, "--json and --toon are mutually exclusive"))
	}

	var code int
	switch {
	case strings.HasPrefix(sel, "http"):
		code = runHTTP(&cli.HTTP, rep)
	case strings.HasPrefix(sel, "exec"):
		code = runExec(&cli.Exec, rep)
	case strings.HasPrefix(sel, "term"):
		code = runTerm(&cli.Term, rep)
	default:
		code = runSnap(&cli.Snap, rep)
	}
	os.Exit(code)
}

// newParser builds the command graph — factored out so a test can catch a
// malformed struct tag (kong.Must panics) instead of the shipped binary.
func newParser() *kong.Kong {
	return kong.Must(&cli,
		kong.Name("taker"),
		kong.Description("Generate professional code & HTTP evidence screenshots for security audits."),
		// --help still ends the process through this hook; parse errors come
		// back from Parse so they can be reported like every other failure.
		kong.Exit(func(code int) { os.Exit(code) }),
	)
}

func runSnap(cmd *SnapCmd, rep *reporter) int {
	rep.setSecretsAll(slices.Concat(cmd.Redact, cmd.RedactRe), cmd.Redact)
	if code := validateRedact(rep, cmd.Redact, cmd.RedactRe, cmd.RedactKeep); code != 0 {
		return code
	}
	for _, c := range [][2]any{
		{"--context", cmd.Context}, {"--wrap-width", cmd.WrapWidth},
		{"--first-line", cmd.FirstLine}, {"--max-marks", cmd.MaxMarks},
	} {
		if code := nonneg(rep, c[0].(string), c[1].(int)); code != 0 {
			return code
		}
	}
	if cmd.ListThemes {
		return listThemes(rep)
	}
	if cmd.File != "" && samePath(cmd.Output, cmd.File) {
		return rep.fail(exitUsage, kindUsage, "-o must differ from the input file — it would overwrite the source")
	}

	theme, err := resolveTheme(cmd.Theme)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "%v", err)
	}
	if cmd.Chrome != "editor" && cmd.Chrome != "window" {
		return rep.fail(exitUsage, kindUsage, "--chrome must be 'editor' or 'window', got %q", cmd.Chrome)
	}

	// Asking for a column width and not getting wrapping is never what anyone
	// meant, and the flag used to be silently ignored without --wrap.
	cmd.WrapWidth = clampWrapWidth(cmd.WrapWidth, false, rep)
	if cmd.WrapWidth > 0 {
		cmd.Wrap = true
	}

	var src []byte
	origin := sourceInfo{Kind: "stdin"}
	switch {
	case cmd.Git != "" && cmd.File != "":
		return rep.fail(exitUsage, kindUsage, "give either a file or --git, not both")
	case cmd.Git != "":
		src, origin, err = readGitBlobIn(cmd.RepoDir, cmd.Git)
		if err == nil {
			src = normalizeInput(src, rep)
		}
	case cmd.File != "":
		src, err = os.ReadFile(cmd.File)
		origin = sourceInfo{Kind: "file", Path: cmd.File}
		src = normalizeInput(src, rep)
	default:
		// Not /dev/stdin: that path does not exist on Windows. An agent that
		// forgot the file hands us an open pipe nobody will write to — probe
		// instead of blocking forever on a read that never comes.
		r := bufio.NewReader(os.Stdin)
		if !stdinHasData(r, 300*time.Millisecond) {
			rep.helpf("taker snap <file> -o out.png — or pipe the source on stdin")
			return rep.fail(exitUsage, kindUsage, "no input: stdin produced no data within 300ms")
		}
		src, err = io.ReadAll(r)
	}
	if err != nil {
		return rep.fail(exitInput, kindInput, "%v", err)
	}
	if origin.Ref != "" {
		rep.note("%s", origin.Ref)
	}
	rep.rep.Source = &Source{Kind: origin.Kind, Path: origin.Path, Rev: origin.Rev}

	title := cmd.Title
	if title == "" && origin.Kind == "git" {
		title = filepath.ToSlash(origin.Path)
	}
	if title == "" && cmd.File != "" {
		// Keep the relative path — in evidence, auth/views.py beats views.py.
		// Absolute paths leak the analyst's filesystem, so trim those.
		if filepath.IsAbs(cmd.File) {
			title = filepath.Base(cmd.File)
		} else {
			title = filepath.ToSlash(filepath.Clean(cmd.File))
		}
	}
	if title == "" {
		title = "code"
	}

	if cmd.FirstLine < 1 {
		return rep.fail(exitUsage, kindUsage, "--first-line must be 1 or more")
	}

	all, err := highlight.Tokenize(string(src), cmd.Lang, cmd.File, theme.Chroma)
	if err != nil {
		return rep.fail(exitInput, kindInput, "tokenize: %v", err)
	}
	// Renumber before anything reads a line number: --lines, --highlight, -m and
	// the gutter all speak the file's numbering once this has run.
	all = highlight.ShiftLines(all, cmd.FirstLine)

	hlLines, err := highlight.ParseHighlightSpec(cmd.Highlight)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "parse highlight: %v", err)
	}

	// Merge before the guard: -m supplies marks just as -H does, and --context
	// centers on either.
	markRe, err := compileRegex("--mark-re", cmd.MarkRe)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "%v", err)
	}
	redactRe, err := compileRegex("--redact-re", cmd.RedactRe)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "%v", err)
	}
	// The compiled patterns join the scrubber: their matches are secrets the
	// literal list never named — a token caught in a filename or a warning
	// stays hidden only if the pattern itself does the masking.
	rep.secretRes = redactRe
	msgs := [][]highlight.LineTokens{all}
	marked, stats := highlight.FindMarks(msgs, cmd.Mark, cmd.MaxMarks)
	reMarked, reStats := highlight.FindRegexMarks(msgs, markRe, cmd.MaxMarks)
	stats = append(stats, reStats...)
	recordMarks(rep, stats)
	hlLines = hlLines.Merge(marked[0]).Merge(reMarked[0])

	// Redaction searches the untrimmed input like marks do: a secret has to be
	// hidden in the excerpt, not only in the lines the image ends up showing.
	redact, redactStats := highlight.FindMarks(msgs, slices.Concat(cmd.Redact, rep.redactFrags(cmd.RedactKeep)), 0)
	redactReM, redactReStats := highlight.FindRegexMarks(msgs, redactRe, 0)
	redactMarks := highlight.KeepSpans(redact[0].Merge(redactReM[0]), cmd.RedactKeep)
	matched := 0
	for _, st := range redactStats {
		matched += st.Matches
		switch {
		case st.Matches == 0:
			rep.warn("--redact %q matched nothing", st.Literal)
			if strings.ContainsAny(st.Literal, "\t\n\r") {
				rep.helpf("the needle holds a control char — it only matches if the file has it too")
			} else {
				rep.helpf("nothing was masked — check the value or use --redact-re '<pattern>'")
			}
		case cmd.RedactKeep > 0 && len(st.Literal) <= cmd.RedactKeep:
			return rep.fail(exitUsage, kindUsage,
				"--redact-keep %d leaves nothing hidden in --redact %q — lower it or drop it",
				cmd.RedactKeep, st.Literal)
		case cmd.RedactKeep > 0 && len(st.Literal)-cmd.RedactKeep < 4:
			rep.warn("--redact-keep %d hides only %d char(s) of --redact %q",
				cmd.RedactKeep, len(st.Literal)-cmd.RedactKeep, st.Literal)
		}
	}
	for _, st := range redactReStats {
		matched += st.Matches
		if st.Matches == 0 {
			rep.warn("--redact-re %q matched nothing", st.Literal)
			rep.helpf("nothing was masked — check the pattern")
		}
	}
	// The literal check above is per needle; a regex match's length is only
	// known once spans exist — when keep consumed them all, nothing is hidden.
	if cmd.RedactKeep > 0 && matched > 0 && redactMarks == nil {
		return rep.fail(exitUsage, kindUsage,
			"--redact-keep %d leaves nothing hidden — lower it or drop it", cmd.RedactKeep)
	}
	rep.addInput("source", src)
	rep.scrubSecrets()
	warnMarkRedactOverlap(rep, cmd.Mark, cmd.Redact)
	if cmd.Context > 0 && len(hlLines) == 0 {
		return rep.fail(exitUsage, kindUsage, "--context needs --highlight or --mark to center on")
	}

	lineSpec, err := resolveLineSpec("--lines", cmd.Lines, hlLines, cmd.Context, rep)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "%v", err)
	}
	lines := highlight.FilterLines(all, lineSpec)
	// A spec that matches nothing would emit a blank PNG at exit 0 — exactly the
	// kind of thing that reaches a report unnoticed.
	if len(lines) == 0 {
		rep.helpf("widen --lines/--first-line or drop --highlight/--mark so some lines remain")
		return rep.fail(exitRefused, kindRefused, "no lines matched (file has %d lines)", len(all))
	}

	// Only a file read from its first line has a known length. For an excerpt
	// the tool has no idea how long the real file is, and a wrong total is
	// worse than none.
	total := excerptTotal(len(all), cmd.FirstLine)
	rcfg := &render.Config{
		Lines:          lines,
		Title:          rep.scrub(title),
		HighlightLines: hlLines,
		RedactRegions:  redactMarks,
		RedactAsked:    len(cmd.Redact)+len(cmd.RedactRe) > 0,
		TotalLines:     total,
		SourceRef:      origin.Ref,
		Theme:          theme,
		Wrap:           cmd.Wrap,
		WrapWidth:      cmd.WrapWidth,
		Caption:        rep.scrub(cmd.Label),
		WindowChrome:   cmd.Chrome == "window",
		Shadow:         cmd.Shadow,
		// The render's own warnings belong in the report: a consumer reading
		// --json has no stderr to notice them on.
		Warn: func(msg string) { rep.warn("%s", msg) },
	}
	img, err := render.Render(rcfg)
	if err != nil {
		return rep.fail(exitRefused, kindRefused, "render: %v", err)
	}
	rep.rep.Redactions = rcfg.Redacted
	if cmd.DryRun {
		return rep.doneDry(img)
	}
	if err := writePNG(img, cmd.Output, rep); err != nil {
		if errors.Is(err, errOutUsage) {
			return rep.fail(exitUsage, kindUsage, "%v", err)
		}
		return rep.fail(exitFail, kindOutput, "%v", err)
	}

	// What the image shows, measured off the view that was drawn rather than
	// assumed from the flags that asked for it.
	rep.rep.Crop = &Crop{}
	*rep.rep.Crop = cropOf(highlight.Extent(lines), total)
	return rep.done(cmd.Output)
}

func runHTTP(cmd *HTTPCmd, rep *reporter) int {
	rep.setSecretsAll(slices.Concat(cmd.Redact, cmd.RedactRe), cmd.Redact)
	if code := validateRedact(rep, cmd.Redact, cmd.RedactRe, cmd.RedactKeep); code != 0 {
		return code
	}
	for _, c := range [][2]any{
		{"--context", cmd.Context}, {"--wrap-width", cmd.WrapWidth}, {"--max-marks", cmd.MaxMarks},
	} {
		if code := nonneg(rep, c[0].(string), c[1].(int)); code != 0 {
			return code
		}
	}
	cmd.WrapWidth = clampWrapWidth(cmd.WrapWidth, true, rep)

	var reqSrc, resSrc []byte
	var err error
	srcPath := cmd.Request
	switch {
	case cmd.File != "" && (cmd.Request != "" || cmd.Response != ""):
		rep.helpf("use one input form: -f <capture> or -r <req> and/or -s <res>")
		return rep.fail(exitUsage, kindUsage, "give either -f or -r/-s, not both")
	case cmd.File != "":
		var raw []byte
		if raw, err = os.ReadFile(cmd.File); err != nil {
			return rep.fail(exitInput, kindInput, "read %s: %v", cmd.File, err)
		}
		raw = normalizeInput(raw, rep)
		reqSrc, resSrc = splitCapture(raw)
		srcPath = cmd.File
		// A "Save item" that is not HTTP at all renders as a confident
		// Request pane — the worst kind of silent success. Garbage in gets
		// an input error; a request with no response gets a warning.
		switch {
		case reqSrc != nil && !looksHTTPStart(reqSrc) && resSrc == nil:
			rep.helpf("-f expects a request and/or response capture (status line 'HTTP/...')")
			return rep.fail(exitInput, kindInput, "%s does not look like an HTTP capture", cmd.File)
		case resSrc == nil:
			rep.warn("%s has no response section — rendering the request alone", cmd.File)
		}
	case cmd.Request == "" && cmd.Response == "":
		rep.helpf("taker http -r <req.http> -s <res.http>")
		rep.helpf("taker http -f <capture> for a combined 'Save item' file")
		return rep.fail(exitUsage, kindUsage, "nothing to render: pass -r, -s or -f")
	default:
		if cmd.Request != "" {
			if reqSrc, err = os.ReadFile(cmd.Request); err == nil {
				reqSrc = normalizeInput(reqSrc, rep)
			} else {
				return rep.fail(exitInput, kindInput, "read request: %v", err)
			}
		}
		if cmd.Response != "" {
			if resSrc, err = os.ReadFile(cmd.Response); err == nil {
				resSrc = normalizeInput(resSrc, rep)
			} else {
				return rep.fail(exitInput, kindInput, "read response: %v", err)
			}
		}
	}
	// An empty capture is a mistake upstream, not evidence of anything. And a
	// file fed to -r/-s that isn't HTTP gets the same gate -f applies — a
	// confident "Request" pane drawn over a log file is silent success.
	if reqSrc != nil && len(bytes.TrimSpace(reqSrc)) == 0 {
		return rep.fail(exitInput, kindInput, "request is empty")
	}
	if resSrc != nil && len(bytes.TrimSpace(resSrc)) == 0 {
		return rep.fail(exitInput, kindInput, "response is empty")
	}
	if cmd.File == "" {
		if reqSrc != nil && !looksHTTPRequest(reqSrc) {
			return rep.fail(exitInput, kindInput, "%s does not look like an HTTP request (start line 'METHOD target HTTP/...')", cmd.Request)
		}
		if resSrc != nil && !looksHTTPResponse(resSrc) {
			return rep.fail(exitInput, kindInput, "%s does not look like an HTTP response (status line 'HTTP/...')", cmd.Response)
		}
	}
	hasReq, hasRes := reqSrc != nil, resSrc != nil
	if !hasReq && cmd.ReqHL != "" {
		return rep.fail(exitUsage, kindUsage, "--hr has no request to highlight; pass -r or -f")
	}
	if !hasRes && cmd.ResHL != "" {
		return rep.fail(exitUsage, kindUsage, "--hs has no response to highlight; pass -s or -f")
	}
	if !hasReq && cmd.ReqLines != "" {
		return rep.fail(exitUsage, kindUsage, "--nr has no request to trim; pass -r or -f")
	}
	if !hasRes && cmd.ResLines != "" {
		return rep.fail(exitUsage, kindUsage, "--ns has no response to trim; pass -s or -f")
	}

	reqHL, err := highlight.ParseHighlightSpec(cmd.ReqHL)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "parse --hr: %v", err)
	}
	resHL, err := highlight.ParseHighlightSpec(cmd.ResHL)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "parse --hs: %v", err)
	}

	markRe, err := compileRegex("--mark-re", cmd.MarkRe)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "%v", err)
	}
	redactRe, err := compileRegex("--redact-re", cmd.RedactRe)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "%v", err)
	}
	// The compiled patterns join the scrubber: their matches are secrets the
	// literal list never named — a token caught in a filename or a warning
	// stays hidden only if the pattern itself does the masking.
	rep.secretRes = redactRe

	// --mark's matches are found while rendering, so they count here too.
	if cmd.Context > 0 && len(reqHL) == 0 && len(resHL) == 0 && len(cmd.Mark) == 0 && len(cmd.MarkRe) == 0 {
		return rep.fail(exitUsage, kindUsage, "--context needs --hr, --hs, --mark or --mark-re to center on")
	}
	if cmd.Context > 0 {
		if cmd.ReqLines != "" {
			rep.warn("--context overrides --nr; ignoring it")
		}
		if cmd.ResLines != "" {
			rep.warn("--context overrides --ns; ignoring it")
		}
	}

	reqLines, err := parseLineSpec("--nr", cmd.ReqLines)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "%v", err)
	}
	resLines, err := parseLineSpec("--ns", cmd.ResLines)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "%v", err)
	}

	if hasReq {
		rep.addInput("request", reqSrc)
	}
	if hasRes {
		rep.addInput("response", resSrc)
	}

	// A --redact-keep at least as long as a literal present in the capture
	// would ship the whole secret — that is misuse, not a rendering choice.
	if cmd.RedactKeep > 0 {
		for _, lit := range cmd.Redact {
			present := bytes.Contains(reqSrc, []byte(lit)) || bytes.Contains(resSrc, []byte(lit))
			switch {
			case !present:
			case len(lit) <= cmd.RedactKeep:
				return rep.fail(exitUsage, kindUsage,
					"--redact-keep %d >= length %d of --redact %q — the whole value would stay visible",
					cmd.RedactKeep, len(lit), lit)
			case len(lit)-cmd.RedactKeep < 4:
				rep.warn("--redact-keep %d hides only %d char(s) of --redact %q",
					cmd.RedactKeep, len(lit)-cmd.RedactKeep, lit)
			}
		}
	}

	warnMarkRedactOverlap(rep, cmd.Mark, cmd.Redact)

	// The renderer resolves --mark while it draws, so it is the only place that
	// knows the view it produced; it hands back what it drew.
	facts := &render.HTTPFacts{}
	img, err := render.RenderHTTP(&render.HTTPConfig{
		Request:       string(reqSrc),
		Response:      string(resSrc),
		RequestHL:     reqHL,
		ResponseHL:    resHL,
		Mark:          cmd.Mark,
		MarkRe:        markRe,
		Redact:        slices.Concat(cmd.Redact, rep.redactFrags(cmd.RedactKeep)),
		RedactRe:      redactRe,
		RedactKeep:    cmd.RedactKeep,
		RedactAsked:   len(cmd.Redact)+len(cmd.RedactRe) > 0,
		MaxMarks:      cmd.MaxMarks,
		RequestLines:  reqLines,
		ResponseLines: resLines,
		Context:       cmd.Context,
		WrapWidth:     cmd.WrapWidth,
		Caption:       rep.scrub(cmd.Label),
		Facts:         facts,
		Warn:          func(msg string) { rep.warn("%s", msg) },
	})
	if err != nil {
		return rep.fail(exitRefused, kindRefused, "render: %v", err)
	}
	recordMarks(rep, facts.Marks)
	rep.rep.Source = &Source{Kind: "http", Path: srcPath}
	rep.scrubSecrets()
	rep.rep.Redactions = facts.Redacted
	rep.rep.Panels = &Panels{}
	if facts.Request.Present {
		c := cropOf(facts.Request.ViewExtent, facts.Request.Total)
		rep.rep.Panels.Request = &c
	}
	if facts.Response.Present {
		c := cropOf(facts.Response.ViewExtent, facts.Response.Total)
		rep.rep.Panels.Response = &c
	}
	if cmd.DryRun {
		return rep.doneDry(img)
	}
	if err := writePNG(img, cmd.Output, rep); err != nil {
		if errors.Is(err, errOutUsage) {
			return rep.fail(exitUsage, kindUsage, "%v", err)
		}
		return rep.fail(exitFail, kindOutput, "%v", err)
	}
	return rep.done(cmd.Output)
}

// splitCapture splits a Burp "Save item" style file — request block, then the
// response — into its two messages. The boundary is the response's status
// line: the first line starting with "HTTP/<digit>". A file that is a single
// message goes to the side its first line names, so `-f` works on a lone
// request or a lone response too.
func splitCapture(src []byte) (req, res []byte) {
	text := string(src)
	off := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "HTTP/") && len(line) > 5 && line[5] >= '0' && line[5] <= '9' {
			if off == 0 {
				return nil, src // a status line at the top: response only
			}
			return []byte(strings.TrimRight(text[:off], "\r\n")), []byte(text[off:])
		}
		off += len(line) + 1
	}
	return src, nil // no status line anywhere: request only
}

// looksHTTPStart checks the first line of a message for an HTTP shape — a
// request line "VERB path HTTP/n" or a status line "HTTP/n" — so a wrong
// file handed to -f cannot pass for a capture.
var httpStartRe = regexp.MustCompile(`^[A-Za-z]+\s+\S+\s+HTTP/`)

func looksHTTPStart(src []byte) bool {
	return looksHTTPResponse(src) || looksHTTPRequest(src)
}

func looksHTTPResponse(src []byte) bool {
	return strings.HasPrefix(startLine(src), "HTTP/")
}

func looksHTTPRequest(src []byte) bool {
	return httpStartRe.MatchString(startLine(src))
}

func startLine(src []byte) string {
	first := src
	if i := bytes.IndexByte(src, '\n'); i >= 0 {
		first = src[:i]
	}
	return strings.TrimSpace(string(first))
}

// compileRegex compiles a flag's patterns once, at the CLI edge, so a bad
// pattern is a usage error that names the flag — not a render failure.
func compileRegex(flag string, patterns []string) ([]*regexp.Regexp, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	out := make([]*regexp.Regexp, 0, len(patterns))
	for i := 0; i < len(patterns); i++ {
		p := patterns[i]
		re, err := regexp.Compile(p)
		// Kong splits flag lists on commas, so `.{0,3}` or `[a,b]` arrive as
		// pieces. An open paren fails the compile, but a lone `{` or `[` is a
		// legal literal — brace-balance is the only reliable tell. Rejoin
		// commas until the piece both balances and compiles.
		for (err != nil || unbalancedRE(p)) && i+1 < len(patterns) {
			i++
			p += "," + patterns[i]
			re, err = regexp.Compile(p)
		}
		if err != nil {
			return nil, fmt.Errorf("parse %s %q: %w", flag, p, err)
		}
		// A pattern that can match the empty string paints nothing useful —
		// zero-width matches are skipped anyway — and run through the
		// scrubber it would mask every character of every message.
		if re.MatchString("") {
			return nil, fmt.Errorf("%s %q can match the empty string — it would mask nothing precise", flag, p)
		}
		out = append(out, re)
	}
	return out, nil
}

// unbalancedRE reports p as a fragment of a split pattern: an opener rune
// without its closer means kong's comma-split cut it in half.
func unbalancedRE(p string) bool {
	depth := 0
	esc := false
	for _, r := range p {
		if esc {
			esc = false
			continue
		}
		if r == '\\' {
			esc = true
			continue
		}
		switch r {
		case '(', '{', '[':
			depth++
		case ')', '}', ']':
			depth--
		}
	}
	return depth != 0
}

// promptRGB is the classic shell-prompt green on the "$ " prefix.
const promptR, promptG, promptB = 0x3f, 0xb9, 0x50

// recolorPrompt paints the "$ " prefix on the transcript's first line in the
// prompt colour. The text lexer gives the whole line one colour; the prompt
// needs its own, like a real terminal draws it.
func recolorPrompt(lines []highlight.LineTokens) {
	const plen = 2 // len("$ ")
	if len(lines) == 0 || lines[0].IsGap {
		return
	}
	var out []highlight.Token
	left := plen
	for _, t := range lines[0].Tokens {
		if left == 0 {
			out = append(out, t)
			continue
		}
		runes := []rune(t.Text)
		if len(runes) <= left {
			out = append(out, highlight.Token{Text: t.Text, R: promptR, G: promptG, B: promptB})
			left -= len(runes)
			continue
		}
		out = append(out,
			highlight.Token{Text: string(runes[:left]), R: promptR, G: promptG, B: promptB},
			highlight.Token{Text: string(runes[left:]), R: t.R, G: t.G, B: t.B})
		left = 0
	}
	lines[0].Tokens = out
}

// termMarks finds the -m literals and --mark-re matches in a transcript and
// records their stats. There is no -H for a transcript: lines have no stable
// numbering worth pointing at.
func termMarks(lines []highlight.LineTokens, needles []string, res []*regexp.Regexp, maxMarks int, rep *reporter) highlight.Marks {
	msgs := [][]highlight.LineTokens{lines}
	m1, s1 := highlight.FindMarks(msgs, needles, maxMarks)
	m2, s2 := highlight.FindRegexMarks(msgs, res, maxMarks)
	recordMarks(rep, append(s1, s2...))
	return m1[0].Merge(m2[0])
}

// ansiRe covers the escape sequences a terminal consumes invisibly: CSI
// (SGR, cursor), OSC (title, hyperlinks) closed by BEL or ST, charset picks,
// and the one-byte controls. The transcript evidence shows what a terminal
// SHOWS — and nothing should be matchable-invisible.
var ansiRe = regexp.MustCompile("\x1b\\[[0-9;:?!]*[ -/]*[@-~]|\x1b\\][^\a\x1b]*(?:\a|\x1b\\\\)|\x1b[()][0-9A-B]|\x1b[=>NO]|\x0f|\x0e")

// normalizeInput makes the text the matcher sees equal the cells the renderer
// draws. ANSI escapes splice a needle while showing it ("Sup3r^[[31mD4ta!"),
// and zero-width codepoints — mid-line BOM, combining marks — count as a
// column in match offsets but advance zero in the glyph run, which shifts the
// redaction box off its text and lets the needle bleed out of the box.
func normalizeInput(b []byte, rep *reporter) []byte {
	stripped, n := stripANSI(b)
	if n > 0 {
		rep.warn("stripped %d ANSI escape sequences before matching", n)
	}
	out, m := normalizeZeroWidth(stripped)
	if m > 0 {
		rep.warn("removed %d invisible/format characters (BOM, ZWSP, combining marks) so the needle stays contiguous", m)
	}
	return out
}

// normalizeZeroWidth DELETES codepoints that carry no standalone glyph —
// format chars (Cf: BOM, ZWSP, ZWJ, bidi controls), combining marks (Mn/Me)
// and soft hyphen. Replacing them would still leave a rune the needle does
// not contain; deleting them makes the spliced needle contiguous again, and
// because matcher and renderer share the normalized text, columns stay
// aligned and the box lands on the needle.
func normalizeZeroWidth(b []byte) ([]byte, int) {
	var sb strings.Builder
	sb.Grow(len(b))
	n := 0
	for _, r := range string(b) {
		if r == '\uFEFF' || r == '\u00AD' || unicode.Is(unicode.Cf, r) ||
			unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) {
			n++
			continue
		}
		sb.WriteRune(r)
	}
	if n == 0 {
		return b, 0
	}
	return []byte(sb.String()), n
}

func stripANSI(b []byte) ([]byte, int) {
	n := 0
	out := ansiRe.ReplaceAllFunc(b, func(m []byte) []byte {
		n++
		return nil
	})
	return out, n
}

// needleFlags take the values a secrecy tool must hide — mark and redact
// needles alike, in both --flag value and --flag=value spellings.
var needleFlags = map[string]bool{
	"-m": true, "--mark": true, "--mark-re": true,
	"--redact": true, "--redact-re": true,
}

// argvNeedleValues collects the tokens that follow needle flags, for the
// parse-error path where the parsed secrets set does not exist yet: kong's
// message and the echoed command both repeat the offending value, and only
// needle values are masked — a file path stays readable, so the echoed
// command remains a replayable record.
func argvNeedleValues(args []string) []string {
	var out []string
	for i, a := range args {
		if a == "--" {
			break
		}
		name, val, hasEq := strings.Cut(a, "=")
		if !needleFlags[name] {
			continue
		}
		if hasEq {
			if val != "" {
				out = append(out, val)
			}
			continue
		}
		if i+1 < len(args) {
			out = append(out, args[i+1])
		}
	}
	return out
}

// nonneg enforces the documented floor the flags' help already states — a
// negative --timeout or --wrap-width silently accepted is a silent success.
func nonneg(rep *reporter, flag string, v int) int {
	if v < 0 {
		return rep.fail(exitUsage, kindUsage, "%s must be >= 0, got %d", flag, v)
	}
	return 0
}

// validateRedact refuses redact arguments that would silently produce an
// unprotected image: an empty needle never matches — and would have shipped a
// "clean" capture — a negative keep is meaningless, and a needle of a char or
// two masks half the alphabet (and the report's own words).
func validateRedact(rep *reporter, literals, patterns []string, keep int) int {
	// Kong drops an empty value from a string slice — "--redact ''" never
	// reaches `literals`. The raw argv is the only faithful source, and an
	// empty needle is the quietest possible footgun: a clean image, no
	// warning, every secret visible.
	for i, a := range os.Args {
		if a == "--redact" || a == "--redact-re" || a == "--mark" || a == "--mark-re" || a == "-m" {
			if i+1 >= len(os.Args) || os.Args[i+1] == "" {
				return rep.fail(exitUsage, kindUsage, "%s needs a non-empty value", a)
			}
		}
		if a == "--redact=" || a == "--redact-re=" || a == "--mark=" || a == "--mark-re=" || a == "-m=" {
			return rep.fail(exitUsage, kindUsage, "%s needs a non-empty value", a[:len(a)-1])
		}
	}
	if keep < 0 {
		return rep.fail(exitUsage, kindUsage, "--redact-keep must be >= 0, got %d", keep)
	}
	for i, s := range literals {
		if strings.Contains(s, "\n") {
			rep.warn("--redact needles match per line — a secret spanning lines needs one flag per part")
		}
		if s == "" {
			return rep.fail(exitUsage, kindUsage,
				"--redact #%d is empty — nothing would be hidden", i+1)
		}
		if len(s) <= 2 {
			rep.warn("--redact %q is %d chars — it masks far more than a secret (quote a literal comma as '\\,')", s, len(s))
		}
	}
	for i, p := range patterns {
		if p == "" {
			return rep.fail(exitUsage, kindUsage,
				"--redact-re #%d is empty — nothing would be hidden", i+1)
		}
	}
	return 0
}

// warnMarkRedactOverlap notes when a needle is both circled and hidden: the
// mark stats still count the match, but the black box is what the image shows.
func warnMarkRedactOverlap(rep *reporter, marks, redacts []string) {
	for _, m := range marks {
		for _, r := range redacts {
			if m != "" && m == r {
				rep.warn("--mark %q is also --redact'ed — the box covers its highlight", m)
			}
		}
	}
}

// termRedact finds the literals to hide and warns when one matched nothing —
// a redact that hides nothing looks exactly like a clean image. A nonzero
// second return is the exit code of a fatal misuse (e.g. keep exposing the
// whole secret); the caller returns it verbatim.
func termRedact(lines []highlight.LineTokens, needles []string, res []*regexp.Regexp, keep int, rep *reporter) (highlight.Marks, int) {
	found, stats := highlight.FindMarks([][]highlight.LineTokens{lines}, needles, 0)
	foundRe, reStats := highlight.FindRegexMarks([][]highlight.LineTokens{lines}, res, 0)
	stats = append(stats, reStats...)
	kept := highlight.KeepSpans(found[0].Merge(foundRe[0]), keep)
	matched := 0
	for i, st := range stats {
		matched += st.Matches
		literal := i < len(stats)-len(reStats)
		switch {
		case st.Matches == 0 && literal:
			rep.warn("--redact %q matched nothing", st.Literal)
			if strings.ContainsAny(st.Literal, "\t\n\r") {
				rep.helpf("the needle holds a control char — it only matches if the file has it too")
			} else {
				rep.helpf("nothing was masked — check the value or use --redact-re '<pattern>'")
			}
		case st.Matches == 0:
			rep.warn("--redact-re %q matched nothing", st.Literal)
			rep.helpf("nothing was masked — check the pattern")
		case literal && keep > 0 && len(st.Literal) <= keep:
			return nil, rep.fail(exitUsage, kindUsage,
				"--redact-keep %d leaves nothing hidden in --redact %q — lower it or drop it",
				keep, st.Literal)
		case literal && keep > 0 && len(st.Literal)-keep < 4:
			rep.warn("--redact-keep %d hides only %d char(s) of --redact %q",
				keep, len(st.Literal)-keep, st.Literal)
		}
	}
	if keep > 0 && matched > 0 && kept == nil {
		return nil, rep.fail(exitUsage, kindUsage,
			"--redact-keep %d leaves nothing hidden — lower it or drop it", keep)
	}
	return kept, 0
}

// terminalRun gathers what a terminal-mode render needs; exec and term differ
// only in where the lines come from.
type terminalRun struct {
	lines       []highlight.LineTokens
	title       string
	theme       *render.Theme
	caption     string
	status      string // exit status or timeout, printed in the footnote
	chrome      string
	shadow      bool
	wrapWidth   int
	marks       highlight.Marks
	redact      highlight.Marks
	redactAsked bool
	output      string
	dryRun      bool
}

// finishTerminal renders a transcript the way a terminal window looks: no
// line numbers — a transcript has no source lines — under a title bar.
func finishTerminal(t *terminalRun, rep *reporter) int {
	rcfg := &render.Config{
		Lines:          t.lines,
		Title:          t.title,
		HighlightLines: t.marks,
		RedactRegions:  t.redact,
		RedactAsked:    t.redactAsked,
		Theme:          t.theme,
		Wrap:           t.wrapWidth > 0,
		WrapWidth:      t.wrapWidth,
		Terminal:       true,
		Caption:        t.caption,
		SourceRef:      t.status,
		WindowChrome:   t.chrome == "window",
		Shadow:         t.shadow,
		Warn:           func(msg string) { rep.warn("%s", msg) },
	}
	img, err := render.Render(rcfg)
	if err != nil {
		return rep.fail(exitRefused, kindRefused, "render: %v", err)
	}
	rep.rep.Redactions = rcfg.Redacted
	if t.dryRun {
		return rep.doneDry(img)
	}
	if err := writePNG(img, t.output, rep); err != nil {
		if errors.Is(err, errOutUsage) {
			return rep.fail(exitUsage, kindUsage, "%v", err)
		}
		return rep.fail(exitFail, kindOutput, "%v", err)
	}
	total := len(t.lines)
	rep.rep.Crop = &Crop{First: 1, Last: total, Shown: total, Total: &total}
	return rep.done(t.output)
}

// execFlagNames is every flag `taker exec` knows — used to catch the trap
// where one of them lands inside the command's argv instead of before --.
// The value form `--flag=v` matches by prefix.
var execFlagNames = map[string]bool{
	"-o": true, "--output": true, "-t": true, "--title": true,
	"-T": true, "--theme": true, "-L": true, "--label": true,
	"-m": true, "--mark": true, "--mark-re": true, "--max-marks": true,
	"--redact": true, "--redact-re": true, "--redact-keep": true,
	"--wrap-width": true, "--chrome": true, "--shadow": true,
	"--timeout": true, "--sh": true, "--dry-run": true,
	"--json": true, "--toon": true, "-h": true, "--help": true,
	"-n": true, "--lines": true, "-v": true, "--version": true,
}

// stdinHasData reports whether at least one byte is readable within the
// grace period. A real producer — cat file | taker — has data in the pipe
// buffer before the process even starts; a harness that opened stdin and
// never writes stalls forever, which is the failure this exists to catch.
// Peek does not consume, so a fast probe leaves the stream intact.
func stdinHasData(r *bufio.Reader, grace time.Duration) bool {
	ch := make(chan bool, 1)
	go func() {
		_, err := r.Peek(1)
		ch <- err == nil
	}()
	select {
	case ok := <-ch:
		return ok
	case <-time.After(grace):
		return false
	}
}

// takerFlagIn finds a taker flag sitting where command argv was expected. It
// only runs when the caller did not use --, because after -- every token is
// the command's own business.
func takerFlagIn(argv []string) string {
	for _, a := range argv {
		name := a
		if i := strings.IndexByte(a, '='); i >= 0 {
			name = a[:i]
		}
		if execFlagNames[name] {
			return a
		}
	}
	return ""
}

func runExec(cmd *ExecCmd, rep *reporter) int {
	rep.setSecretsAll(slices.Concat(cmd.Redact, cmd.RedactRe), cmd.Redact)
	if code := validateRedact(rep, cmd.Redact, cmd.RedactRe, cmd.RedactKeep); code != 0 {
		return code
	}
	for _, c := range [][2]any{
		{"--wrap-width", cmd.WrapWidth}, {"--max-marks", cmd.MaxMarks}, {"--timeout", cmd.Timeout},
	} {
		if code := nonneg(rep, c[0].(string), c[1].(int)); code != 0 {
			return code
		}
	}
	theme, err := resolveTheme(cmd.Theme)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "%v", err)
	}
	if cmd.Chrome != "editor" && cmd.Chrome != "window" {
		return rep.fail(exitUsage, kindUsage, "--chrome must be 'editor' or 'window', got %q", cmd.Chrome)
	}
	cmd.WrapWidth = clampWrapWidth(cmd.WrapWidth, false, rep)

	var argv []string
	var cmdStr string
	switch {
	case cmd.Shell != "" && len(cmd.Args) > 0:
		rep.helpf("drop one: --sh '<pipeline>' or a command after --")
		return rep.fail(exitUsage, kindUsage, "give either --sh or a command, not both")
	case cmd.Shell != "":
		argv = []string{"sh", "-c", cmd.Shell}
		cmdStr = cmd.Shell
	case len(cmd.Args) == 0:
		rep.helpf("taker exec -- id")
		return rep.fail(exitUsage, kindUsage, "no command: taker exec -- <command> or taker exec --sh '<pipeline>'")
	default:
		argv = cmd.Args
		// kong keeps the "--" separator itself in the passthrough args.
		sep := len(argv) > 0 && argv[0] == "--"
		if sep {
			argv = argv[1:]
			// After -- every token is the command's — including things that
			// look like our own flags. A warning states where each one went
			// so `exec -- id -o x.png` can't quietly misplace the output.
			if bad := takerFlagIn(argv); bad != "" {
				rep.warn("%q after -- goes to the command; the PNG goes to %s", bad, cmd.Output)
			}
		} else if bad := takerFlagIn(argv); bad != "" {
			// The cold-start trap: `taker exec id -o out.png` runs
			// "id -o out.png" and writes the evidence to the default path —
			// silent success doing the wrong thing. Error and name the fix.
			rep.helpf("taker exec <taker-flags> -- %s", argv[0])
			return rep.fail(exitUsage, kindUsage,
				"%q is a taker flag sitting inside the command; taker's own flags go before --", bad)
		}
		if len(argv) == 0 {
			rep.helpf("taker exec -- id")
			return rep.fail(exitUsage, kindUsage, "no command: taker exec -- <command> or taker exec --sh '<pipeline>'")
		}
		cmdStr = strings.Join(argv, " ")
	}
	rep.note("$ %s", cmdStr)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cmd.Timeout)*time.Second)
	defer cancel()
	// Kill the whole process group on the deadline: a child that forks a
	// sleeper leaves the pipe open, and CommandContext's lone kill would
	// leave us blocked reading it until the grandchild exits on its own.
	child := newChild(argv)
	var buf bytes.Buffer
	child.Stdout, child.Stderr = &buf, &buf
	var runErr error
	if err := child.Start(); err != nil {
		runErr = err
	} else {
		waitc := make(chan error, 1)
		go func() { waitc <- child.Wait() }()
		select {
		case runErr = <-waitc:
		case <-ctx.Done():
			killGroup(child)
			<-waitc
		}
	}
	out := buf.Bytes()

	// The exit status is part of the evidence: a failed exploit looks different
	// from a successful one, and a killed long-runner is not an empty result.
	var status string
	rep.rep.Exec = &ExecReport{}
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		status = fmt.Sprintf("killed after %ds", cmd.Timeout)
		rep.rep.Exec.TimedOut = true
	case runErr != nil:
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			rep.helpf("check that the command exists on PATH and its argv is valid")
			return rep.fail(exitInput, kindInput, "run %q: %v", cmdStr, runErr)
		}
		status = fmt.Sprintf("exit %d", exitErr.ExitCode())
		code := exitErr.ExitCode()
		rep.rep.Exec.Exit = &code
	default:
		status = "exit 0"
		zero := 0
		rep.rep.Exec.Exit = &zero
	}

	transcript := "$ " + cmdStr + "\n" + string(out)
	// A real terminal consumes escape sequences invisibly; a needle split by
	// one — `Sup3r\x1b[31mD4ta!` — would stay legible while "matched nothing"
	// warned. Strip before matching, and declare the transform.
	stripped, nANSI := stripANSI([]byte(transcript))
	stripped, nZW := normalizeZeroWidth(stripped)
	if nZW > 0 {
		rep.warn("removed %d invisible/format characters so the needle stays contiguous", nZW)
	}
	if nANSI > 0 {
		rep.warn("stripped %d ANSI escape sequences from the transcript", nANSI)
	}
	lines, err := highlight.Tokenize(string(stripped), "text", "", theme.Chroma)
	if err != nil {
		return rep.fail(exitRefused, kindRefused, "tokenize transcript: %v", err)
	}
	recolorPrompt(lines)

	markRe, err := compileRegex("--mark-re", cmd.MarkRe)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "%v", err)
	}
	redactRe, err := compileRegex("--redact-re", cmd.RedactRe)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "%v", err)
	}
	// The compiled patterns join the scrubber: their matches are secrets the
	// literal list never named — a token caught in a filename or a warning
	// stays hidden only if the pattern itself does the masking.
	rep.secretRes = redactRe

	rep.addInput("transcript", []byte(transcript))
	rep.metaKV = append(rep.metaKV, [2]string{"taker:exec", cmdStr})
	rep.rep.Source = &Source{Kind: "exec", Path: cmdStr}
	rep.scrubSecrets()

	redactM, code := termRedact(lines, slices.Concat(cmd.Redact, rep.redactFrags(cmd.RedactKeep)), redactRe, cmd.RedactKeep, rep)
	if code != 0 {
		return code
	}
	warnMarkRedactOverlap(rep, cmd.Mark, cmd.Redact)
	return finishTerminal(&terminalRun{
		lines:       lines,
		title:       rep.scrub(cmd.Title),
		theme:       theme,
		caption:     rep.scrub(cmd.Label),
		status:      status,
		chrome:      cmd.Chrome,
		shadow:      cmd.Shadow,
		wrapWidth:   cmd.WrapWidth,
		marks:       termMarks(lines, cmd.Mark, markRe, cmd.MaxMarks, rep),
		redact:      redactM,
		redactAsked: len(cmd.Redact)+len(cmd.RedactRe) > 0,
		output:      cmd.Output,
		dryRun:      cmd.DryRun,
	}, rep)
}

func runTerm(cmd *TermCmd, rep *reporter) int {
	rep.setSecretsAll(slices.Concat(cmd.Redact, cmd.RedactRe), cmd.Redact)
	if code := validateRedact(rep, cmd.Redact, cmd.RedactRe, cmd.RedactKeep); code != 0 {
		return code
	}
	for _, c := range [][2]any{
		{"--wrap-width", cmd.WrapWidth}, {"--max-marks", cmd.MaxMarks},
	} {
		if code := nonneg(rep, c[0].(string), c[1].(int)); code != 0 {
			return code
		}
	}
	theme, err := resolveTheme(cmd.Theme)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "%v", err)
	}
	if cmd.Chrome != "editor" && cmd.Chrome != "window" {
		return rep.fail(exitUsage, kindUsage, "--chrome must be 'editor' or 'window', got %q", cmd.Chrome)
	}
	cmd.WrapWidth = clampWrapWidth(cmd.WrapWidth, false, rep)

	src, err := os.ReadFile(cmd.File)
	if err != nil {
		return rep.fail(exitInput, kindInput, "%v", err)
	}
	if len(bytes.TrimSpace(src)) == 0 {
		return rep.fail(exitInput, kindInput, "%s is empty", cmd.File)
	}
	// Same contract as exec: escape codes render nothing on a real terminal,
	// so they must not hide a needle from the matcher either.
	stripped, nANSI := stripANSI(src)
	stripped, nZW := normalizeZeroWidth(stripped)
	if nZW > 0 {
		rep.warn("removed %d invisible/format characters so the needle stays contiguous", nZW)
	}
	if nANSI > 0 {
		rep.warn("stripped %d ANSI escape sequences from the transcript", nANSI)
	}
	lines, err := highlight.Tokenize(string(stripped), "text", cmd.File, theme.Chroma)
	if err != nil {
		return rep.fail(exitRefused, kindRefused, "tokenize transcript: %v", err)
	}

	markRe, err := compileRegex("--mark-re", cmd.MarkRe)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "%v", err)
	}
	redactRe, err := compileRegex("--redact-re", cmd.RedactRe)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "%v", err)
	}
	// The compiled patterns join the scrubber: their matches are secrets the
	// literal list never named — a token caught in a filename or a warning
	// stays hidden only if the pattern itself does the masking.
	rep.secretRes = redactRe

	rep.addInput("transcript", src)
	rep.rep.Source = &Source{Kind: "transcript", Path: cmd.File}
	rep.scrubSecrets()

	redactM, code := termRedact(lines, slices.Concat(cmd.Redact, rep.redactFrags(cmd.RedactKeep)), redactRe, cmd.RedactKeep, rep)
	if code != 0 {
		return code
	}
	warnMarkRedactOverlap(rep, cmd.Mark, cmd.Redact)
	return finishTerminal(&terminalRun{
		lines:       lines,
		title:       rep.scrub(cmd.Title),
		theme:       theme,
		caption:     rep.scrub(cmd.Label),
		chrome:      cmd.Chrome,
		shadow:      cmd.Shadow,
		wrapWidth:   cmd.WrapWidth,
		marks:       termMarks(lines, cmd.Mark, markRe, cmd.MaxMarks, rep),
		redact:      redactM,
		redactAsked: len(cmd.Redact)+len(cmd.RedactRe) > 0,
		output:      cmd.Output,
		dryRun:      cmd.DryRun,
	}, rep)
}

// listThemes prints the palettes. With --json the list travels as data, so a
// consumer parses nothing.
func listThemes(rep *reporter) int {
	names := make([]string, 0, len(render.Themes))
	for k := range render.Themes {
		names = append(names, k)
	}
	sort.Strings(names)

	for _, name := range names {
		rep.rep.Themes = append(rep.rep.Themes, ThemeReport{
			Name:     name,
			Label:    render.Themes[name].Name,
			HTTPOnly: render.Themes[name].HTTPOnly,
		})
	}
	if rep.format == "" {
		fmt.Fprintln(rep.out, "Available themes:")
		for _, name := range names {
			note := ""
			switch {
			case name == "vscode-dark":
				note = "  (default)"
			case render.Themes[name].HTTPOnly:
				note = "  (automatic for `taker http` \u2014 not selectable)"
			}
			fmt.Fprintf(rep.out, "    %-20s  %s%s\n", name, render.Themes[name].Name, note)
		}
	}
	// A successful listing is ok:true like any other run — a consumer that
	// branches on ok would misread exit 0 with ok:false as a failure.
	rep.rep.OK = true
	rep.rep.Exit = exitOK
	return rep.emit(exitOK)
}

// resolveLineSpec picks which lines to render: --context expands around each
// highlighted line, otherwise the explicit spec (empty = every line). Shared by
// both commands so `taker http` trims the same way `taker` does.
//
// Context only applies to a panel that has highlights to center on. In HTTP mode
// one side often carries none — the request in a response-only finding — and
// that side must still render, not abort the run.
func resolveLineSpec(flag, spec string, hl highlight.Marks, context int, rep *reporter) ([]int, error) {
	if s := highlight.ContextSpec(hl, context); s != nil {
		if spec != "" {
			rep.warn("--context overrides %s; ignoring it", flag)
		}
		return s, nil
	}
	return parseLineSpec(flag, spec)
}

// parseLineSpec returns an error instead of exiting, so the caller can report it
// with the flag named and the right exit code.
func parseLineSpec(flag, spec string) ([]int, error) {
	parsed, err := highlight.ParseLineSpec(spec)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", flag, err)
	}
	return parsed, nil
}

// sourceInfo is where the rendered bytes came from. Ref is the label the image
// carries, Rev the commit a report can be checked against.
type sourceInfo struct {
	Kind string // "file", "git" or "stdin"
	Path string
	Ref  string
	Rev  string
}

// readGitBlob reads <rev>:<path> out of the repository in the working directory.
//
// It exists to delete a class of error rather than to detect it: evidence has to
// show the code of the revision being reported, and a snippet copied out of an
// editor is a transcription that nothing can check. Rendering from the object
// store means there is nothing to transcribe.
func readGitBlob(spec string) ([]byte, sourceInfo, error) {
	return readGitBlobIn("", spec)
}

// readGitBlobIn takes the repository directory so a test can point it at a
// throwaway repo instead of changing the process's working directory.
func readGitBlobIn(dir, spec string) ([]byte, sourceInfo, error) {
	rev, path, ok := strings.Cut(spec, ":")
	if !ok || rev == "" || path == "" {
		return nil, sourceInfo{}, fmt.Errorf("--git takes <rev>:<path>, for example HEAD:src/auth/login.py")
	}
	// The spec travels inside one argument ("rev:path"), so a revision that
	// starts with '-' is handed to git as a flag — and git has flags like
	// --output that write files.
	if strings.HasPrefix(rev, "-") {
		return nil, sourceInfo{}, fmt.Errorf("--git %s: revision must not start with '-'", spec)
	}
	sha, err := gitOut(dir, "rev-parse", rev+"^{commit}")
	if err != nil {
		return nil, sourceInfo{}, fmt.Errorf("--git %s: %w", spec, err)
	}
	out, err := gitOut(dir, "show", rev+":"+path)
	if err != nil {
		return nil, sourceInfo{}, fmt.Errorf("--git %s: %w", spec, err)
	}
	short := strings.TrimSpace(sha)
	if len(short) > 12 {
		short = short[:12]
	}
	return []byte(out), sourceInfo{
		Kind: "git",
		Path: filepath.ToSlash(path),
		Ref:  fmt.Sprintf("%s @ %s", filepath.ToSlash(path), short),
		Rev:  short,
	}, nil
}

// gitOut runs git and returns stdout, with git's own message when it fails: it
// already names the revision, the path or the missing repository.
func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return string(out), nil
}

// excerptTotal is the file's line count when the whole file was read, and 0 when
// the input started partway down it.
func excerptTotal(rendered, firstLine int) int {
	if firstLine > 1 {
		return 0
	}
	return rendered
}

// clampWrapWidth reports a too-narrow width once, at the flag level. Render
// clamps silently too, but doing it here keeps a two-panel HTTP run from
// printing the same complaint for each panel.
func clampWrapWidth(w int, httpMode bool, rep *reporter) int {
	const minCols = 20
	if w <= 0 {
		return w
	}
	if w < minCols {
		rep.warn("--wrap-width %d is below the %d-column minimum; using %d", w, minCols, minCols)
		return minCols
	}
	if max := render.MaxWrapCols(httpMode); w > max {
		rep.warn("--wrap-width %d is wider than the canvas; wrapping at %d columns", w, max)
		return max
	}
	return w
}

func resolveTheme(name string) (*render.Theme, error) {
	theme, ok := render.Themes[strings.ToLower(name)]
	if !ok {
		return nil, fmt.Errorf("unknown theme %q. Run --list-themes to see options.", name)
	}
	// Its style only covers the HTTP lexer's token types, so source code comes
	// out flat — every keyword falling back to the plain text color.
	if theme.HTTPOnly {
		return nil, fmt.Errorf("theme %q is only for `taker http`; source code renders without highlighting.\n"+
			"       Use vscode-dark (default) or run --list-themes.", name)
	}
	return theme, nil
}

// writePNG encodes to a temporary file and renames it into place.
//
// Writing straight to path would truncate whatever is already there before
// encoding, and os.Exit skips deferred closes — so a failure part-way left a
// corrupt PNG where a good piece of evidence used to be.
// errOutUsage marks -o mistakes that are the caller's fault, not the fs's.
var errOutUsage = errors.New("bad output path")

func writePNG(img image.Image, path string, rep *reporter) error {
	if path == "" {
		return fmt.Errorf("%w: the path is empty", errOutUsage)
	}
	if path == "-" {
		return fmt.Errorf("%w: '-' is not a real path", errOutUsage)
	}
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		return fmt.Errorf("%w: %s is a directory", errOutUsage, path)
	}
	if _, err := os.Stat(path); err == nil {
		rep.warn("overwriting existing %s", path)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".taker-*.png")
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	tmpName := tmp.Name()

	fail := func(err error) error {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	meta := [][2]string{
		{"taker:version", version},
		{"taker:generated", rep.rep.Generated},
	}
	if rep.rep.Command != "" {
		meta = append(meta, [2]string{"taker:command", rep.rep.Command})
	}
	meta = append(meta, rep.metaKV...)
	data, err := pngWithMeta(img, meta)
	if err != nil {
		return fail(fmt.Errorf("encode png: %w", err))
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(fmt.Errorf("write output: %w", err))
	}
	if err := tmp.Close(); err != nil {
		return fail(fmt.Errorf("write output: %w", err))
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		// The temp name is an implementation detail — report the target.
		return fmt.Errorf("save output: %s: %s", path, strings.ReplaceAll(err.Error(), tmpName, filepath.Base(path)))
	}
	// On stderr: with --json, stdout carries the report and nothing else.
	rep.note("wrote %s", path)
	return nil
}

// samePath compares two paths after Abs — `-o` set to the input file would
// destroy the evidence's source, and a warning does not undo a rename.
func samePath(a, b string) bool {
	aa, ea := filepath.Abs(a)
	bb, eb := filepath.Abs(b)
	return ea == nil && eb == nil && aa == bb
}
