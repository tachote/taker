package render

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"path/filepath"
	"unicode/utf8"

	customfont "github.com/cvargas/taker/font"
	"github.com/cvargas/taker/highlight"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

const (
	fontSizePt       = 13.0
	fontDPI          = 144.0
	lineHeightMul    = 1.5
	paddingX         = 32
	paddingY         = 20
	tabBarHeight     = 38
	gutterPadR       = 20
	gutterPadL       = 16
	gapRowHeight     = 18
	highlightBorderW = 2
	maxImageWidth    = 2400
	iconSizePt       = 9.0
	defaultWrapCols  = 100
)

// Config holds all parameters for a single render pass.
type Config struct {
	Lines          []highlight.LineTokens
	Title          string
	HighlightLines map[int]bool
	Theme          *Theme
	Wrap           bool // enable word wrap
	WrapWidth      int  // columns per visual row (0 = defaultWrapCols)
	HTTPMode       bool // use simple panel header instead of VS Code tab bar
}

// visualLine is one rendered row — may be a logical line, a continuation row
// from word wrap, or a gap sentinel.
type visualLine struct {
	lineNum        int
	isGap          bool
	isContinuation bool // second+ row of a wrapped logical line
	tokens         []highlight.Token
}

// buildVisualLines converts logical lines into visual rows, splitting long
// lines when wrapCols > 0.
func buildVisualLines(lines []highlight.LineTokens, wrapCols int) []visualLine {
	var result []visualLine
	for _, lt := range lines {
		if lt.IsGap {
			result = append(result, visualLine{isGap: true})
			continue
		}
		if wrapCols <= 0 {
			result = append(result, visualLine{lineNum: lt.LineNum, tokens: lt.Tokens})
			continue
		}
		rows := splitTokensAt(lt.Tokens, wrapCols)
		for i, row := range rows {
			result = append(result, visualLine{
				lineNum:        lt.LineNum,
				isContinuation: i > 0,
				tokens:         row,
			})
		}
	}
	return result
}

// splitTokensAt splits a token slice into rows of at most cols characters each.
func splitTokensAt(tokens []highlight.Token, cols int) [][]highlight.Token {
	var rows [][]highlight.Token
	var current []highlight.Token
	used := 0

	for _, tok := range tokens {
		runes := []rune(tok.Text)
		start := 0
		for start < len(runes) {
			avail := cols - used
			if avail <= 0 {
				rows = append(rows, current)
				current = nil
				used = 0
				avail = cols
			}
			end := start + avail
			if end > len(runes) {
				end = len(runes)
			}
			current = append(current, highlight.Token{
				Text:   string(runes[start:end]),
				R:      tok.R,
				G:      tok.G,
				B:      tok.B,
				Bold:   tok.Bold,
				Italic: tok.Italic,
			})
			used += end - start
			start = end
		}
	}
	// Always append the last (possibly empty) row so blank lines render.
	rows = append(rows, current)
	return rows
}

type layout struct {
	face        font.Face
	iconFace    font.Face
	charWidth   int
	lineHeight  int
	ascent      int
	iconAscent  int
	iconW       int
	gutterW     int
	contentX    int
	totalWidth  int
	totalHeight int
	codeAreaY   int
}

