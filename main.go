package main

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

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
	Git        string   `name:"git" placeholder:"<rev>:<path>" help:"Render a file as it is in a revision, e.g. HEAD:src/auth/login.py. Removes the copy of the code with no revision attached."`
	Highlight  string   `short:"H" help:"Regions to highlight: '15', '13,15', '10-20', or '15:20-45' for columns."`
	Mark       []string `short:"m" help:"Highlight every occurrence of this text. Repeatable."`
	MaxMarks   int      `name:"max-marks" default:"0" help:"Keep at most N matches per --mark literal, earliest first (default: all)."`
	Label      string   `short:"L" help:"Caption strip stating what the image proves."`
	Context    int      `short:"c" default:"0" help:"Include N context lines around each highlighted line (requires --highlight)."`
	Wrap       bool     `short:"w" help:"Wrap long lines at a column boundary."`
	WrapWidth  int      `name:"wrap-width" default:"0" help:"Columns per row (default: 100, minimum 20). Implies --wrap."`
	Output     string   `short:"o" default:"output.png" help:"Output PNG file path."`
	Title      string   `short:"t" help:"Title shown in the tab bar (defaults to file basename)."`
	Theme      string   `short:"T" default:"vscode-dark" help:"Color theme. Use --list-themes to see options."`
	ListThemes bool     `name:"list-themes" help:"Print available themes and exit."`
	JSON       bool     `name:"json" help:"Write a machine-readable report to stdout. Human messages stay on stderr."`
}

// HTTPCmd renders a side-by-side HTTP request/response evidence image.
type HTTPCmd struct {
	Request   string   `short:"r" required:"" help:"File containing the HTTP request."`
	Response  string   `short:"s" required:"" help:"File containing the HTTP response."`
	ReqHL     string   `name:"hr" help:"Regions to highlight in request: '8-9' or '8:20-45' for columns."`
	ResHL     string   `name:"hs" help:"Regions to highlight in response: '4' or '4:12-60' for columns."`
	Mark      []string `short:"m" help:"Highlight every occurrence of this text in either panel. Repeatable."`
	MaxMarks  int      `name:"max-marks" default:"0" help:"Keep at most N matches per --mark literal, earliest first (default: all)."`
	Label     string   `short:"L" help:"Caption strip stating what the image proves."`
	ReqLines  string   `name:"nr" help:"Lines to include from the request, e.g. '1-20'."`
	ResLines  string   `name:"ns" help:"Lines to include from the response, e.g. '1-30'."`
	Context   int      `short:"c" default:"0" help:"Include N context lines around each highlighted line; applies per panel."`
	WrapWidth int      `name:"wrap-width" default:"0" help:"Columns per panel (default: 90, minimum 20)."`
	Output    string   `short:"o" default:"output.png" help:"Output PNG file path."`
	JSON      bool     `name:"json" help:"Write a machine-readable report to stdout. Human messages stay on stderr."`
}

var cli struct {
	Snap    SnapCmd `cmd:"" default:"withargs" help:"Render a code file as a PNG screenshot."`
	HTTP    HTTPCmd `cmd:"http" help:"Render HTTP request/response evidence (side-by-side)."`
	Version bool    `short:"v" name:"version" help:"Print version and exit."`
}

// version is set by GoReleaser via -ldflags "-X main.version=..."
var version = "dev"

func main() {
	// Show help when called with no arguments and stdin is a terminal.
	if len(os.Args) == 1 {
		fi, err := os.Stdin.Stat()
		if err != nil || (fi.Mode()&os.ModeCharDevice) != 0 {
			rep := newReporter(false, "snap")
			os.Exit(rep.fail(exitUsage, kindUsage, "no input. Run 'taker --help' for usage."))
		}
	}

	ctx := kong.Parse(&cli,
		kong.Name("taker"),
		kong.Description("Generate professional code & HTTP evidence screenshots for security audits."),
		kong.UsageOnError(),
		// kong exits 1 on a bad flag. The published contract says 2 for a usage
		// error and 0 for --help, so say which kind of failure this is instead
		// of leaving a consumer to guess. The code is also what makes the
		// difference between "I called it wrong" and "the input was bad"
		// available without parsing anything.
		kong.Exit(func(code int) {
			if code == 0 {
				os.Exit(exitOK)
			}
			os.Exit(exitUsage)
		}),
	)

	if cli.Version {
		fmt.Println(version)
		os.Exit(exitOK)
	}

	mode, jsonOut := "snap", cli.Snap.JSON
	if ctx.Command() == "http" {
		mode, jsonOut = "http", cli.HTTP.JSON
	}
	rep := newReporter(jsonOut, mode)

	var code int
	switch ctx.Command() {
	case "http":
		code = runHTTP(&cli.HTTP, rep)
	default:
		code = runSnap(&cli.Snap, rep)
	}
	os.Exit(code)
}

