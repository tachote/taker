package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"image"
	"io"
	"io/fs"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/alecthomas/kong"
	"github.com/tachote/taker/highlight"
	"github.com/tachote/taker/render"
	"golang.org/x/text/unicode/norm"
)

// SnapCmd renders a single code file.
type SnapCmd struct {
	File       string   `arg:"" optional:"" help:"Source file to render (reads stdin if omitted)."`
	Lang       string   `short:"l" help:"Language for syntax highlighting (auto-detected if omitted)."`
	Lines      string   `short:"n" help:"Lines to include, e.g. '1,5,10-20'. Ignored when --context is set."`
	FirstLine  int      `name:"first-line" default:"1" help:"Line number of the input's first line. Set it when piping an excerpt, so the gutter and --highlight agree with the real file."`
	Git        string   `name:"git" placeholder:"<rev>:<path>" help:"Render <path> at <rev> instead of the working-tree copy, e.g. HEAD:src/auth/login.py."`
	Highlight  string   `short:"H" help:"Regions to highlight: '15', '13,15', '10-20', or '15:20-45' for columns."`
	Mark       []string `short:"m" sep:"none" help:"Highlight every occurrence of this text; a comma is part of it. Repeat -m for more."`
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
	TOON       bool     `name:"toon" help:"Write the report as TOON — same document, typically 25-40% fewer tokens than --json."`
}

// HTTPCmd renders HTTP request/response evidence. Both sides, one side, or a
// single combined capture (what Burp's "Copy to file" produces) are accepted.
type HTTPCmd struct {
	Request    string   `short:"r" help:"File containing the HTTP request."`
	Response   string   `short:"s" help:"File containing the HTTP response."`
	File       string   `short:"f" help:"Combined capture file holding request then response (Burp 'Save item' format); the response starts at the first HTTP/ status line."`
	ReqHL      string   `name:"hr" help:"Regions to highlight in request: '8-9' or '8:20-45' for columns."`
	ResHL      string   `name:"hs" help:"Regions to highlight in response: '4' or '4:12-60' for columns."`
	Mark       []string `short:"m" sep:"none" help:"Highlight every occurrence of this text in either panel; a comma is part of it. Repeat -m for more."`
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
	TOON       bool     `name:"toon" help:"Write the report as TOON — same document, typically 25-40% fewer tokens than --json."`
}

// ExecCmd runs a command and renders the terminal session — the prompt line
// plus stdout/stderr — as evidence. The proof of an RCE is `id` and its output
// in the same frame, not a screenshot of the analyst's own terminal.
type ExecCmd struct {
	Args       []string `arg:"" name:"command" passthrough:"" optional:"" help:"Command to run. Put it after -- so its flags stay its own."`
	Shell      string   `name:"sh" help:"Run this command line through sh -c (for pipes and quoting)."`
	Timeout    int      `name:"timeout" default:"15" help:"Seconds before the command is killed (1 to 9223372036)."`
	Output     string   `short:"o" default:"output.png" help:"Output PNG file path."`
	Label      string   `short:"L" help:"Caption strip stating what the image proves."`
	Mark       []string `short:"m" sep:"none" help:"Highlight every occurrence of this text; a comma is part of it. Repeat -m for more."`
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
	TOON       bool     `name:"toon" help:"Write the report as TOON — same document, typically 25-40% fewer tokens than --json."`
}

// TermCmd renders a saved terminal transcript (typescript, copied output)
// through the same terminal look as exec, without running anything.
type TermCmd struct {
	File       string   `arg:"" required:"" help:"Transcript file to render."`
	Output     string   `short:"o" default:"output.png" help:"Output PNG file path."`
	Label      string   `short:"L" help:"Caption strip stating what the image proves."`
	Mark       []string `short:"m" sep:"none" help:"Highlight every occurrence of this text; a comma is part of it. Repeat -m for more."`
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
	TOON       bool     `name:"toon" help:"Write the report as TOON — same document, typically 25-40% fewer tokens than --json."`
}

var cli struct {
	Snap    SnapCmd `cmd:"" default:"withargs" help:"Render a code file as a PNG screenshot."`
	HTTP    HTTPCmd `cmd:"http" help:"Render HTTP request/response evidence."`
	Exec    ExecCmd `cmd:"exec" help:"Run a command and render its terminal output as evidence. Example: taker exec -o out.png -- id"`
	Term    TermCmd `cmd:"term" help:"Render a saved terminal transcript as evidence."`
	Version bool    `short:"v" name:"version" help:"Print version and exit."`
}

// version is set by GoReleaser via -ldflags "-X main.version=..."; init
// falls back to the build info for every other build.
var version = "dev"

func init() { version = resolveVersion(version, debug.ReadBuildInfo) }

