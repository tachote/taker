package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/tachote/taker/highlight"
	"github.com/tachote/taker/render"
)

// SnapCmd renders a single code file.
type SnapCmd struct {
	File       string `arg:"" optional:"" help:"Source file to render (reads stdin if omitted)."`
	Lang       string `short:"l" help:"Language for syntax highlighting (auto-detected if omitted)."`
	Lines      string `short:"n" help:"Lines to include, e.g. '1,5,10-20'. Ignored when --context is set."`
	Highlight  string `short:"H" help:"Lines to highlight, e.g. '15' or '13,15' or '10-20'."`
	Context    int    `short:"c" default:"0" help:"Include N context lines around each highlighted line (requires --highlight)."`
	Wrap       bool   `short:"w" help:"Wrap long lines (like VS Code word wrap)."`
	WrapWidth  int    `name:"wrap-width" default:"0" help:"Column width for wrapping (default: 100)."`
	Output     string `short:"o" default:"output.png" help:"Output PNG file path."`
	Title      string `short:"t" help:"Title shown in the tab bar (defaults to file basename)."`
	Theme      string `short:"T" default:"vscode-dark" help:"Color theme. Use --list-themes to see options."`
	ListThemes bool   `name:"list-themes" help:"Print available themes and exit."`
}

// HTTPCmd renders a side-by-side HTTP request/response evidence image.
type HTTPCmd struct {
	Request   string `short:"r" required:"" help:"File containing the HTTP request."`
	Response  string `short:"s" required:"" help:"File containing the HTTP response."`
	ReqHL     string `name:"hr" help:"Lines to highlight in request, e.g. '8-9'."`
	ResHL     string `name:"hs" help:"Lines to highlight in response, e.g. '4'."`
	WrapWidth int    `name:"wrap-width" default:"0" help:"Columns per panel (default: 90)."`
	Output    string `short:"o" default:"output.png" help:"Output PNG file path."`
	Theme     string `short:"T" default:"vscode-dark" help:"Color theme."`
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
			fmt.Fprintln(os.Stderr, "taker: no input. Run 'taker --help' for usage.")
			os.Exit(1)
		}
	}

	ctx := kong.Parse(&cli,
		kong.Name("taker"),
		kong.Description("Generate professional code & HTTP evidence screenshots for security audits."),
		kong.UsageOnError(),
	)

	if cli.Version {
		fmt.Println(version)
		os.Exit(0)
	}

	switch ctx.Command() {
	case "http":
		runHTTP(&cli.HTTP)
	default:
		runSnap(&cli.Snap)
	}
}

func runSnap(cmd *SnapCmd) {
	if cmd.ListThemes {
		names := make([]string, 0, len(render.Themes))
		for k := range render.Themes {
			names = append(names, k)
		}
		sort.Strings(names)
		fmt.Println("Available themes:")
		for _, name := range names {
			marker := "  "
			if name == "vscode-dark" {
				marker = "* "
			}
			fmt.Printf("  %s%-20s  %s\n", marker, name, render.Themes[name].Name)
		}
		os.Exit(0)
	}

	theme := resolveTheme(cmd.Theme)

	var src []byte
	var err error
	if cmd.File != "" {
		src, err = os.ReadFile(cmd.File)
	} else {
		src, err = os.ReadFile("/dev/stdin")
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "taker: read input: %v\n", err)
		os.Exit(1)
	}

	title := cmd.Title
	if title == "" && cmd.File != "" {
		title = filepath.Base(cmd.File)
	}
	if title == "" {
		title = "code"
	}

	all, err := highlight.Tokenize(string(src), cmd.Lang, cmd.File, theme.Chroma)
	if err != nil {
		fmt.Fprintf(os.Stderr, "taker: tokenize: %v\n", err)
		os.Exit(1)
	}

	hlLines, err := highlight.ParseHighlightSpec(cmd.Highlight)
	if err != nil {
		fmt.Fprintf(os.Stderr, "taker: parse highlight: %v\n", err)
		os.Exit(1)
	}

	var lineSpec []int
	if cmd.Context > 0 {
		if len(hlLines) == 0 {
			fmt.Fprintf(os.Stderr, "taker: --context requires --highlight\n")
			os.Exit(1)
		}
		if cmd.Lines != "" {
			fmt.Fprintf(os.Stderr, "taker: --context overrides --lines; ignoring -n\n")
		}
		var nums []int
		for lineNum := range hlLines {
			start := lineNum - cmd.Context
			if start < 1 {
				start = 1
			}
			for i := start; i <= lineNum+cmd.Context; i++ {
				nums = append(nums, i)
			}
		}
		lineSpec = highlight.BuildLineSpec(nums)
	} else {
		lineSpec, err = highlight.ParseLineSpec(cmd.Lines)
		if err != nil {
			fmt.Fprintf(os.Stderr, "taker: parse lines: %v\n", err)
			os.Exit(1)
		}
	}

	lines := highlight.FilterLines(all, lineSpec)

	img, err := render.Render(&render.Config{
		Lines:          lines,
		Title:          title,
		HighlightLines: hlLines,
		Theme:          theme,
		Wrap:           cmd.Wrap,
		WrapWidth:      cmd.WrapWidth,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "taker: render: %v\n", err)
		os.Exit(1)
	}

	writePNG(img, cmd.Output)
}

func runHTTP(cmd *HTTPCmd) {
	theme := resolveTheme(cmd.Theme)

	reqSrc, err := os.ReadFile(cmd.Request)
	if err != nil {
		fmt.Fprintf(os.Stderr, "taker: read request: %v\n", err)
		os.Exit(1)
	}
	resSrc, err := os.ReadFile(cmd.Response)
	if err != nil {
		fmt.Fprintf(os.Stderr, "taker: read response: %v\n", err)
		os.Exit(1)
	}

	reqHL, err := highlight.ParseHighlightSpec(cmd.ReqHL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "taker: parse --hr: %v\n", err)
		os.Exit(1)
	}
	resHL, err := highlight.ParseHighlightSpec(cmd.ResHL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "taker: parse --hs: %v\n", err)
		os.Exit(1)
	}

	img, err := render.RenderHTTP(&render.HTTPConfig{
		Request:    string(reqSrc),
		Response:   string(resSrc),
		RequestHL:  reqHL,
		ResponseHL: resHL,
		Theme:      theme,
		WrapWidth:  cmd.WrapWidth,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "taker: render: %v\n", err)
		os.Exit(1)
	}

	writePNG(img, cmd.Output)
}

func resolveTheme(name string) *render.Theme {
	theme, ok := render.Themes[strings.ToLower(name)]
	if !ok {
		fmt.Fprintf(os.Stderr, "taker: unknown theme %q. Run --list-themes to see options.\n", name)
		os.Exit(1)
	}
	return theme
}

func writePNG(img image.Image, path string) {
	out, err := os.Create(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "taker: create output: %v\n", err)
		os.Exit(1)
	}
	defer out.Close()
	if err := png.Encode(out, img); err != nil {
		fmt.Fprintf(os.Stderr, "taker: encode png: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("taker: wrote %s\n", path)
}