func buildLayout(cfg *Config, vlines []visualLine, wrapCols int) (*layout, error) {
	face, err := customfont.LoadFace(fontSizePt, fontDPI)
	if err != nil {
		return nil, fmt.Errorf("load font: %w", err)
	}

	iconFace, err := customfont.LoadIconFace(iconSizePt, fontDPI)
	if err != nil {
		return nil, fmt.Errorf("load icon font: %w", err)
	}

	iconMetrics := iconFace.Metrics()
	iconAscent := int(iconMetrics.Ascent >> 6)
	iconAdv, ok := iconFace.GlyphAdvance('\ue627')
	if !ok {
		iconAdv = fixed.I(14)
	}
	iconW := int(iconAdv >> 6)

	metrics := face.Metrics()
	ascent := int(metrics.Ascent >> 6)
	descent := int(metrics.Descent >> 6)
	lineH := int(float64(ascent+descent) * lineHeightMul)

	adv, ok := face.GlyphAdvance('M')
	if !ok {
		adv = fixed.I(8)
	}
	charW := int(adv >> 6)

	// Gutter width from max line number in original lines.
	maxLineNum := 1
	for _, lt := range cfg.Lines {
		if !lt.IsGap && lt.LineNum > maxLineNum {
			maxLineNum = lt.LineNum
		}
	}
	digits := len(fmt.Sprintf("%d", maxLineNum))
	gutterW := gutterPadL + digits*charW + gutterPadR
	contentX := gutterW + 1

	// Canvas width: fixed at wrapCols when wrapping, otherwise longest line.
	var maxChars int
	if wrapCols > 0 {
		maxChars = wrapCols
	} else {
		maxChars = highlight.LongestLineLen(cfg.Lines)
		if maxChars < 20 {
			maxChars = 20
		}
	}

	totalW := contentX + paddingX + maxChars*charW + paddingX
	if totalW > maxImageWidth {
		totalW = maxImageWidth
	}

	// Height: sum of visual row heights.
	totalH := tabBarHeight + paddingY
	for _, vl := range vlines {
		if vl.isGap {
			totalH += gapRowHeight
		} else {
			totalH += lineH
		}
	}
	totalH += paddingY

	return &layout{
		face:        face,
		iconFace:    iconFace,
		charWidth:   charW,
		lineHeight:  lineH,
		ascent:      ascent,
		iconAscent:  iconAscent,
		iconW:       iconW,
		gutterW:     gutterW,
		contentX:    contentX,
		totalWidth:  totalW,
		totalHeight: totalH,
		codeAreaY:   tabBarHeight,
	}, nil
}

// Render produces the final PNG image.
func Render(cfg *Config) (*image.NRGBA, error) {
	if cfg.Theme == nil {
		cfg.Theme = DefaultTheme
	}
	ui := cfg.Theme.UI

	wrapCols := 0
	if cfg.Wrap {
		wrapCols = cfg.WrapWidth
		if wrapCols <= 0 {
			wrapCols = defaultWrapCols
		}
	}

	vlines := buildVisualLines(cfg.Lines, wrapCols)

	l, err := buildLayout(cfg, vlines, wrapCols)
	if err != nil {
		return nil, err
	}

	img := image.NewNRGBA(image.Rect(0, 0, l.totalWidth, l.totalHeight))

	// Pass 1: backgrounds
	fillRect(img, image.Rect(0, 0, l.totalWidth, l.totalHeight), ui.Bg)
	fillRect(img, image.Rect(0, 0, l.totalWidth, tabBarHeight), ui.TabBarBg)
	fillRect(img, image.Rect(0, tabBarHeight, l.totalWidth, l.totalHeight), ui.Bg)
	fillRect(img, image.Rect(0, tabBarHeight, l.gutterW, l.totalHeight), ui.Bg)
	fillRect(img, image.Rect(l.gutterW, tabBarHeight, l.gutterW+1, l.totalHeight), ui.GutterSep)

	// Pass 2: header
	if cfg.HTTPMode {
		drawHTTPPanelHeader(img, l, cfg.Title, ui)
	} else {
		drawTabBar(img, l, cfg.Title, ui)
	}

	type rowBounds struct{ y1, y2 int }
	var hlBounds []rowBounds

	// Pass 3: visual lines
	y := l.codeAreaY + paddingY
	digits := len(fmt.Sprintf("%d", func() int {
		m := 1
		for _, lt := range cfg.Lines {
			if !lt.IsGap && lt.LineNum > m {
				m = lt.LineNum
			}
		}
		return m
	}()))

	for _, vl := range vlines {
		if vl.isGap {
			fillRect(img, image.Rect(0, y, l.totalWidth, y+gapRowHeight), ui.GapBg)
			drawString(img, l.face, "⋮", l.contentX+paddingX/2, y+gapRowHeight-4, ui.GapFg)
			y += gapRowHeight
			continue
		}

		rowY := y
		rowBottom := y + l.lineHeight

		isHL := len(cfg.HighlightLines) > 0 && cfg.HighlightLines[vl.lineNum]
		if isHL {
			fillRect(img, image.Rect(0, rowY, l.totalWidth, rowBottom), ui.HighlightBg)
			hlBounds = append(hlBounds, rowBounds{rowY, rowBottom})
		}

		// Line number: only on the first visual row; continuation rows get a
		// subtle wrap arrow in the gutter instead.
		if !vl.isContinuation {
			lineNumStr := fmt.Sprintf("%*d", digits, vl.lineNum)
			numW := utf8.RuneCountInString(lineNumStr) * l.charWidth
			numX := l.gutterW - gutterPadR - numW
			drawString(img, l.face, lineNumStr, numX, rowY+l.ascent, ui.LineNumFg)
		} else {
			// Draw a faint ↩ right-aligned in the gutter to signal continuation.
			arrowW := l.charWidth
			arrowX := l.gutterW - gutterPadR - arrowW
			drawString(img, l.face, "↩", arrowX, rowY+l.ascent, ui.GapFg)
		}

		// Code tokens
		tokenX := l.contentX + paddingX
		baseline := rowY + l.ascent
		for _, tok := range vl.tokens {
			c := color.NRGBA{tok.R, tok.G, tok.B, 0xff}
			drawString(img, l.face, tok.Text, tokenX, baseline, c)
			tokenX += utf8.RuneCountInString(tok.Text) * l.charWidth
		}

		y += l.lineHeight
	}

	// Pass 4: highlight border
	if len(hlBounds) > 0 {
		y1 := hlBounds[0].y1
		y2 := hlBounds[len(hlBounds)-1].y2
		drawRectOutline(img, image.Rect(1, y1, l.totalWidth-1, y2), ui.HighlightBorder, highlightBorderW)
	}

	return img, nil
}