// resolveVersion keeps an ldflag version and otherwise reads the one the Go
// toolchain recorded. `go install …@v0.3.0` and a `go build` in a checkout
// both embed a module version, and printing "dev" for them put
// taker:version=dev into every PNG they made — provenance that names no
// build. A build with no module version still names its commit, when the
// toolchain recorded one.
func resolveVersion(ldflag string, read func() (*debug.BuildInfo, bool)) string {
	if ldflag != "dev" {
		return ldflag
	}
	info, ok := read()
	if !ok || info == nil {
		return ldflag
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return strings.TrimPrefix(v, "v")
	}
	var rev string
	dirty := false
	for _, kv := range info.Settings {
		switch kv.Key {
		case "vcs.revision":
			rev = kv.Value
		case "vcs.modified":
			dirty = kv.Value == "true"
		}
	}
	if rev == "" {
		return ldflag
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if dirty {
		return ldflag + "+" + rev + ".dirty"
	}
	return ldflag + "+" + rev
}

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
		mode := parseErrorMode(parser.Model, os.Args[1:])
		// Honor the format flag even though parsing died — a caller that
		// asked for JSON gets JSON on every path. Only needle-flag values
		// are secrets to mask; file paths and option values stay replayable.
		rep := newReporter(argvFormat(os.Args[1:]), mode)
		rep.argv = os.Args
		rep.secrets = argvNeedleValues(os.Args[1:])
		// The patterns' MATCHES are secrets too, and a parse error is the
		// earliest failure there is: a typo in any flag echoed a file name
		// such as tok_ABC123.py in the command, in kong's "unexpected
		// argument" message and on stderr. A pattern that does not compile
		// cannot scrub anything; its own text is already masked above.
		if res, err := compileRegex("--redact-re", argvRedactRe(os.Args[1:])); err == nil {
			rep.secretRes = res
		}
		rep.scrubSecrets()
		// The mode's own help: `taker --help` lists only the commands. The
		// flags also travel inline, so an agent can fix the call without a
		// second round trip.
		rep.helpf("run 'taker %s --help' for the valid flags", mode)
		if flags := modeFlags(parser.Model, mode); len(flags) > 0 {
			rep.helpf("taker %s flags: %s", mode, strings.Join(flags, " "))
		}
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
	// ran. It is recorded shell-quoted, so it replays (see commandLine).
	rep.argv = os.Args
	rep.scrubSecrets()
	// Parse succeeded, so the needles are already known — register them before
	// any failure path below can echo the command.
	var redactLits, redactRes []string
	switch mode {
	case "http":
		redactLits, redactRes = cli.HTTP.Redact, cli.HTTP.RedactRe
	case "exec":
		redactLits, redactRes = cli.Exec.Redact, cli.Exec.RedactRe
	case "term":
		redactLits, redactRes = cli.Term.Redact, cli.Term.RedactRe
	default:
		redactLits, redactRes = cli.Snap.Redact, cli.Snap.RedactRe
	}
	rep.setSecrets(slices.Concat(redactLits, redactRes))
	// The pattern text is a secret already; its MATCHES are too — a token in
	// the input's file name leaks through the command, source.path and an
	// "open tok_…: no such file" error on any failure before the run* compile.
	// A pattern that does not compile is left for the run* function, which
	// reports it as the usage error it is.
	if res, err := compileRegex("--redact-re", redactRes); err == nil {
		rep.secretRes = res
		rep.scrubSecrets()
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

// parseErrorMode picks the mode for a command line kong refused. It is the
// first positional token, the way kong itself reads it: a command name there
// selects that command, anything else is snap's file. Flags — and the value
// after a flag that takes one — are skipped, so `taker --json http --bogus`
// is http, and `taker app.py -t http --bogus` is still snap.
func parseErrorMode(app *kong.Application, args []string) string {
	takesValue := map[string]bool{}
	commands := map[string]bool{}
	for _, n := range app.Children {
		commands[n.Name] = true
		for _, f := range n.Flags {
			if !f.IsBool() {
				takesValue["--"+f.Name] = true
				if f.Short != 0 {
					takesValue["-"+string(f.Short)] = true
				}
			}
		}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			// Everything after is positional; the next token decides.
			if i+1 < len(args) && commands[args[i+1]] {
				return args[i+1]
			}
			return "snap"
		case strings.HasPrefix(a, "-") && a != "-":
			if !strings.Contains(a, "=") && takesValue[a] {
				i++
			}
		case commands[a]:
			return a
		default:
			return "snap"
		}
	}
	return "snap"
}

// argvFormat finds the report format a refused command line asked for. kong
// reads --json=true and --json=false too, so this does: "--json=true" used to
// be missed and the error came back as TOON.
func argvFormat(args []string) string {
	format := ""
	for _, a := range args {
		if a == "--" {
			break
		}
		name, val, hasEq := strings.Cut(a, "=")
		if name != "--json" && name != "--toon" {
			continue
		}
		on := true
		if hasEq {
			b, err := strconv.ParseBool(val)
			on = err == nil && b
		}
		switch {
		case on:
			format = strings.TrimPrefix(name, "--")
		case format == strings.TrimPrefix(name, "--"):
			format = "" // a later --json=false turns an earlier --json off
		}
	}
	return format
}

// modeFlags lists the long flags a mode accepts, off kong's model, for the
// inline hint on a parse error.
func modeFlags(app *kong.Application, mode string) []string {
	for _, n := range app.Children {
		if n.Name != mode {
			continue
		}
		var out []string
		for _, f := range n.Flags {
			if !f.Hidden {
				out = append(out, "--"+f.Name)
			}
		}
		return out
	}
	return nil
}

// newParser builds the command graph — factored out so a test can catch a
// malformed struct tag (kong.Must panics) instead of the shipped binary.
func newParser() *kong.Kong {
	return kong.Must(&cli,
		kong.Name("taker"),
		kong.Description("Generate professional code, HTTP and terminal evidence screenshots for security audits."),
		// --help still ends the process through this hook; parse errors come
		// back from Parse so they can be reported like every other failure.
		kong.Exit(func(code int) { os.Exit(code) }),
	)
}

func runSnap(cmd *SnapCmd, rep *reporter) int {
	rep.setSecretsAll(slices.Concat(cmd.Redact, cmd.RedactRe), cmd.Redact)
	if code := validateRedact(rep, os.Args[1:], cmd.Redact, cmd.RedactRe, cmd.RedactKeep); code != 0 {
		return code
	}
	redactRe, code := compileRedactRe(rep, cmd.RedactRe)
	if code != 0 {
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

	theme, err := resolveTheme(cmd.Theme, "source code")
	if err != nil {
		themeHelp(rep)
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
	case cmd.File != "":
		src, err = os.ReadFile(cmd.File)
		origin = sourceInfo{Kind: "file", Path: cmd.File}
	default:
		// Not /dev/stdin: that path does not exist on Windows. An agent that
		// forgot the file hands us an open pipe nobody will write to — probe
		// instead of blocking forever on a read that never comes.
		r := bufio.NewReader(os.Stdin)
		switch probeStdin(r, 300*time.Millisecond) {
		case stdinEOF:
			rep.helpf("taker snap <file> -o out.png — or pipe the source on stdin")
			return rep.fail(exitInput, kindInput, "stdin is empty")
		case stdinStalled:
			rep.helpf("taker snap <file> -o out.png — or pipe the source on stdin")
			return rep.fail(exitUsage, kindUsage, "no input: stdin produced no data within 300ms")
		}
		src, err = io.ReadAll(r)
	}
	if errors.Is(err, errGitUsage) {
		rep.helpf("taker snap --git HEAD:path/to/file.py -o out.png")
		return rep.fail(exitUsage, kindUsage, "%v", err)
	}
	if err != nil {
		return rep.fail(exitInput, kindInput, "%v", err)
	}
	// Counted on the raw bytes: the normaliser re-encodes them as U+FFFD,
	// after which nothing can tell them from a real replacement character.
	inputName := "stdin"
	if origin.Path != "" {
		inputName = origin.Path
	}
	warnInvalidUTF8(rep, inputName, src, max(cmd.FirstLine, 1)-1)
	// Every source gets the same treatment, piped bytes included: a needle
	// spliced by an ANSI escape would otherwise stay legible while "matched
	// nothing" warned, and the hash would cover bytes the image does not show.
	src = normalizeInput(src, rep)
	// http and term refuse an empty input; a blank editor pane at exit 0 is
	// the same silent success the "no lines matched" guard exists to stop.
	// Checked after normalizing: a file of only BOMs or ANSI codes draws
	// nothing too.
	if len(bytes.TrimSpace(src)) == 0 {
		return rep.fail(exitInput, kindInput, "%s is empty", inputName)
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

	lexName := lexFilename(cmd.File, origin)
	// Tokenize treats an unknown --lang as absent, so a typo ("pyhton")
	// silently became whatever the name or content suggested — often plain
	// text — and the image looked like the tool could not highlight it.
	if cmd.Lang != "" && !highlight.KnownLang(cmd.Lang) {
		used := highlight.LexerName(string(src), cmd.Lang, lexName)
		if used == "fallback" { // chroma's name for its no-lexer lexer
			used = "plain text"
		}
		rep.warn("--lang %q is not a language taker knows; highlighted as %s instead", cmd.Lang, used)
		rep.helpf("use a lexer name or alias such as python, go, javascript, java, php")
	}
	all, err := highlight.Tokenize(string(src), cmd.Lang, lexName, theme.Chroma)
	if err != nil {
		return rep.fail(exitInput, kindInput, "tokenize: %v", err)
	}
	// Renumber before anything reads a line number: --lines, --highlight, -m and
	// the gutter all speak the file's numbering once this has run.
	all = highlight.ShiftLines(all, cmd.FirstLine)
	// Specs are clamped to the input's last line before they are expanded:
	// one entry per line, so "1-999999999" was a gigabyte or a refusal.
	maxLine := all[len(all)-1].LineNum

	hlLines, err := highlight.ParseHighlightSpecMax(cmd.Highlight, maxLine)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "parse highlight: %v", err)
	}

	// Merge before the guard: -m supplies marks just as -H does, and --context
	// centers on either.
	markRe, err := compileRegex("--mark-re", cmd.MarkRe)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "%v", err)
	}
	msgs := [][]highlight.LineTokens{all}
	marked, stats := highlight.FindMarks(msgs, cmd.Mark, cmd.MaxMarks)
	reMarked, reStats := highlight.FindRegexMarks(msgs, markRe, cmd.MaxMarks)
	stats = append(stats, reStats...)
	markBase := len(rep.rep.Marks)
	recordMarks(rep, stats)
	// Normalized: -H and -m hitting the same value ("-H 1:10-16 -m sk_live")
	// otherwise drew nested boxes whose inner border split one value in two.
	hlLines = hlLines.Merge(marked[0]).Merge(reMarked[0]).Normalize()

	// Redaction searches the untrimmed input like marks do: a secret has to be
	// hidden in the excerpt, not only in the lines the image ends up showing.
	redact, redactStats := highlight.FindMarks(msgs, cmd.Redact, 0)
	redactReM, redactReStats := highlight.FindRegexMarks(msgs, redactRe, 0)
	// Fragments are boxed but not reported: the analyst typed the literal,
	// not its parts, so a part that matched nothing is no caveat.
	fragM, _ := highlight.FindRegexMarks(msgs, rep.redactFragRes(cmd.RedactKeep), 0)
	redactMarks, consumed := highlight.KeepSpans(redact[0].Merge(redactReM[0]).Merge(fragM[0]), cmd.RedactKeep)
	matched := 0
	for _, st := range redactStats {
		matched += st.Matches
		// Characters, not bytes: keep counts columns, and "ñññññ" is 10 bytes
		// but 5 columns — a byte count passed it under keep 5, drawn in full.
		chars := utf8.RuneCountInString(st.Literal)
		switch {
		case st.Matches == 0:
			rep.warn("--redact %q matched nothing", st.Literal)
			if strings.ContainsAny(st.Literal, "\t\n\r") {
				rep.helpf("the needle holds a control char — it only matches if the file has it too")
			} else {
				rep.helpf("nothing was masked — check the value or use --redact-re '<pattern>'")
			}
		case cmd.RedactKeep > 0 && chars <= cmd.RedactKeep:
			return rep.fail(exitUsage, kindUsage,
				"--redact-keep %d leaves nothing hidden in --redact %q — lower it or drop it",
				cmd.RedactKeep, st.Literal)
		case cmd.RedactKeep > 0 && chars-cmd.RedactKeep < 4:
			rep.warn("--redact-keep %d hides only %d char(s) of --redact %q",
				cmd.RedactKeep, chars-cmd.RedactKeep, st.Literal)
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
	// known once spans exist. Any match keep consumed is a secret drawn in
	// full, and one exposed match among five looks exactly like five hidden.
	if consumed > 0 {
		return rep.fail(exitUsage, kindUsage, "%s", keepExposesMsg(cmd.RedactKeep, consumed))
	}
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

	lineSpec, err := resolveLineSpec("--lines", cmd.Lines, hlLines, cmd.Context, maxLine, rep)
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
	markShown(rep, markBase, stats, func(_ int, st highlight.MarkStats) int { return st.Shown(lines) })

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
	// What the image shows, measured off the view that was drawn rather than
	// assumed from the flags that asked for it. Set before the dry-run exit:
	// a preflight reports the same document, and the crop is what an agent
	// checks before it decides the image is worth writing.
	crop := cropOf(highlight.Extent(lines), total)
	rep.rep.Crop = &crop
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

func runHTTP(cmd *HTTPCmd, rep *reporter) int {
	rep.setSecretsAll(slices.Concat(cmd.Redact, cmd.RedactRe), cmd.Redact)
	if code := validateRedact(rep, os.Args[1:], cmd.Redact, cmd.RedactRe, cmd.RedactKeep); code != 0 {
		return code
	}
	redactRe, code := compileRedactRe(rep, cmd.RedactRe)
	if code != 0 {
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
	// Every input file is evidence the run must not destroy: -o naming any of
	// them would rename a PNG over the capture.
	for _, in := range []string{cmd.Request, cmd.Response, cmd.File} {
		if in != "" && samePath(cmd.Output, in) {
			return rep.fail(exitUsage, kindUsage, "-o must differ from the input file — it would overwrite the source")
		}
	}

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
		rawReq, rawRes := splitCapture(raw)
		warnHTTPHeadUTF8(rep, "request", rawReq)
		warnHTTPHeadUTF8(rep, "response", rawRes)
		raw = normalizeInput(raw, rep)
		reqSrc, resSrc = splitCapture(raw)
		srcPath = cmd.File
		// A "Save item" that is not HTTP at all renders as a confident
		// Request pane — the worst kind of silent success. Garbage in gets
		// an input error; a request with no response gets a warning.
		switch {
		case reqSrc != nil && !looksHTTPStart(reqSrc) && resSrc == nil:
			rep.helpf("-f expects a request and/or response capture (request line 'METHOD target HTTP/...' or ':method:', status line 'HTTP/...')")
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
				warnHTTPHeadUTF8(rep, "request", reqSrc)
				reqSrc = normalizeInput(reqSrc, rep)
			} else {
				return rep.fail(exitInput, kindInput, "read request: %v", err)
			}
		}
		if cmd.Response != "" {
			if resSrc, err = os.ReadFile(cmd.Response); err == nil {
				warnHTTPHeadUTF8(rep, "response", resSrc)
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
			return rep.fail(exitInput, kindInput, "%s does not look like an HTTP request (start line 'METHOD target HTTP/...' or an HTTP/2 :method pseudo header)", cmd.Request)
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

	// Each panel's specs clamp to its own message: they expand one entry per
	// line, and the renderer drops every line past the end anyway.
	reqMax, resMax := render.HTTPLineCount(string(reqSrc)), render.HTTPLineCount(string(resSrc))
	reqHL, err := highlight.ParseHighlightSpecMax(cmd.ReqHL, reqMax)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "parse --hr: %v", err)
	}
	resHL, err := highlight.ParseHighlightSpecMax(cmd.ResHL, resMax)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "parse --hs: %v", err)
	}

	markRe, err := compileRegex("--mark-re", cmd.MarkRe)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "%v", err)
	}

	// --mark's matches are found while rendering, so they count here too.
	if cmd.Context > 0 && len(reqHL) == 0 && len(resHL) == 0 && len(cmd.Mark) == 0 && len(cmd.MarkRe) == 0 {
		return rep.fail(exitUsage, kindUsage, "--context needs --hr, --hs, --mark or --mark-re to center on")
	}

	reqLines, err := parseLineSpec("--nr", cmd.ReqLines, reqMax)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "%v", err)
	}
	resLines, err := parseLineSpec("--ns", cmd.ResLines, resMax)
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
			chars := utf8.RuneCountInString(lit) // keep counts columns, not bytes
			switch {
			case !present:
			case chars <= cmd.RedactKeep:
				return rep.fail(exitUsage, kindUsage,
					"--redact-keep %d >= length %d of --redact %q — the whole value would stay visible",
					cmd.RedactKeep, chars, lit)
			case chars-cmd.RedactKeep < 4:
				rep.warn("--redact-keep %d hides only %d char(s) of --redact %q",
					cmd.RedactKeep, chars-cmd.RedactKeep, lit)
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
		Redact:        cmd.Redact,
		RedactFrag:    rep.redactFragRes(cmd.RedactKeep),
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
	// A keep that would draw a match in full is the caller's misuse, found
	// only once the renderer had the spans — a usage error, like the literal
	// check above, not a refused render.
	var keepErr *render.RedactKeepError
	if errors.As(err, &keepErr) {
		return rep.fail(exitUsage, kindUsage, "%v", err)
	}
	if err != nil {
		return rep.fail(exitRefused, kindRefused, "render: %v", err)
	}
	base := len(rep.rep.Marks)
	recordMarks(rep, facts.Marks)
	// The renderer trimmed the panels, so it counted what each one shows.
	markShown(rep, base, facts.Marks, func(i int, _ highlight.MarkStats) int {
		if i < len(facts.Shown) {
			return facts.Shown[i]
		}
		return 0
	})
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

// h2RequestRe is the first line of an HTTP/2 request in pseudo-header form
// (what browsers' DevTools and HTTP/2 proxies copy): it has no request line
// and opens straight into :method, :path, :authority or :scheme. The
// renderer draws that form; refusing it here was the only thing in the way.
var h2RequestRe = regexp.MustCompile(`(?i)^:(method|authority|scheme|path):`)

func looksHTTPStart(src []byte) bool {
	return looksHTTPResponse(src) || looksHTTPRequest(src)
}

func looksHTTPResponse(src []byte) bool {
	return strings.HasPrefix(startLine(src), "HTTP/")
}

func looksHTTPRequest(src []byte) bool {
	first := startLine(src)
	return httpStartRe.MatchString(first) || h2RequestRe.MatchString(first)
}

func startLine(src []byte) string {
	first := src
	if i := bytes.IndexByte(src, '\n'); i >= 0 {
		first = src[:i]
	}
	return strings.TrimSpace(string(first))
}

// compileRedactRe compiles --redact-re before anything else in a run can fail
// and hands the patterns to the scrubber. Their matches are secrets the
// literal list never named — a token caught in a file name, the source path
// or an error message stays hidden only if the pattern itself does the
// masking, and a failure that came before the compile used to skip it.
func compileRedactRe(rep *reporter, patterns []string) ([]*regexp.Regexp, int) {
	res, err := compileRegex("--redact-re", patterns)
	if err != nil {
		return nil, rep.fail(exitUsage, kindUsage, "%v", err)
	}
	rep.secretRes = res
	rep.scrubSecrets()
	return res, 0
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
// without its closer means kong's comma-split cut it in half. Inside a
// character class a bracket is a member, not an opener: `=[^)]+` is a whole
// pattern, and counting its ')' glued the next --redact-re onto it, so the
// fused pattern matched neither secret and both rendered in clear. A class
// left open at the end ("[a" from "[a,b]") is the split it looks like.
func unbalancedRE(p string) bool {
	depth := 0
	inClass := false
	for i := 0; i < len(p); i++ {
		// Every metacharacter is ASCII, and no byte of a multi-byte UTF-8
		// rune is, so walking bytes cannot mistake a rune for syntax.
		c := p[i]
		if c == '\\' {
			i++ // the escaped byte is a literal
			continue
		}
		if inClass {
			switch {
			case c == '[' && i+1 < len(p) && p[i+1] == ':':
				// A POSIX class "[:alpha:]" nests inside the brackets.
				if j := strings.Index(p[i+2:], ":]"); j >= 0 {
					i += j + 3
				}
			case c == ']':
				inClass = false
			}
			continue
		}
		switch c {
		case '[':
			inClass = true
			// "[]a]" and "[^]a]": a ']' first in the class is a member.
			if i+1 < len(p) && p[i+1] == '^' {
				i++
			}
			if i+1 < len(p) && p[i+1] == ']' {
				i++
			}
		case '(', '{':
			depth++
		case ')', '}':
			depth--
		}
	}
	return depth != 0 || inClass
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
	stats := append(s1, s2...)
	base := len(rep.rep.Marks)
	recordMarks(rep, stats)
	// A transcript renders every line, so this equals kept today; counted off
	// the view anyway, so a future trim cannot make the report overclaim.
	markShown(rep, base, stats, func(_ int, st highlight.MarkStats) int { return st.Shown(lines) })
	// Two needles on one value draw one box, not nested ones.
	return m1[0].Merge(m2[0]).Normalize()
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
		rep.warn("removed %d invisible/format characters (BOM, ZWSP, combining marks with no precomposed form) so the needle stays contiguous", m)
	}
	return out
}

// warnInvalidUTF8 says when bytes of the input are not UTF-8. Each one is
// drawn as U+FFFD, which has a glyph, so nothing in the image shows that a
// value was lost — and a needle holding those bytes can never match. offset
// shifts the line into the numbering the analyst reads (--first-line, the
// prompt line of a transcript).
func warnInvalidUTF8(rep *reporter, name string, raw []byte, offset int) {
	n, line := highlight.InvalidUTF8(string(raw))
	if n == 0 {
		return
	}
	rep.warn("%d invalid UTF-8 byte(s) in %s (first on line %d) render as U+FFFD; the image cannot show their values",
		n, name, line+offset)
}

// warnHTTPHeadUTF8 is warnInvalidUTF8 for the start line and headers of one
// HTTP message, counted on the raw bytes. The body is left out: the renderer
// already says when a body is binary or compressed, and a second warning
// about the same bytes would only repeat it.
func warnHTTPHeadUTF8(rep *reporter, side string, raw []byte) {
	head := raw
	if i := bytes.Index(raw, []byte("\n\n")); i >= 0 {
		head = raw[:i]
	}
	if i := bytes.Index(head, []byte("\n\r\n")); i >= 0 {
		head = head[:i]
	}
	warnInvalidUTF8(rep, "the "+side+"'s start line or headers", head, 0)
}

// normalizeZeroWidth DELETES codepoints that carry no standalone glyph —
// format chars (Cf: BOM, ZWSP, ZWJ, bidi controls), combining marks (Mn/Me)
// and soft hyphen. Replacing them would still leave a rune the needle does
// not contain; deleting them makes the spliced needle contiguous again, and
// because matcher and renderer share the normalized text, columns stay
// aligned and the box lands on the needle.
//
// The text is composed to NFC first. A decomposed accent ("Jose" + U+0301)
// is a combining mark too, and deleting it rendered "José" as "Jose" — a
// changed name in the evidence. Composed, it is one rune and one cell, so
// only the marks with no precomposed form are left to delete.
func normalizeZeroWidth(b []byte) ([]byte, int) {
	composed := b
	if !norm.NFC.IsNormal(b) {
		composed = norm.NFC.Bytes(b)
	}
	var sb strings.Builder
	sb.Grow(len(composed))
	n := 0
	for _, r := range string(composed) {
		if r == '\uFEFF' || r == '\u00AD' || unicode.Is(unicode.Cf, r) ||
			unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) {
			n++
			continue
		}
		sb.WriteRune(r)
	}
	if n == 0 {
		return composed, 0
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

// argvRedactRe collects the --redact-re values off the raw argv, for the
// parse-error path, split on unescaped commas the way kong splits a parsed
// flag list — compileRegex then rejoins a pattern whose commas kong would
// have cut, exactly as it does on the parsed path.
func argvRedactRe(args []string) []string {
	var out []string
	for i, a := range args {
		if a == "--" {
			break
		}
		var val string
		switch {
		case strings.HasPrefix(a, "--redact-re="):
			val = strings.TrimPrefix(a, "--redact-re=")
		case a == "--redact-re" && i+1 < len(args):
			val = args[i+1]
		default:
			continue
		}
		for _, p := range kong.SplitEscaped(val, ',') {
			if p != "" {
				out = append(out, p)
			}
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
//
// argv is taker's own part of the command line: the scan stops at "--",
// after which every token is a file name or the executed command's, and
// `taker exec -- ls -m` is not an empty -m.
func validateRedact(rep *reporter, argv, literals, patterns []string, keep int) int {
	// Kong drops an empty value from a string slice — "--redact ''" never
	// reaches `literals`. The raw argv is the only faithful source, and an
	// empty needle is the quietest possible footgun: a clean image, no
	// warning, every secret visible.
	if i := slices.Index(argv, "--"); i >= 0 {
		argv = argv[:i]
	}
	for i, a := range argv {
		if a == "--redact" || a == "--redact-re" || a == "--mark" || a == "--mark-re" || a == "-m" {
			if i+1 >= len(argv) || argv[i+1] == "" {
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
		if n := utf8.RuneCountInString(s); n <= 2 {
			rep.warn("--redact %q is %d chars — it masks far more than a secret (quote a literal comma as '\\,')", s, n)
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

// keepExposesMsg is the refusal for a --redact-keep that would draw whole
// matches in clear. The analyst asked for those values to be hidden, so the
// run stops instead of shipping an image that hides most of them.
func keepExposesMsg(keep, exposed int) string {
	return (&render.RedactKeepError{Keep: keep, Exposed: exposed}).Error()
}

// termRedact finds the literals to hide and warns when one matched nothing —
// a redact that hides nothing looks exactly like a clean image. frags are the
// literals' whole-token fragments: boxed, never warned about, because the
// analyst did not type them. A nonzero second return is the exit code of a
// fatal misuse (e.g. keep exposing the whole secret); the caller returns it
// verbatim.
func termRedact(lines []highlight.LineTokens, needles []string, res, frags []*regexp.Regexp, keep int, rep *reporter) (highlight.Marks, int) {
	found, stats := highlight.FindMarks([][]highlight.LineTokens{lines}, needles, 0)
	foundRe, reStats := highlight.FindRegexMarks([][]highlight.LineTokens{lines}, res, 0)
	foundFrag, _ := highlight.FindRegexMarks([][]highlight.LineTokens{lines}, frags, 0)
	stats = append(stats, reStats...)
	kept, consumed := highlight.KeepSpans(found[0].Merge(foundRe[0]).Merge(foundFrag[0]), keep)
	matched := 0
	for i, st := range stats {
		matched += st.Matches
		literal := i < len(stats)-len(reStats)
		chars := utf8.RuneCountInString(st.Literal) // keep counts columns, not bytes
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
		case literal && keep > 0 && chars <= keep:
			return nil, rep.fail(exitUsage, kindUsage,
				"--redact-keep %d leaves nothing hidden in --redact %q — lower it or drop it",
				keep, st.Literal)
		case literal && keep > 0 && chars-keep < 4:
			rep.warn("--redact-keep %d hides only %d char(s) of --redact %q",
				keep, chars-keep, st.Literal)
		}
	}
	// A regex match shorter than keep passes every per-literal check and is
	// drawn in full; any consumed span is an exposed secret.
	if consumed > 0 {
		return nil, rep.fail(exitUsage, kindUsage, "%s", keepExposesMsg(keep, consumed))
	}
	if keep > 0 && matched > 0 && kept == nil {
		return nil, rep.fail(exitUsage, kindUsage,
			"--redact-keep %d leaves nothing hidden — lower it or drop it", keep)
	}
	return kept, 0
}

// terminalTabWidth is where a terminal puts its tab stops. The output of ls,
// ps or a tab-separated table was aligned by the terminal that printed it,
// on stops 8 columns apart; drawn on snap's 4 the columns no longer line up
// with what the analyst saw.
const terminalTabWidth = 8

// tokenizeTranscript is the one tokenizer exec and term share, so the two
// renders of the same session cannot drift apart.
func tokenizeTranscript(src, filename string, theme *render.Theme) ([]highlight.LineTokens, error) {
	return highlight.TokenizeTabs(src, "text", filename, theme.Chroma, terminalTabWidth)
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
	trimHelp    string // the next command when the transcript is too tall to render
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
		if t.trimHelp != "" {
			rep.helpf("%s", t.trimHelp)
		}
		return rep.fail(exitRefused, kindRefused, "render: %v", err)
	}
	rep.rep.Redactions = rcfg.Redacted
	// Before the dry-run exit, so a preflight carries the same crop.
	total := len(t.lines)
	rep.rep.Crop = &Crop{First: 1, Last: total, Shown: total, Total: &total}
	if t.dryRun {
		return rep.doneDry(img)
	}
	if err := writePNG(img, t.output, rep); err != nil {
		if errors.Is(err, errOutUsage) {
			return rep.fail(exitUsage, kindUsage, "%v", err)
		}
		return rep.fail(exitFail, kindOutput, "%v", err)
	}
	return rep.done(t.output)
}

// execFlagNames is every flag `taker exec` defines — used to catch the trap
// where one of them lands inside the command's argv instead of before --.
// The value form `--flag=v` matches by prefix. It is read off kong's model,
// not kept by hand: a hand list once named -n, --lines, -v and --version,
// which exec does not have, and refused `head -n 2` and `grep -v` as taker
// flags. The global -h and -v stay out on purpose — inside a command they
// are the command's (grep -h, grep -v), and neither one misplaces evidence.
var execFlagNames = sync.OnceValue(func() map[string]bool {
	names := map[string]bool{}
	// A throwaway struct, so building the model never touches the parsed cli.
	var probe struct {
		Exec ExecCmd `cmd:"exec"`
	}
	for _, n := range kong.Must(&probe).Model.Children {
		for _, f := range n.Flags {
			names["--"+f.Name] = true
			for _, a := range f.Aliases {
				names["--"+a] = true
			}
			if f.Short != 0 {
				names["-"+string(f.Short)] = true
			}
		}
	}
	return names
})

// splitNeedle names a redact needle that the prompt line's quoting breaks
// apart: one the raw words hold but the quoted line does not. The image finds
// needles in the drawn text, so a split needle would be drawn in clear —
// it's$ecret quoted as "it's\$ecret". The quoting rule keeps this to a
// needle that holds both a ' and one of " $ ` \ !.
func splitNeedle(raw, quoted string, lits []string, res, frags []*regexp.Regexp) string {
	for i, l := range lits {
		if l != "" && strings.Contains(raw, l) && !strings.Contains(quoted, l) {
			return fmt.Sprintf("--redact #%d", i+1)
		}
	}
	for i, re := range res {
		for _, m := range re.FindAllString(raw, -1) {
			if m != "" && !strings.Contains(quoted, m) {
				return fmt.Sprintf("--redact-re #%d", i+1)
			}
		}
	}
	for _, re := range frags {
		for _, m := range re.FindAllString(raw, -1) {
			if !strings.Contains(quoted, m) {
				return "a part of a --redact value"
			}
		}
	}
	return ""
}

// stdinState is what a probe of stdin found.
type stdinState int

const (
	stdinData    stdinState = iota // at least one byte is readable
	stdinEOF                       // the producer closed the pipe without writing
	stdinStalled                   // nothing within the grace period, or unreadable
)

// probeStdin reports whether at least one byte is readable within the grace
// period. A real producer — cat file | taker — has data in the pipe buffer
// before the process even starts; a harness that opened stdin and never
// writes stalls forever, which is the failure this exists to catch. An
// immediate EOF is neither: the input was empty, not slow, and saying "no
// data within 300ms" sent the caller looking for the wrong problem. Peek does
// not consume, so a fast probe leaves the stream intact.
func probeStdin(r *bufio.Reader, grace time.Duration) stdinState {
	ch := make(chan stdinState, 1)
	go func() {
		_, err := r.Peek(1)
		switch {
		case err == nil:
			ch <- stdinData
		case errors.Is(err, io.EOF):
			ch <- stdinEOF
		default:
			ch <- stdinStalled
		}
	}()
	select {
	case st := <-ch:
		return st
	case <-time.After(grace):
		return stdinStalled
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
		if execFlagNames()[name] {
			return a
		}
	}
	return ""
}

// Limits on what exec reads from the command. Nothing past about 650 rows
// renders under the 32000px cap, so these sit far above any transcript that
// can render — ANSI colour and multi-byte text included — and far below the
// gigabytes an unbounded `yes` reached before the height check refused it.
const (
	execMaxBytes = 4 << 20
	execMaxLines = 10000
)

// execPipeGrace is how long exec keeps reading after the command exits. A
// background process it started can hold the output pipe open for good;
// waiting for it kept taker until the deadline and then labelled a command
// that had exited 0 as killed.
const execPipeGrace = 2 * time.Second

// childRun is how one exec run ended.
type childRun struct {
	out      []byte
	startErr error
	state    *os.ProcessState
	signal   string // the signal that ended the command, "" when it exited
	timedOut bool   // the deadline killed the command itself
	held     bool   // something still held the output past the grace; it was killed
	overflow string // the limit the output passed, "" when it fit
}

// outputCap collects the command's output up to the exec limits. It is
// sealed once the run is over, so a writer that outlives the run — a
// detached process on a platform where closing the pipe does not stop the
// read — cannot change the bytes after they were taken.
type outputCap struct {
	mu     sync.Mutex
	buf    []byte
	lines  int
	over   string
	sealed bool
}

func (c *outputCap) add(p []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sealed {
		return false
	}
	c.lines += bytes.Count(p, []byte{'\n'})
	switch {
	case len(c.buf)+len(p) > execMaxBytes:
		c.over = fmt.Sprintf("%d MiB", execMaxBytes>>20)
	case c.lines > execMaxLines:
		c.over = fmt.Sprintf("%d lines", execMaxLines)
	default:
		c.buf = append(c.buf, p...)
		return true
	}
	return false
}

// limit is the limit the output passed, "" while it fits.
func (c *outputCap) limit() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.over
}

func (c *outputCap) seal() ([]byte, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sealed = true
	return c.buf, c.over
}

// runChild runs argv with stdout and stderr on one pipe that exec reads
// itself, so it knows which of three things ended the run: the command
// exiting, the deadline, or the output cap. The whole process group is
// killed on the deadline and on the cap: a child that forks a sleeper
// leaves the pipe open, and a lone kill would leave us reading it until
// the grandchild exits on its own.
func runChild(argv []string, timeout, grace time.Duration) childRun {
	var run childRun
	child := newChild(argv)
	pr, pw, err := os.Pipe()
	if err != nil {
		run.startErr = err
		return run
	}
	defer pr.Close()
	child.Stdout, child.Stderr = pw, pw
	err = child.Start()
	pw.Close() // the child holds its own copy; ours would keep the pipe open
	if err != nil {
		run.startErr = err
		return run
	}

	capture := &outputCap{}
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		b := make([]byte, 32<<10)
		for {
			n, err := pr.Read(b)
			if n > 0 && !capture.add(b[:n]) {
				return
			}
			if err != nil {
				return
			}
		}
	}()
	waitc := make(chan struct{})
	go func() {
		child.Wait()
		close(waitc)
	}()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	// Phase one: until the command itself is gone. The reader can finish
	// first — the command closed its output, or passed the cap.
	exited := false
	for !exited {
		select {
		case <-waitc:
			exited = true
		case <-readDone:
			if over := capture.limit(); over != "" {
				killGroup(child)
				<-waitc
				run.overflow = over
				return run
			}
			readDone = nil // EOF: the command is still running, keep waiting
		case <-deadline.C:
			exited = true
			// The command can exit on its own in the same instant; only
			// a command our kill ended is a timeout. Asked of the wait,
			// not of the status: a Windows status always says "exited",
			// a killed process included.
			select {
			case <-waitc:
				continue
			default:
			}
			killed := killGroup(child)
			<-waitc
			run.timedOut = killedByDeadline(child.ProcessState, killed)
		}
	}
	run.state = child.ProcessState
	run.signal = signalName(run.state)

	// Phase two: the output the command wrote before it exited is still in
	// the pipe. Read it to the end, but not past the grace: a process that
	// holds the pipe after the command is gone is not part of its answer.
	if readDone != nil {
		select {
		case <-readDone:
		case <-time.After(grace):
			run.held = true
		}
	}
	run.out, run.overflow = capture.seal()
	// What is left of the group goes with the run. A process that left the
	// group (setsid) is out of reach; the pipe closes under it when we return.
	if run.held || run.overflow != "" {
		killGroup(child)
	}
	return run
}

// ownArgs is the part of argv that taker parsed itself. Kong hands exec
// everything from the first positional on, verbatim, as the command — so a
// tail of argv equal to it belongs to the command, and `taker exec id -o x -m`
// must reach the trap below, not fail as an empty -m.
func ownArgs(argv, passthrough []string) []string {
	if n := len(argv) - len(passthrough); n >= 0 && slices.Equal(argv[n:], passthrough) {
		return argv[:n]
	}
	return argv
}

// maxTimeout is the largest --timeout, in seconds, that time.Duration holds.
const maxTimeout = math.MaxInt64 / int64(time.Second)

func runExec(cmd *ExecCmd, rep *reporter) int {
	rep.setSecretsAll(slices.Concat(cmd.Redact, cmd.RedactRe), cmd.Redact)
	if code := validateRedact(rep, ownArgs(os.Args[1:], cmd.Args), cmd.Redact, cmd.RedactRe, cmd.RedactKeep); code != 0 {
		return code
	}
	redactRe, code := compileRedactRe(rep, cmd.RedactRe)
	if code != 0 {
		return code
	}
	for _, c := range [][2]any{
		{"--wrap-width", cmd.WrapWidth}, {"--max-marks", cmd.MaxMarks},
	} {
		if code := nonneg(rep, c[0].(string), c[1].(int)); code != 0 {
			return code
		}
	}
	// Zero used to kill the command before it printed anything and render
	// the empty frame as "killed after 0s" — an image of nothing that looks
	// like evidence of something.
	// A value past maxTimeout overflows time.Duration: the deadline went
	// negative, fired at once, and drew the same empty "killed after Ns".
	if cmd.Timeout < 1 || int64(cmd.Timeout) > maxTimeout {
		return rep.fail(exitUsage, kindUsage, "--timeout must be between 1 and %d seconds, got %d", maxTimeout, cmd.Timeout)
	}
	theme, err := resolveTheme(cmd.Theme, "a transcript")
	if err != nil {
		themeHelp(rep)
		return rep.fail(exitUsage, kindUsage, "%v", err)
	}
	if cmd.Chrome != "editor" && cmd.Chrome != "window" {
		return rep.fail(exitUsage, kindUsage, "--chrome must be 'editor' or 'window', got %q", cmd.Chrome)
	}
	cmd.WrapWidth = clampWrapWidth(cmd.WrapWidth, false, rep)

	var argv []string
	// cmdStr is the prompt line the image draws, needles still in place for
	// the black boxes; provStr is the same line masked, for every text
	// channel. Each word is masked before it is quoted (see quoteScrubbed).
	var cmdStr, provStr string
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
		} else if strings.HasPrefix(argv[0], "-") {
			// Kong takes an unknown flag for the start of the command, so
			// `taker exec --bogus` would run a program named "--bogus" and
			// blame PATH, and `taker exec -n 1 -- id` would blame -n for
			// sitting inside a command it was in front of. Name the cause.
			if argv[0] == "--list-themes" {
				rep.helpf("taker --list-themes")
			} else {
				rep.helpf("taker exec --help")
			}
			if slices.Contains(argv, "--") {
				rep.helpf("only taker exec's own flags go before --; the command and its flags go after it")
			}
			return rep.fail(exitUsage, kindUsage, "unknown flag %q for taker exec", argv[0])
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
		// The prompt line is the record of what ran, so it is quoted the way
		// a shell would need it: words joined with bare spaces showed
		// `grep -c func main a.go` for a search of "func main", a different
		// command than the one that ran.
		cmdStr = shellQuote(argv)
		if what := splitNeedle(strings.Join(argv, " "), cmdStr, cmd.Redact, redactRe, rep.redactFragRes(cmd.RedactKeep)); what != "" {
			rep.helpf("taker exec --sh '<command line>' shows the line as typed")
			return rep.fail(exitUsage, kindUsage,
				"quoting the prompt line splits %s, so the image could not hide it", what)
		}
		provStr = rep.quoteScrubbed(argv)
	}
	if provStr == "" {
		provStr = rep.scrubMeta(cmdStr)
	}
	rep.note("$ %s", provStr)

	run := runChild(argv, time.Duration(cmd.Timeout)*time.Second, execPipeGrace)
	if run.startErr != nil {
		rep.helpf("check that the command exists on PATH and its argv is valid")
		return rep.fail(exitInput, kindInput, "run %q: %v", provStr, run.startErr)
	}
	// Past the cap nothing can render, and a cut transcript would be less
	// faithful than none: refuse, and name the way to keep what matters.
	if run.overflow != "" {
		rep.helpf("taker exec --sh %s", quoteWord(provStr+" | head -n 200", true))
		return rep.fail(exitRefused, kindRefused,
			"the output passed %s — it cannot render under 32000px; the command was killed", run.overflow)
	}

	// The exit status is part of the evidence: a failed exploit looks different
	// from a successful one, and a killed long-runner is not an empty result.
	// It comes from how the wait ended, not from the clock: a command that
	// exited on its own keeps its own status even when the deadline passed
	// while something it left behind still held the output.
	var status string
	rep.rep.Exec = &ExecReport{Signal: run.signal}
	switch {
	case run.timedOut:
		status = fmt.Sprintf("killed after %ds", cmd.Timeout)
		rep.rep.Exec.TimedOut = true
	case run.signal != "":
		status = "killed by " + run.signal
	default:
		code := run.state.ExitCode()
		status = fmt.Sprintf("exit %d", code)
		rep.rep.Exec.Exit = &code
	}
	if run.held {
		// The image must not pass for the whole output of everything the
		// command started: say so in the footnote, not only on stderr.
		status += fmt.Sprintf(" · output cut %s later", execPipeGrace)
		rep.warn("a background process still held the output %s after the command ended; "+
			"taker killed what was left of the command's process group and closed the pipe, "+
			"so nothing printed later is in the image", execPipeGrace)
	}
	out := run.out

	transcript := "$ " + cmdStr + "\n" + string(out)
	warnInvalidUTF8(rep, "the transcript", []byte(transcript), 0)
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
	lines, err := tokenizeTranscript(string(stripped), "", theme)
	if err != nil {
		return rep.fail(exitRefused, kindRefused, "tokenize transcript: %v", err)
	}
	recolorPrompt(lines)

	markRe, err := compileRegex("--mark-re", cmd.MarkRe)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "%v", err)
	}

	// The hash covers the text that was matched and drawn, as snap and http
	// do: the raw transcript still holds an ANSI-spliced secret in clear, and
	// its sum would confirm guesses at it.
	rep.addInput("transcript", stripped)
	rep.metaKV = append(rep.metaKV, [2]string{"taker:exec", provStr})
	rep.rep.Source = &Source{Kind: "exec", Path: provStr}
	rep.scrubSecrets()

	redactM, code := termRedact(lines, cmd.Redact, redactRe, rep.redactFragRes(cmd.RedactKeep), cmd.RedactKeep, rep)
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
		trimHelp:    "taker exec --sh " + quoteWord(provStr+" | head -n 200", true),
	}, rep)
}

func runTerm(cmd *TermCmd, rep *reporter) int {
	rep.setSecretsAll(slices.Concat(cmd.Redact, cmd.RedactRe), cmd.Redact)
	if code := validateRedact(rep, os.Args[1:], cmd.Redact, cmd.RedactRe, cmd.RedactKeep); code != 0 {
		return code
	}
	redactRe, code := compileRedactRe(rep, cmd.RedactRe)
	if code != 0 {
		return code
	}
	for _, c := range [][2]any{
		{"--wrap-width", cmd.WrapWidth}, {"--max-marks", cmd.MaxMarks},
	} {
		if code := nonneg(rep, c[0].(string), c[1].(int)); code != 0 {
			return code
		}
	}
	theme, err := resolveTheme(cmd.Theme, "a transcript")
	if err != nil {
		themeHelp(rep)
		return rep.fail(exitUsage, kindUsage, "%v", err)
	}
	if cmd.Chrome != "editor" && cmd.Chrome != "window" {
		return rep.fail(exitUsage, kindUsage, "--chrome must be 'editor' or 'window', got %q", cmd.Chrome)
	}
	cmd.WrapWidth = clampWrapWidth(cmd.WrapWidth, false, rep)
	if samePath(cmd.Output, cmd.File) {
		return rep.fail(exitUsage, kindUsage, "-o must differ from the input file — it would overwrite the source")
	}

	src, err := os.ReadFile(cmd.File)
	if err != nil {
		return rep.fail(exitInput, kindInput, "%v", err)
	}
	if len(bytes.TrimSpace(src)) == 0 {
		return rep.fail(exitInput, kindInput, "%s is empty", cmd.File)
	}
	warnInvalidUTF8(rep, cmd.File, src, 0)
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
	lines, err := tokenizeTranscript(string(stripped), cmd.File, theme)
	if err != nil {
		return rep.fail(exitRefused, kindRefused, "tokenize transcript: %v", err)
	}

	markRe, err := compileRegex("--mark-re", cmd.MarkRe)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "%v", err)
	}

	// Normalized, then masked — the same rule as every other mode. The raw
	// file's sum would equal `sha256sum <file>` and verify guesses at a secret
	// an escape sequence splices.
	rep.addInput("transcript", stripped)
	rep.rep.Source = &Source{Kind: "transcript", Path: cmd.File}
	rep.scrubSecrets()

	redactM, code := termRedact(lines, cmd.Redact, redactRe, rep.redactFragRes(cmd.RedactKeep), cmd.RedactKeep, rep)
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
		trimHelp:    "head -n 200 " + quoteWord(cmd.File, false) + " > short.txt, then taker term short.txt",
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
//
// maxLine is the input's last line number: windows and ranges are clamped to
// it before they are expanded line by line.
func resolveLineSpec(flag, spec string, hl highlight.Marks, context, maxLine int, rep *reporter) ([]int, error) {
	if s := highlight.ContextSpecMax(hl, context, maxLine); s != nil {
		if spec != "" {
			rep.warn("--context overrides %s; ignoring it", flag)
		}
		return s, nil
	}
	return parseLineSpec(flag, spec, maxLine)
}