func runSnap(cmd *SnapCmd, rep *reporter) int {
	if cmd.ListThemes {
		return listThemes(rep)
	}

	theme, err := resolveTheme(cmd.Theme)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "%v", err)
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
		src, origin, err = readGitBlob(cmd.Git)
	case cmd.File != "":
		src, err = os.ReadFile(cmd.File)
		origin = sourceInfo{Kind: "file", Path: cmd.File}
	default:
		// Not /dev/stdin: that path does not exist on Windows.
		src, err = io.ReadAll(os.Stdin)
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
	marked, stats := highlight.FindMarks([][]highlight.LineTokens{all}, cmd.Mark, cmd.MaxMarks)
	recordMarks(rep, stats)
	hlLines = hlLines.Merge(marked[0])
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
		return rep.fail(exitRefused, kindRefused, "no lines matched (file has %d lines)", len(all))
	}

	// Only a file read from its first line has a known length. For an excerpt
	// the tool has no idea how long the real file is, and a wrong total is
	// worse than none.
	total := excerptTotal(len(all), cmd.FirstLine)
	img, err := render.Render(&render.Config{
		Lines:          lines,
		Title:          title,
		HighlightLines: hlLines,
		TotalLines:     total,
		SourceRef:      origin.Ref,
		Theme:          theme,
		Wrap:           cmd.Wrap,
		WrapWidth:      cmd.WrapWidth,
		Caption:        cmd.Label,
		// The render's own warnings belong in the report: a consumer reading
		// --json has no stderr to notice them on.
		Warn: func(msg string) { rep.warn("%s", msg) },
	})
	if err != nil {
		return rep.fail(exitRefused, kindRefused, "render: %v", err)
	}
	if err := writePNG(img, cmd.Output, rep); err != nil {
		return rep.fail(exitFail, kindOutput, "%v", err)
	}

	// What the image shows, measured off the view that was drawn rather than
	// assumed from the flags that asked for it.
	rep.rep.Crop = &Crop{}
	*rep.rep.Crop = cropOf(highlight.Extent(lines), total)
	return rep.done(cmd.Output)
}

func runHTTP(cmd *HTTPCmd, rep *reporter) int {
	cmd.WrapWidth = clampWrapWidth(cmd.WrapWidth, true, rep)

	reqSrc, err := os.ReadFile(cmd.Request)
	if err != nil {
		return rep.fail(exitInput, kindInput, "read request: %v", err)
	}
	resSrc, err := os.ReadFile(cmd.Response)
	if err != nil {
		return rep.fail(exitInput, kindInput, "read response: %v", err)
	}
	// An empty capture is a mistake upstream, not evidence of anything.
	if len(bytes.TrimSpace(reqSrc)) == 0 {
		return rep.fail(exitInput, kindInput, "%s is empty", cmd.Request)
	}
	if len(bytes.TrimSpace(resSrc)) == 0 {
		return rep.fail(exitInput, kindInput, "%s is empty", cmd.Response)
	}

	reqHL, err := highlight.ParseHighlightSpec(cmd.ReqHL)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "parse --hr: %v", err)
	}
	resHL, err := highlight.ParseHighlightSpec(cmd.ResHL)
	if err != nil {
		return rep.fail(exitUsage, kindUsage, "parse --hs: %v", err)
	}

	// --mark's matches are found while rendering, so they count here too.
	if cmd.Context > 0 && len(reqHL) == 0 && len(resHL) == 0 && len(cmd.Mark) == 0 {
		return rep.fail(exitUsage, kindUsage, "--context needs --hr, --hs or --mark to center on")
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

	// The renderer resolves --mark while it draws, so it is the only place that
	// knows the view it produced; it hands back what it drew.
	facts := &render.HTTPFacts{}
	img, err := render.RenderHTTP(&render.HTTPConfig{
		Request:       string(reqSrc),
		Response:      string(resSrc),
		RequestHL:     reqHL,
		ResponseHL:    resHL,
		Mark:          cmd.Mark,
		MaxMarks:      cmd.MaxMarks,
		RequestLines:  reqLines,
		ResponseLines: resLines,
		Context:       cmd.Context,
		WrapWidth:     cmd.WrapWidth,
		Caption:       cmd.Label,
		Facts:         facts,
		Warn:          func(msg string) { rep.warn("%s", msg) },
	})
	if err != nil {
		return rep.fail(exitRefused, kindRefused, "render: %v", err)
	}
	recordMarks(rep, facts.Marks)
	rep.rep.Source = &Source{Kind: "http", Path: cmd.Request}
	rep.rep.Panels = &Panels{
		Request:  cropOf(facts.Request.ViewExtent, facts.Request.Total),
		Response: cropOf(facts.Response.ViewExtent, facts.Response.Total),
	}
	if err := writePNG(img, cmd.Output, rep); err != nil {
		return rep.fail(exitFail, kindOutput, "%v", err)
	}
	return rep.done(cmd.Output)
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
	if !rep.machines {
		fmt.Fprintln(rep.out, "Available themes:")
		for _, name := range names {
			note := ""
			switch {
			case name == "vscode-dark":
				note = "  (default)"
			case render.Themes[name].HTTPOnly:
				note = "  (`taker http` only \u2014 not for code)"
			}
			fmt.Fprintf(rep.out, "    %-20s  %s%s\n", name, render.Themes[name].Name, note)
		}
	}
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
func writePNG(img image.Image, path string, rep *reporter) error {
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
	if err := png.Encode(tmp, img); err != nil {
		return fail(fmt.Errorf("encode png: %w", err))
	}
	if err := tmp.Close(); err != nil {
		return fail(fmt.Errorf("write output: %w", err))
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("save output: %w", err)
	}
	// On stderr: with --json, stdout carries the report and nothing else.
	rep.note("wrote %s", path)
	return nil
}
