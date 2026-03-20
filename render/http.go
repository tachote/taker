package render

import (
	"fmt"
	"image"
	"image/draw"

	"github.com/tachote/taker/highlight"
)

const (
	httpDividerW    = 2
	httpDefaultCols = 90
)

// HTTPConfig holds parameters for HTTP request/response evidence rendering.
type HTTPConfig struct {
	Request   string
	Response  string
	RequestHL map[int]bool
	ResponseHL map[int]bool
	Theme     *Theme
	WrapWidth int // columns per panel (0 = httpDefaultCols)
}

// RenderHTTP renders a side-by-side HTTP request/response evidence image.
func RenderHTTP(cfg *HTTPConfig) (*image.NRGBA, error) {
	if cfg.Theme == nil {
		cfg.Theme = DefaultTheme
	}

	wrapCols := cfg.WrapWidth
	if wrapCols <= 0 {
		wrapCols = httpDefaultCols
	}

	reqLines, err := highlight.Tokenize(cfg.Request, "http", "request.http", cfg.Theme.Chroma)
	if err != nil {
		return nil, fmt.Errorf("tokenize request: %w", err)
	}
	resLines, err := highlight.Tokenize(cfg.Response, "http", "response.http", cfg.Theme.Chroma)
	if err != nil {
		return nil, fmt.Errorf("tokenize response: %w", err)
	}

	reqImg, err := renderHTTPPanel(reqLines, cfg.RequestHL, "Request", cfg.Theme, wrapCols)
	if err != nil {
		return nil, fmt.Errorf("render request: %w", err)
	}
	resImg, err := renderHTTPPanel(resLines, cfg.ResponseHL, "Response", cfg.Theme, wrapCols)
	if err != nil {
		return nil, fmt.Errorf("render response: %w", err)
	}

	return composeHTTP(reqImg, resImg, cfg.Theme.UI), nil
}

func renderHTTPPanel(lines []highlight.LineTokens, hlLines map[int]bool, title string, theme *Theme, wrapCols int) (*image.NRGBA, error) {
	return Render(&Config{
		Lines:          lines,
		Title:          title,
		HighlightLines: hlLines,
		Theme:          theme,
		Wrap:           true,
		WrapWidth:      wrapCols,
		HTTPMode:       true,
	})
}

// composeHTTP places two panel images side by side with a divider.
func composeHTTP(left, right *image.NRGBA, ui UIColors) *image.NRGBA {
	lw := left.Bounds().Dx()
	lh := left.Bounds().Dy()
	rw := right.Bounds().Dx()
	rh := right.Bounds().Dy()

	totalW := lw + httpDividerW + rw
	totalH := lh
	if rh > totalH {
		totalH = rh
	}

	out := image.NewNRGBA(image.Rect(0, 0, totalW, totalH))
	draw.Draw(out, out.Bounds(), image.NewUniform(ui.Bg), image.Point{}, draw.Src)

	// Left panel
	draw.Draw(out, left.Bounds(), left, image.Point{}, draw.Src)

	// Vertical divider
	fillRect(out, image.Rect(lw, 0, lw+httpDividerW, totalH), ui.GutterSep)

	// Right panel
	draw.Draw(out,
		image.Rect(lw+httpDividerW, 0, lw+httpDividerW+rw, rh),
		right,
		image.Point{0, 0},
		draw.Src,
	)

	return out
}

// drawHTTPPanelHeader draws a simple "Request" / "Response" header bar.
func drawHTTPPanelHeader(img *image.NRGBA, l *layout, title string, ui UIColors) {
	// Background
	fillRect(img, image.Rect(0, 0, l.totalWidth, tabBarHeight), ui.TabBarBg)
	// Left accent strip (3px)
	fillRect(img, image.Rect(0, 0, 3, tabBarHeight), ui.TabAccent)
	// Bottom border
	fillRect(img, image.Rect(0, tabBarHeight-1, l.totalWidth, tabBarHeight), ui.TabBorder)
	// Title text
	textX := 12
	textY := tabBarHeight/2 + l.ascent/2 - 2
	drawString(img, l.face, title, textX, textY, ui.TabText)
}