// parseLineSpec returns an error instead of exiting, so the caller can report it
// with the flag named and the right exit code.
func parseLineSpec(flag, spec string, maxLine int) ([]int, error) {
	parsed, err := highlight.ParseLineSpecMax(spec, maxLine)
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

// lexFilename is the name the lexer picks a language from. With --git there
// is no file argument — the path lives in the origin — and an empty name left
// short code to content analysis, which rendered it flat.
func lexFilename(file string, origin sourceInfo) string {
	if origin.Kind == "git" {
		return origin.Path
	}
	return file
}

// errGitUsage marks a --git value that is malformed — the caller's mistake,
// exit 2 — as opposed to a well-formed spec git cannot resolve (exit 3).
var errGitUsage = errors.New("--git takes <rev>:<path>")

// readGitBlobIn reads <rev>:<path> out of the repository in dir ("" for the
// working directory; a test points it at a throwaway repo).
//
// It exists to delete a class of error rather than to detect it: evidence has to
// show the code of the revision being reported, and a snippet copied out of an
// editor is a transcription that nothing can check. Rendering from the object
// store means there is nothing to transcribe.
func readGitBlobIn(dir, spec string) ([]byte, sourceInfo, error) {
	rev, path, ok := strings.Cut(spec, ":")
	if !ok || rev == "" || path == "" {
		return nil, sourceInfo{}, fmt.Errorf("%w, for example HEAD:src/auth/login.py", errGitUsage)
	}
	// The spec travels inside one argument ("rev:path"), so a revision that
	// starts with '-' is handed to git as a flag — and git has flags like
	// --output that write files.
	if strings.HasPrefix(rev, "-") {
		return nil, sourceInfo{}, fmt.Errorf("%w; the revision in %q must not start with '-'", errGitUsage, spec)
	}
	sha, err := gitOut(dir, "rev-parse", rev+"^{commit}")
	if err != nil {
		return nil, sourceInfo{}, fmt.Errorf("--git %s: %w", spec, err)
	}
	// Only a blob is a file. `git show` prints a directory's tree listing
	// (and a submodule's commit) as if it were content, which rendered a file
	// that does not exist, named after the directory, at exit 0.
	typ, err := gitOut(dir, "cat-file", "-t", rev+":"+path)
	if err != nil {
		return nil, sourceInfo{}, fmt.Errorf("--git %s: %w", spec, err)
	}
	switch typ = strings.TrimSpace(typ); typ {
	case "blob":
	case "tree":
		return nil, sourceInfo{}, fmt.Errorf("--git %s is a tree (directory), not a file", spec)
	default:
		return nil, sourceInfo{}, fmt.Errorf("--git %s is a %s, not a file", spec, typ)
	}
	// cat-file returns the committed bytes: no textconv, no filters — the
	// revision exactly as the hash names it.
	out, err := gitOut(dir, "cat-file", "blob", rev+":"+path)
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

// resolveTheme looks --theme up for a mode that is not http; content names
// what that mode draws ("source code", "a transcript"), so the refusal says
// what would actually come out flat.
//
// The error stays one plain sentence: the next command goes through
// themeHelp into help[], where a script reads it, and error.message no longer
// carries terminal layout.
func resolveTheme(name, content string) (*render.Theme, error) {
	theme, ok := render.Themes[strings.ToLower(name)]
	if !ok {
		return nil, fmt.Errorf("unknown theme %q", name)
	}
	// Its style only covers the HTTP lexer's token types, so source code comes
	// out flat — every keyword falling back to the plain text color.
	if theme.HTTPOnly {
		return nil, fmt.Errorf("theme %q is only for `taker http` (%s renders without highlighting)", name, content)
	}
	return theme, nil
}

// themeHelp is the next step after a resolveTheme refusal.
func themeHelp(rep *reporter) {
	rep.helpf("run 'taker --list-themes' to see options; vscode-dark is the default")
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
	prev, statErr := os.Stat(path)
	if statErr == nil {
		rep.warn("overwriting existing %s", path)
	}
	dir := filepath.Dir(path)
	tmp, err := createTemp(dir)
	if err != nil {
		// A missing directory is a typo in -o, the caller's to fix — the
		// same class as "-o is a directory".
		if _, derr := os.Stat(dir); errors.Is(derr, fs.ErrNotExist) {
			return fmt.Errorf("%w: directory %s does not exist", errOutUsage, dir)
		}
		// The temp name is an implementation detail — report the target.
		var pe *fs.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return fmt.Errorf("create output: %s: %v", path, err)
	}
	tmpName := tmp.Name()
	// Overwriting keeps the mode the evidence file already had, rather than
	// whatever the new one was created with.
	if statErr == nil {
		if err := tmp.Chmod(prev.Mode().Perm()); err != nil {
			tmp.Close()
			os.Remove(tmpName)
			return fmt.Errorf("create output: %s: %v", path, err)
		}
	}

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
	meta = append(meta, rep.pngMeta()...)
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

// createTemp opens a new hidden file in dir for writePNG to rename into
// place. It is not os.CreateTemp: that creates 0600, and the rename carried
// the mode onto the evidence, so a PNG meant to be shared could not be read
// by anyone else. Created 0644 here, the umask still applies — the file gets
// what any other new file in that directory would.
func createTemp(dir string) (*os.File, error) {
	for i := 0; ; i++ {
		name := filepath.Join(dir, ".taker-"+strconv.FormatUint(uint64(rand.Uint32()), 10)+".png")
		f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) && i < 100 {
			continue
		}
		return f, err
	}
}

// samePath reports whether two paths name the same file — `-o` set to the
// input file would destroy the evidence's source, and a warning does not undo
// a rename. When both exist the file identity decides, so a symlink or a
// second spelling of the input cannot slip past a string comparison; Abs is
// the fallback for an output that does not exist yet.
func samePath(a, b string) bool {
	if fa, err := os.Stat(a); err == nil {
		if fb, err := os.Stat(b); err == nil {
			return os.SameFile(fa, fb)
		}
	}
	aa, ea := filepath.Abs(a)
	bb, eb := filepath.Abs(b)
	return ea == nil && eb == nil && aa == bb
}