func drawTabBar(img *image.NRGBA, l *layout, title string, ui UIColors) {
	const (
		tabPad    = 14
		iconGap   = 7
		closeBtnW = 8
		closeGap  = 8
	)
	titleW := utf8.RuneCountInString(title) * l.charWidth
	tabW := tabPad + l.iconW + iconGap + titleW + closeGap + closeBtnW + tabPad
	if tabW > l.totalWidth {
		tabW = l.totalWidth
	}

	fillRect(img, image.Rect(0, 0, tabW, tabBarHeight), ui.ActiveTab)
	fillRect(img, image.Rect(0, 0, tabW, 2), ui.TabAccent)
	fillRect(img, image.Rect(tabW, 0, tabW+1, tabBarHeight), ui.TabBorder)
	fillRect(img, image.Rect(tabW+1, 0, l.totalWidth, tabBarHeight), ui.TabBarBg)
	fillRect(img, image.Rect(0, tabBarHeight-1, l.totalWidth, tabBarHeight), ui.TabBorder)

	iconColor := fileIconColor(title)
	iconGlyph := fileIconGlyph(title)
	iconX := tabPad
	iconBaseline := (tabBarHeight + l.iconAscent) / 2
	drawString(img, l.iconFace, string(iconGlyph), iconX, iconBaseline, iconColor)

	textX := iconX + l.iconW + iconGap
	textY := tabBarHeight/2 + l.ascent/2 - 2
	drawString(img, l.face, title, textX, textY, ui.TabText)

	closeX := tabW - tabPad - closeBtnW
	closeY := (tabBarHeight - closeBtnW) / 2
	fillRect(img, image.Rect(closeX, closeY, closeX+closeBtnW, closeY+closeBtnW),
		color.NRGBA{0x99, 0x99, 0x99, 0x60})

	_ = filepath.Base
}

func drawString(img *image.NRGBA, face font.Face, s string, x, baseline int, c color.NRGBA) {
	d := font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(c),
		Face: face,
		Dot:  fixed.P(x, baseline),
	}
	d.DrawString(s)
}

func fillRect(img *image.NRGBA, r image.Rectangle, c color.NRGBA) {
	draw.Draw(img, r, image.NewUniform(c), image.Point{}, draw.Src)
}

func drawRectOutline(img *image.NRGBA, r image.Rectangle, c color.NRGBA, thickness int) {
	fillRect(img, image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+thickness), c)
	fillRect(img, image.Rect(r.Min.X, r.Max.Y-thickness, r.Max.X, r.Max.Y), c)
	fillRect(img, image.Rect(r.Min.X, r.Min.Y, r.Min.X+thickness, r.Max.Y), c)
	fillRect(img, image.Rect(r.Max.X-thickness, r.Min.Y, r.Max.X, r.Max.Y), c)
}
