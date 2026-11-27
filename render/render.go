package render

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	customfont "github.com/tachote/taker/font"
	"github.com/tachote/taker/highlight"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

const (
	fontSizePt    = 13.0
	fontDPI       = 144.0
	lineHeightMul = 1.4
	paddingX      = 32
	paddingY      = 20
	tabBarHeight  = 38
	tabPad        = 14
	tabIconGap    = 7
	gutterPadR    = 20
	gutterPadL    = 16
	codeGutterGap = 16

	// Burp's panels are denser than an editor's. Its gutter reserves two digit
	// cells and leaves the line numbers 18px in from the pane edge, with a
	// hairline rule after them. The header is a title band over a tab row of
	// the same colour, closed by a 2px border — that shape is what makes the
	// panel read as a Repeater pane rather than a code block. Burp's band is
	// taller (109px) because it also carries the editor's toolbar; without the
	// toolbar that height is dead space above the title, so it is trimmed to
	// what the title needs. The row height is
	// the face's own line box with no added leading, as in Burp's editor: the
	// multiplier stays under 1 to mean exactly that, because a row taller than
	// its font is what made the panel look airy next to a real capture.
	burpLineHeightMul = 0.98
	burpTitleH        = 84
	burpTabRowH       = 46
	burpTabUnderlineH = 6
	burpTabBorderH    = 2
	burpPadY          = 4
	burpGutterPadL    = 18
	burpGutterPadR    = 12
	burpCodeGutterGap = 10
	burpTitleX        = 12
	gapRowHeight      = 30
	highlightBorderW  = 2
	highlightPadR     = 12 // keeps the box border off the window edge
	markPadX          = 3  // breathing room around a column mark
	wholeLineInsetL   = 4  // keeps a whole-line box's border off the pane edge
	maxImageWidth     = 2400
	// Browsers stop decoding past 32767px, so anything taller is not evidence
	// anyone can open. Warn well before that: past a few thousand pixels the
	// image stops being readable as a screenshot even though it still loads.
	maxImageHeight  = 32000
	warnImageHeight = 8000
	iconSizePt      = 11.0
	uiSizePt        = 10.0 // panel titles and tabs: UI text is smaller than code
	defaultWrapCols = 100
	minContentChars = 20
	chromeMargin    = 28
	chromeRadius    = 10
	chromeBorderW   = 1

	// Mark boxes get the window's own corner language at a smaller radius:
	// square strokes against a rounded window read as a different renderer.
	markCornerR = 4

	// The window chrome: a title band over the editor pane, macOS style —
	// three dots and the file name centred. It is the look a reader associates
	// with a screenshot taken for a report rather than an editor crop.
	windowBarH     = 46
	windowDotR     = 6
	windowDotGap   = 9
	windowDotInset = 22
	windowTitlePad = 60 // room kept on each side of the centred title for the dots

	// The drop shadow sits in the chrome margin: a soft falloff under the
	// window, offset down so the lift reads as light coming from above.
	shadowSpread  = 9
	shadowOffsetY = 7
	shadowAlpha   = 0.34
)

const (
	tabCloseGap = 10 // space between the title and the close glyph in the tab
	// gClose is nf-fa-close: the × a VS Code tab shows on its trailing edge.
	gClose = '\uf00d'
)

// Config holds all parameters for a single render pass.
type Config struct {
	Lines          []highlight.LineTokens
	Title          string
	HighlightLines highlight.Marks
	Theme          *Theme
	Wrap           bool   // enable word wrap
	WrapWidth      int    // columns per visual row (0 = defaultWrapCols)
	HTTPMode       bool   // panel header instead of tab bar; caller adds chrome
	MinHeight      int    // pad the panel to at least this height
	Caption        string // optional strip stating what the image proves
	// TotalLines is how many lines the file has, so a trimmed view can say what
	// it left out. 0 means unknown and prints no note.
	TotalLines int
	// SourceRef names the revision the code was read from, e.g.
	// "src/auth/login.py @ 9f344fad66cf". It travels in the footnote because the
	// code in the image has to be checkable against the commit being reported,
	// and a screenshot that does not name its revision cannot be checked at all.
	SourceRef string

	// WindowChrome draws a title band with traffic-light dots above the tab
	// bar, turning the editor pane into a window. Snap only.
	WindowChrome bool
	// Shadow lifts the window off the canvas with a soft drop shadow.
	Shadow bool

	// Warn, when set, receives the warnings this render produces — a cropped view
	// that lost marks, a character the font cannot draw, an image taller than a
	// viewer opens. The CLI routes them into its machine-readable report so a
	// consumer sees the same caveats the analyst sees; left nil they go to
	// stderr, which is what a caller rendering for its own eyes wants.
	Warn func(msg string)

	// RedactRegions are column spans covered by a solid box: the secret stays
	// out of the image while the box and the footnote say where it was. A
	// redaction is a hiding, not a highlighting — it is drawn over marks.
	RedactRegions highlight.Marks
	// RedactAsked means the caller requested masking; when the render then
	// redacts nothing, the footnote says so — "0 regions redacted" — instead
	// of letting the image pass for a clean capture.
	RedactAsked bool
	// Terminal drops the line-number gutter and the wrap arrow: a transcript
	// has no source lines to number.
	Terminal bool
	// Redacted is set by Render — how many boxes were drawn, so the footnote
	// and the machine report can declare the masking the image shows.
	Redacted int

	httpTabs []burpTab // view strip for an HTTP panel

	noWarn bool // tests silence warnings with it; nothing in the CLI sets it
}

// visualLine is one rendered row — may be a logical line, a continuation row
// from word wrap, or a gap sentinel.
type visualLine struct {
	lineNum        int
	isGap          bool
	skipped        int  // lines hidden by this gap (0 = unknown)
	isContinuation bool // second+ row of a wrapped logical line
	rowIndex       int  // which row of the wrapped logical line this is
	tokens         []highlight.Token
}

// runeLen is how many characters this row actually shows.
func (v visualLine) runeLen() int {
	n := 0
	for _, tok := range v.tokens {
		n += utf8.RuneCountInString(tok.Text)
	}
	return n
}

// buildVisualLines converts logical lines into visual rows, splitting long
// lines when wrapCols > 0.
func buildVisualLines(lines []highlight.LineTokens, wrapCols int) []visualLine {
	var result []visualLine
	for _, lt := range lines {
		if lt.IsGap {
			result = append(result, visualLine{isGap: true, skipped: lt.Skipped})
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
				rowIndex:       i,
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
				Text: string(runes[start:end]),
				R:    tok.R,
				G:    tok.G,
				B:    tok.B,
			})
			used += end - start
			start = end
		}
	}
	// Always append the last (possibly empty) row so blank lines render.
	rows = append(rows, current)
	return rows
}

// metrics holds the font faces and derived pixel sizes. Independent of content.
type metrics struct {
	face        font.Face
	uiFace      font.Face // panel chrome (titles, tabs) — smaller than the code face
	iconFace    font.Face
	charWidth   int
	ascent      int
	descent     int
	uiAscent    int
	uiDescent   int
	iconAscent  int
	iconDescent int
	iconW       int
}

// loadMetrics is the parse; metricsFor is the cache. The three faces come out
// of embedded TTFs on every call, and one HTTP render asks for them three
// times (each panel, then the caption). The metrics are content-independent,
// so one parse serves every render in the process.
var metricsFor = sync.OnceValues(loadMetrics)

func loadMetrics() (*metrics, error) {
	face, err := customfont.LoadFace(fontSizePt, fontDPI)
	if err != nil {
		return nil, fmt.Errorf("load font: %w", err)
	}
	uiFace, err := customfont.LoadFace(uiSizePt, fontDPI)
	if err != nil {
		return nil, fmt.Errorf("load ui font: %w", err)
	}
	iconFace, err := customfont.LoadIconFace(iconSizePt, fontDPI)
	if err != nil {
		return nil, fmt.Errorf("load icon font: %w", err)
	}

	iconMetrics := iconFace.Metrics()
	iconAdv, ok := iconFace.GlyphAdvance(0xf07d3)
	if !ok {
		iconAdv = fixed.I(14)
	}

	fm := face.Metrics()
	ascent := int(fm.Ascent >> 6)
	descent := int(fm.Descent >> 6)

	adv, ok := face.GlyphAdvance('M')
	if !ok {
		adv = fixed.I(8)
	}

	uiM := uiFace.Metrics()

	return &metrics{
		face:        newCachedFace(face),
		uiFace:      newCachedFace(uiFace),
		iconFace:    newCachedFace(iconFace),
		charWidth:   int(adv >> 6),
		ascent:      ascent,
		descent:     descent,
		uiAscent:    int(uiM.Ascent >> 6),
		uiDescent:   int(uiM.Descent >> 6),
		iconAscent:  int(iconMetrics.Ascent >> 6),
		iconDescent: int(iconMetrics.Descent >> 6),
		iconW:       int(iconAdv >> 6),
	}, nil
}

// baselineIn returns the baseline that vertically centers one text line in a
// band of the given height starting at y.
func (m *metrics) baselineIn(y, height int) int {
	return y + (height-(m.ascent+m.descent))/2 + m.ascent
}

// baselineUI is baselineIn for the smaller chrome face.
func (m *metrics) baselineUI(y, height int) int {
	return y + (height-(m.uiAscent+m.uiDescent))/2 + m.uiAscent
}

// baselineIcon centers the icon face's line box in a band. Ascent alone
// misplaces glyphs with deep bearings — the close × rode visibly high.
func (m *metrics) baselineIcon(y, height int) int {
	return y + (height-(m.iconAscent+m.iconDescent))/2 + m.iconAscent
}

type layout struct {
	*metrics
	digits      int
	lineHeight  int
	gutterW     int
	contentX    int
	codeX       int
	highlightR  int // right edge of the highlight band
	totalWidth  int
	neededWidth int // width before clamping to maxImageWidth
	totalHeight int
	codeAreaY   int // first pixel below the header
	padY        int
	gutterR     int
}

// geom is the mode-dependent spacing. Named fields on purpose: as a tuple of
// six ints, reordering it broke both call sites silently and still compiled.
type geom struct {
	lineMul float64
	headerH int
	padY    int
	gutterL int
	gutterR int
	codeGap int
	// canvasPadR is the margin between the widest row and the panel's right
	// edge. It is part of the layout, not a style choice: the wrap decision
	// measures against it, so it has to match what the layout actually leaves.
	canvasPadR int
}

// geometryFor returns the spacing for a mode. HTTP panels imitate Burp Suite's
// message editor; snap imitates a VS Code tab.
func geometryFor(httpMode bool) geom {
	if httpMode {
		return geom{
			lineMul:    burpLineHeightMul,
			headerH:    burpTitleH + burpTabRowH,
			padY:       burpPadY,
			gutterL:    burpGutterPadL,
			gutterR:    burpGutterPadR,
			codeGap:    burpCodeGutterGap,
			canvasPadR: burpGutterPadR,
		}
	}
	return geom{
		lineMul:    lineHeightMul,
		headerH:    tabBarHeight,
		padY:       paddingY,
		gutterL:    gutterPadL,
		gutterR:    gutterPadR,
		codeGap:    codeGutterGap,
		canvasPadR: paddingX,
	}
}

// gutterMetrics sizes the line-number gutter from the highest line number.
func gutterMetrics(lines []highlight.LineTokens, charW int, padL, padR int) (digits, gutterW int) {
	maxNum := 1
	for _, lt := range lines {
		if !lt.IsGap && lt.LineNum > maxNum {
			maxNum = lt.LineNum
		}
	}
	digits = len(strconv.Itoa(maxNum))
	return digits, padL + digits*charW + padR
}

func buildLayout(cfg *Config, m *metrics, vlines []visualLine, digits, gutterW int) *layout {
	g := geometryFor(cfg.HTTPMode)
	lineH := int(float64(m.ascent+m.descent) * g.lineMul)

	contentX := gutterW + 1
	codeX := contentX + g.codeGap

	// Canvas width from the widest rendered row, measured with real glyph
	// advances so non-ASCII text is not under-counted.
	contentW := minContentChars * m.charWidth
	for _, vl := range vlines {
		if vl.isGap {
			continue
		}
		if w := rowWidth(m.face, vl.tokens); w > contentW {
			contentW = w
		}
	}

	// neededW is content only: it drives the auto-wrap decision, and wrapping
	// cannot make an over-long title fit. The header widens the canvas but must
	// not make short code look like it overflowed. HTTP panels use the Burp
	// right gutter, not snap's editor padding, or the panel wraps ~20px early.
	neededW := codeX + contentW + g.canvasPadR
	totalW := neededW
	if hw := headerWidth(m, cfg); hw > totalW {
		totalW = hw
	}
	if totalW > maxImageWidth {
		totalW = maxImageWidth
	}

	totalH := g.headerH + g.padY
	for _, vl := range vlines {
		if vl.isGap {
			totalH += gapRowHeight
		} else {
			totalH += lineH
		}
	}
	totalH += g.padY
	if cfg.MinHeight > totalH {
		totalH = cfg.MinHeight
	}

	return &layout{
		metrics:     m,
		digits:      digits,
		lineHeight:  lineH,
		gutterW:     gutterW,
		contentX:    contentX,
		codeX:       codeX,
		highlightR:  totalW - highlightPadR,
		totalWidth:  totalW,
		neededWidth: neededW,
		totalHeight: totalH,
		codeAreaY:   g.headerH,
		padY:        g.padY,
		gutterR:     g.gutterR,
	}
}

// headerWidth is the minimum width needed to show the header without clipping —
// for an HTTP panel that is the tab row, which is wider than the title.
func headerWidth(m *metrics, cfg *Config) int {
	if cfg.HTTPMode {
		tab := 0
		for _, t := range cfg.httpTabs {
			tab += textWidth(m.uiFace, t.label) + burpTabPitch
		}
		return tab + burpTitleX
	}
	return tabPad + m.iconW + tabIconGap + textWidth(m.face, cfg.Title) +
		tabCloseGap + textWidth(m.iconFace, string(gClose)) + tabPad
}

// MaxWrapCols is the widest wrap that still fits the canvas, assuming the
// narrowest possible gutter. Content with more line numbers needs a wider
// gutter and so wraps a little sooner; this is the bound a caller can check
// without having tokenized anything.
func MaxWrapCols(httpMode bool) int {
	m, err := metricsFor()
	if err != nil {
		return defaultWrapCols
	}
	g := geometryFor(httpMode)
	return fitColumns(g, g.gutterL+m.charWidth+g.gutterR, m.charWidth) // one digit
}

// fitColumns is how many character cells fit between the gutter and the
// canvas cap. Render and MaxWrapCols must agree on it exactly: the CLI checks
// --wrap-width against MaxWrapCols, and Render clamps to its own copy, so two
// formulas that drift apart would report one bound and enforce another.
func fitColumns(g geom, gutterW, charW int) int {
	return max(minContentChars, (maxImageWidth-(gutterW+1+g.codeGap)-g.canvasPadR)/charW)
}

// Render produces the final PNG image.
func Render(cfg *Config) (*image.NRGBA, error) {
	if cfg.Theme == nil {
		cfg.Theme = DefaultTheme
	}
	ui := cfg.Theme.UI

	m, err := metricsFor()
	if err != nil {
		return nil, err
	}
	g := geometryFor(cfg.HTTPMode)
	digits, gutterW := 0, 0
	if !cfg.Terminal {
		digits, gutterW = gutterMetrics(cfg.Lines, m.charWidth, g.gutterL, g.gutterR)
	}

	// Columns that still fit inside the canvas cap. Anything past this would be
	// cropped off the right edge, which is exactly the silent evidence loss the
	// auto-wrap below exists to prevent.
	fitCols := fitColumns(g, gutterW, m.charWidth)

	wrapCols := 0
	if cfg.Wrap {
		wrapCols = cfg.WrapWidth
		switch {
		case wrapCols <= 0:
			wrapCols = defaultWrapCols
		case wrapCols < minContentChars:
			// Defensive floor for library callers; the CLI reports it once.
			// One character per row turns a long body into a 38000px column.
			wrapCols = minContentChars
		}
		// Silent, like the minimum-column floor: this is content-aware and would
		// fire once per panel with a different number each time. MaxWrapCols
		// gives the CLI a content-independent bound to report once.
		if wrapCols > fitCols {
			wrapCols = fitCols
		}
	}

	vlines := buildVisualLines(cfg.Lines, wrapCols)
	l := buildLayout(cfg, m, vlines, digits, gutterW)

	// Content wider than the canvas would be silently cropped — that loses the
	// evidence. Wrap instead, and say so.
	// ponytail: one retry is enough; wrapped rows are bounded by wrapCols.
	if wrapCols == 0 && l.neededWidth > maxImageWidth {
		wrapCols = fitCols
		if !cfg.noWarn {
			cfg.warnf("content exceeds %dpx; wrapping at %d columns (use --wrap-width to override)",
				maxImageWidth, wrapCols)
		}
		vlines = buildVisualLines(cfg.Lines, wrapCols)
		l = buildLayout(cfg, m, vlines, digits, gutterW)
	}

	// Refuse before allocating: a 130000px canvas is ~580MB and unviewable.
	if l.totalHeight > maxImageHeight {
		return nil, fmt.Errorf(
			"%q needs %dpx of height, past the %dpx browsers can decode — "+
				"trim it with --context or a line range",
			cfg.Title, l.totalHeight, maxImageHeight)
	}
	if l.totalHeight > warnImageHeight && !cfg.noWarn {
		cfg.warnf("%q is %dpx tall; --context or a line range makes it readable", cfg.Title, l.totalHeight)
	}

	// The redaction count is pure geometry, and the footnote that declares it
	// has to be sized before the canvas exists, so count it up front.
	var redactRuns []markRun
	{
		y := l.codeAreaY + l.padY
		for _, vl := range vlines {
			if vl.isGap {
				y += gapRowHeight
				continue
			}
			for _, box := range l.markBoxes(cfg.RedactRegions, vl.lineNum, vl, wrapCols) {
				redactRuns = append(redactRuns, markRun{box.x1, box.x2, y, y + l.lineHeight})
			}
			y += l.lineHeight
		}
	}
	cfg.Redacted = len(redactRuns)

	// A snap is composed on one canvas: the panel is drawn straight into its
	// place inside the window, and the bar, margin, shadow and strips around
	// it. Wrapping the panel in a new full-size image per layer cost a full
	// allocation, fill and copy each time — peak memory four times the PNG.
	var img *image.NRGBA
	var snap *snapCanvas
	if cfg.HTTPMode {
		img = image.NewNRGBA(image.Rect(0, 0, l.totalWidth, l.totalHeight))
	} else {
		snap = newSnapCanvas(cfg, l, m)
		img = snap.panel
	}

	// Pass 1: backgrounds. The gutter rule is a hairline between the line
	// numbers and the text: snap has always had one, and Burp's message editor
	// has one too, so both modes draw it.
	fillRect(img, image.Rect(0, 0, l.totalWidth, l.totalHeight), ui.Bg)
	if l.gutterW > 0 {
		fillRect(img, image.Rect(l.gutterW, l.codeAreaY, l.gutterW+1, l.totalHeight), ui.GutterSep)
	}

	// Pass 2: header
	if cfg.HTTPMode {
		drawHTTPPanelHeader(img, l, cfg, ui)
	} else {
		drawTabBar(img, l, cfg.Title, ui)
	}

	// Pass 3: visual lines
	var hlRuns []markRun
	y := l.codeAreaY + l.padY

	for _, vl := range vlines {
		if vl.isGap {
			fillRect(img, image.Rect(0, y, l.totalWidth, y+gapRowHeight), ui.GapBg)
			// Hairlines frame the band as a region; without them it reads as
			// an oddly empty code line.
			fillRect(img, image.Rect(0, y, l.totalWidth, y+1), ui.GutterSep)
			fillRect(img, image.Rect(0, y+gapRowHeight-1, l.totalWidth, y+gapRowHeight), ui.GutterSep)
			// Centred in the code area: the count belongs to the view, not to
			// a column of text. LineNumFg, not GapFg — it must stay readable.
			label := gapLabel(vl.skipped)
			lx := l.codeX + max(0, (l.totalWidth-l.codeX-textWidth(l.face, label))/2)
			drawString(img, l.face, label, lx, l.baselineIn(y, gapRowHeight), ui.LineNumFg)
			y += gapRowHeight
			continue
		}

		rowY := y
		rowBottom := y + l.lineHeight

		// A whole-line band plus a column box draws a border around the line and
		// another inside it, which reads as a rendering fault. When a line names
		// columns, the band keeps its wash for context and the precise marks own
		// every border on that line.
		precise := namesColumns(cfg.HighlightLines[vl.lineNum])
		for _, box := range l.markBoxes(cfg.HighlightLines, vl.lineNum, vl, wrapCols) {
			fillRect(img, image.Rect(box.x1, rowY, box.x2, rowBottom), ui.HighlightBg)
			if box.wholeLine && precise {
				continue
			}
			hlRuns = append(hlRuns, markRun{box.x1, box.x2, rowY, rowBottom})
		}

		// Line number on the first visual row only; continuation rows get a
		// subtle wrap arrow in the gutter instead. Drawn after the highlight
		// wash (above) so a whole-line mark never paints over the number.
		baseline := l.baselineIn(rowY, l.lineHeight)
		if l.gutterW > 0 {
			switch {
			case !vl.isContinuation:
				lineNumStr := fmt.Sprintf("%*d", l.digits, vl.lineNum)
				numX := l.gutterW - l.gutterR - l.digits*l.charWidth
				numFg := ui.LineNumFg
				if _, marked := cfg.HighlightLines[vl.lineNum]; marked {
					// Keep the number legible on top of the mark wash.
					numFg = ui.TabText
				}
				drawString(img, l.face, lineNumStr, numX, baseline, numFg)
			case !cfg.HTTPMode:
				// Burp leaves the gutter blank on wrapped rows; the missing
				// number is the marker. Snap gets an explicit arrow.
				arrowX := l.gutterW - l.gutterR - l.charWidth
				drawString(img, l.face, "↩", arrowX, baseline, ui.GapFg)
			}
		}

		// Code tokens
		tokenX := l.codeX
		for _, tok := range vl.tokens {
			c := color.NRGBA{tok.R, tok.G, tok.B, 0xff}
			drawString(img, l.face, tok.Text, tokenX, baseline, c)
			tokenX += textWidth(l.face, tok.Text)
		}

		y += l.lineHeight
	}

	// Asking for marks and getting none is how a useless screenshot reaches a
	// report. Judge it on the boxes actually drawn, not on whether the line
	// numbers exist: "7:900-999" names a real line and no real columns.
	if !cfg.noWarn && len(cfg.HighlightLines) > 0 && len(hlRuns) == 0 {
		cfg.warnf("nothing was marked in %q; the requested lines or columns are not in the output", cfg.Title)
	}

	// The quiet half of the same failure: with one mark inside the view and one
	// outside it, an image with a single box is written in silence while the
	// analyst's note says two lines are highlighted. The count of a --mark
	// literal is reported for the whole input, so without this the tool states
	// the matches it found and shows fewer.
	if !cfg.noWarn && len(hlRuns) > 0 {
		if absent := marksOutsideView(cfg.HighlightLines, vlines); len(absent) > 0 {
			cfg.warnf("marked %s %s %s not in the %q output",
				plural(len(absent), "line", "lines"), compressRanges(absent),
				plural(len(absent), "is", "are"), cfg.Title)
		}
	}

	reportUndrawable(cfg, vlines)

	// Pass 4: one border per contiguous run of equally-wide marked rows
	for _, run := range mergeRuns(hlRuns) {
		drawRoundedOutline(img, image.Rect(run.x1, run.y1, run.x2, run.y2),
			markCornerR, ui.HighlightBorder, highlightBorderW)
	}

	// Redaction goes last: a masked span must cover whatever it overlaps,
	// including a mark border. Black on every theme — a redaction bar reads
	// the same whether the report page is dark or white.
	for _, run := range mergeRuns(redactRuns) {
		fillRect(img, image.Rect(run.x1, run.y1, run.x2, run.y2), color.NRGBA{0, 0, 0, 0xff})
	}

	if cfg.HTTPMode {
		return img, nil // caller composes the panels, then adds the caption
	}
	return snap.finish(cfg, ui, m), nil
}

// snapCanvas is a snap's final image laid out before anything is drawn: the
// window (bar plus panel) inside the chrome margin, then the footnote and the
// caption strips below it.
type snapCanvas struct {
	out               *image.NRGBA
	win               image.Rectangle // the window inside the margin, bar included
	panel             *image.NRGBA    // zero-origin view of the editor pane inside out
	footnote, caption []string
	footTop, capTop   int // first pixel of each strip, below its gap
	capH              int // the caption strip's own height, gap excluded
}

// newSnapCanvas sizes the whole image, allocates it once and fills what the
// panel will not cover. The panel view has its origin at (0,0) and shares
// out's pixels, so the drawing code keeps its panel-relative coordinates.
func newSnapCanvas(cfg *Config, l *layout, m *metrics) *snapCanvas {
	ui := cfg.Theme.UI
	barH := 0
	if cfg.WindowChrome {
		barH = windowBarH
	}
	win := image.Rect(chromeMargin, chromeMargin,
		chromeMargin+l.totalWidth, chromeMargin+barH+l.totalHeight)
	sw, sh := win.Max.X+chromeMargin, win.Max.Y+chromeMargin

	// Caption and footnote go outside the window: they annotate the screenshot,
	// they are not part of the editor being screenshotted. The footnote sits
	// inside the caption, closest to the code it describes. Each may widen the
	// canvas for the next, which is why they are laid out in this order.
	c := &snapCanvas{win: win}
	note := joinNotes(cfg.SourceRef, cropNote("", cfg.Lines, cfg.TotalLines), redactNote(cfg.Redacted, cfg.RedactAsked))
	var footH, capH int
	c.footnote, sw, footH = footnoteStrip.layout(m, note, sw)
	c.caption, sw, capH = captionStrip.layout(m, cfg.Caption, sw)
	c.footTop = sh + footnoteGap
	c.capTop = sh + footH + captionGap
	c.capH = capH - captionGap

	c.out = image.NewNRGBA(image.Rect(0, 0, sw, sh+footH+capH))
	// Everything but the window is canvas; the panel paints its own background.
	fillRect(c.out, image.Rect(0, 0, sw, win.Min.Y), ui.ChromeBg)
	fillRect(c.out, image.Rect(0, win.Min.Y, win.Min.X, win.Max.Y), ui.ChromeBg)
	fillRect(c.out, image.Rect(win.Max.X, win.Min.Y, sw, win.Max.Y), ui.ChromeBg)
	fillRect(c.out, image.Rect(0, win.Max.Y, sw, c.out.Rect.Max.Y), ui.ChromeBg)

	c.panel = view(c.out, image.Rect(win.Min.X, win.Min.Y+barH, win.Max.X, win.Max.Y))
	return c
}

// finish draws what surrounds the panel once the panel is on the canvas.
func (c *snapCanvas) finish(cfg *Config, ui UIColors, m *metrics) *image.NRGBA {
	if cfg.WindowChrome {
		drawWindowBar(view(c.out, image.Rect(c.win.Min.X, c.win.Min.Y, c.win.Max.X, c.win.Min.Y+windowBarH)),
			cfg.Title, ui, m)
	}
	// The corners are masked back to whatever lies under the window there. With
	// a shadow that is the shadow, not the flat canvas: masking to ChromeBg cut
	// a light notch into the shadow at every rounded corner.
	bg := func(x, y int) color.NRGBA { return ui.ChromeBg }
	if cfg.Shadow {
		drawShadow(c.out, c.win)
		bg = func(x, y int) color.NRGBA {
			return mix(ui.ChromeBg, rgb(0, 0, 0), shadowAlphaAt(c.win, x, y))
		}
	}
	roundCorners(c.out, c.win, chromeRadius, bg, ui.TabBorder)
	drawFootnote(c.out, c.footTop, c.footnote, ui, m)
	drawCaption(c.out, c.capTop, c.capH, c.caption, ui, m)
	return c.out
}

// view is r of img as an image with its origin at (0,0), sharing img's pixels.
// Code that draws a panel in panel coordinates can then draw it in place.
func view(img *image.NRGBA, r image.Rectangle) *image.NRGBA {
	r = r.Intersect(img.Rect)
	return &image.NRGBA{
		Pix:    img.Pix[img.PixOffset(r.Min.X, r.Min.Y):],
		Stride: img.Stride,
		Rect:   image.Rect(0, 0, r.Dx(), r.Dy()),
	}
}

// markRun is one marked rectangle. Column marks make the horizontal extent
// vary between rows, so it travels with the run.
type markRun struct{ x1, x2, y1, y2 int }

// mergeRuns collapses vertically adjacent rows into single boxes, so that
// non-contiguous highlights get one box each instead of one box swallowing
// everything between them. Rows only merge when they span the same columns —
// otherwise a narrow column mark would be stretched to a neighbour's width.
//
// Runs are matched by their columns, not by order: a row with two marks
// arrives interleaved with the next row's (A1, B1, A2, B2), and comparing each
// row only with the last run kept A2 from ever meeting A1. Output keeps the
// order in which each run first appeared.
func mergeRuns(rows []markRun) []markRun {
	if len(rows) == 0 {
		return nil
	}
	var runs []markRun
	open := map[[2]int]int{} // columns -> index of the newest run with them
	for _, r := range rows {
		key := [2]int{r.x1, r.x2}
		if i, ok := open[key]; ok && runs[i].y2 == r.y1 {
			runs[i].y2 = r.y2
			continue
		}
		open[key] = len(runs)
		runs = append(runs, r)
	}
	return runs
}

// markBox is the horizontal extent of one mark on one visual row.
type markBox struct {
	x1, x2    int
	wholeLine bool
}

// markBoxes maps a line's marks onto the visual row in vl. A wrapped line
// splits into rows of wrapCols characters, so a span may land on one row,
// straddle several, or miss this row entirely.
//
// It takes the whole map rather than one line's spans so that "absent" and
// "marked with no spans" stay distinguishable: with a bare slice the two look
// alike, and Marks.Has would call a line marked while nothing was drawn.
func (l *layout) markBoxes(marks highlight.Marks, line int, vl visualLine, wrapCols int) []markBox {
	spans, marked := marks[line]
	if vl.isGap || !marked {
		return nil
	}
	if len(spans) == 0 {
		// Marked, but no region named. Start the band just inside the pane's
		// left edge so its wash sits behind the line number too: the number is the
		// reader's anchor and must not be swallowed by a half-covered band,
		// and the band reads as a full row the way Burp's line selection does.
		return []markBox{{x1: wholeLineInsetL, x2: l.highlightR, wholeLine: true}}
	}

	rowStart, rowEnd := 1, 0 // 1-based columns covered by this visual row
	if wrapCols > 0 {
		rowStart = vl.rowIndex*wrapCols + 1
		rowEnd = rowStart + wrapCols - 1
	}

	var boxes []markBox
	for _, sp := range spans {
		if sp.WholeLine() {
			// Full-width band, like an editor's current-line highlight. It starts
			// just inside the pane edge so the gutter and its number are inside
			// the wash, while its border stays clear of the pane's own outline.
			boxes = append(boxes, markBox{x1: wholeLineInsetL, x2: l.highlightR, wholeLine: true})
			continue
		}
		start, end := sp.Start, sp.End
		if rowEnd > 0 { // wrapped: clip to the columns this row shows
			if end < rowStart || start > rowEnd {
				continue
			}
			start = max(start, rowStart) - rowStart + 1
			end = min(end, rowEnd) - rowStart + 1
		} else if vl.isContinuation {
			continue
		}
		// Never mark past the text: a box floating in blank space reads as a
		// rendering fault, not as evidence.
		if width := vl.runeLen(); end > width {
			end = width
		}
		if end < start {
			continue
		}
		// Breathe a little around the glyph cells: a border flush against the
		// first and last character reads as clipping, not as a marker.
		boxes = append(boxes, markBox{
			x1: l.codeX + (start-1)*l.charWidth - markPadX,
			x2: l.codeX + end*l.charWidth + markPadX,
		})
	}
	return boxes
}

// namesColumns reports whether any span on a line points at specific columns.
func namesColumns(spans []highlight.Span) bool {
	for _, sp := range spans {
		if !sp.WholeLine() {
			return true
		}
	}
	return false
}

func gapLabel(skipped int) string {
	switch {
	case skipped == 1:
		return "··· 1 line skipped"
	case skipped > 1:
		return fmt.Sprintf("··· %d lines skipped", skipped)
	}
	return "···"
}

func drawTabBar(img *image.NRGBA, l *layout, title string, ui UIColors) {
	// A title wider than the canvas would run off the edge mid-word, which
	// looks like a broken render rather than a long path.
	closeW := textWidth(l.iconFace, string(gClose))
	chromeW := tabPad + l.iconW + tabIconGap + tabCloseGap + closeW + tabPad
	title = ellipsizeLeft(l.face, title, l.totalWidth-chromeW)

	tabW := chromeW + textWidth(l.face, title)
	if tabW > l.totalWidth {
		tabW = l.totalWidth
	}

	fillRect(img, image.Rect(0, 0, l.totalWidth, tabBarHeight), ui.TabBarBg)
	fillRect(img, image.Rect(0, 0, tabW, tabBarHeight), ui.ActiveTab)
	fillRect(img, image.Rect(0, 0, tabW, 2), ui.TabAccent)
	fillRect(img, image.Rect(tabW, 0, tabW+1, tabBarHeight), ui.TabBorder)
	fillRect(img, image.Rect(0, tabBarHeight-1, l.totalWidth, tabBarHeight), ui.TabBorder)

	ic := iconFor(title)
	drawString(img, l.iconFace, string(ic.glyph), tabPad,
		l.baselineIcon(0, tabBarHeight), ic.color)
	drawString(img, l.face, title, tabPad+l.iconW+tabIconGap,
		l.baselineIn(0, tabBarHeight), ui.TabText)
	// The close button rides the tab's trailing edge, like every editor tab.
	drawString(img, l.iconFace, string(gClose),
		tabW-tabPad-closeW, l.baselineIcon(0, tabBarHeight), ui.LineNumFg)
}

// ellipsizeLeft shortens s from the left to fit maxWidth. The tail is kept
// because for a path the file name identifies the evidence; the directories
// above it do not.
func ellipsizeLeft(face font.Face, s string, maxWidth int) string {
	if maxWidth <= 0 || textWidth(face, s) <= maxWidth {
		return s
	}
	const ellipsis = "…"
	ellW := textWidth(face, ellipsis)
	r := []rune(s)
	for i := 1; i < len(r); i++ {
		if ellW+textWidth(face, string(r[i:])) <= maxWidth {
			return ellipsis + string(r[i:])
		}
	}
	return ellipsis
}

// drawShadow lays the window's silhouette under it before the panel is pasted.
// The silhouette is the window rect pushed down shadowOffsetY; a Chebyshev
// distance ramp over shadowSpread pixels is what a blur reads as at this size.
//
// Pixels inside win are skipped: the panel owns them, and roundCorners
// repaints the corners from the background, so shading them was nine tenths
// of the work for none of the output. Skipping them also means the shadow can
// be laid after the panel is drawn in place.
func drawShadow(img *image.NRGBA, win image.Rectangle) {
	shifted := win.Add(image.Pt(0, shadowOffsetY))
	b := img.Bounds()
	y0 := max(b.Min.Y, shifted.Min.Y-shadowSpread)
	y1 := min(b.Max.Y, shifted.Max.Y+shadowSpread)
	x0 := max(b.Min.X, shifted.Min.X-shadowSpread)
	x1 := min(b.Max.X, shifted.Max.X+shadowSpread)
	for y := y0; y < y1; y++ {
		inWinRows := y >= win.Min.Y && y < win.Max.Y
		for x := x0; x < x1; x++ {
			if inWinRows && x >= win.Min.X && x < win.Max.X {
				x = win.Max.X - 1 // jump to the right margin
				continue
			}
			if t := shadowAlphaAt(win, x, y); t > 0 {
				img.SetNRGBA(x, y, mix(img.NRGBAAt(x, y), rgb(0, 0, 0), t))
			}
		}
	}
}

// shadowAlphaAt is the shadow's opacity at (x, y) for a window at win, zero
// outside its reach. roundCorners asks for it too, to rebuild the shadowed
// background under the corners it masks.
func shadowAlphaAt(win image.Rectangle, x, y int) float64 {
	shifted := win.Add(image.Pt(0, shadowOffsetY))
	d := max(max(shifted.Min.X-x, x-shifted.Max.X),
		max(shifted.Min.Y-y, y-shifted.Max.Y))
	return max(0, shadowAlpha*float64(shadowSpread-d)/float64(shadowSpread))
}

// drawWindowBar dresses the panel as a window: a title band above the tab bar
// carrying the traffic-light dots and the file name centred. The dots use the
// OS palette — they are the window's, not the app's. out is the band itself.
func drawWindowBar(out *image.NRGBA, title string, ui UIColors, m *metrics) {
	w := out.Bounds().Dx()
	fillRect(out, image.Rect(0, 0, w, windowBarH), ui.TabBarBg)
	fillRect(out, image.Rect(0, windowBarH-1, w, windowBarH), ui.TabBorder)

	dots := []color.NRGBA{
		rgb(0xff, 0x5f, 0x57), // close
		rgb(0xfe, 0xbc, 0x2e), // minimise
		rgb(0x28, 0xc8, 0x40), // zoom
	}
	cy := windowBarH / 2
	for i, d := range dots {
		drawCircle(out, windowDotInset+i*(windowDotR*2+windowDotGap)+windowDotR, cy, windowDotR, d)
	}

	if maxW := w - 2*windowTitlePad; textWidth(m.uiFace, title) > maxW {
		title = ellipsizeLeft(m.uiFace, title, maxW)
	}
	tx := max(0, (w-textWidth(m.uiFace, title))/2)
	drawString(out, m.uiFace, title, tx, m.baselineUI(0, windowBarH), ui.LineNumFg)
}

// drawCircle fills a disc with an anti-aliased edge.
func drawCircle(img *image.NRGBA, cx, cy, r int, c color.NRGBA) {
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			d := math.Hypot(float64(x)+0.5-float64(cx), float64(y)+0.5-float64(cy))
			if w := clamp01(float64(r) - d + 0.5); w > 0 {
				img.SetNRGBA(x, y, mix(img.NRGBAAt(x, y), c, w))
			}
		}
	}
}

// roundCorners masks the corners of r back to the background and strokes a
// rounded border. bg gives the background at a pixel, since a shadow makes it
// vary across the corner.
func roundCorners(img *image.NRGBA, r image.Rectangle, radius int, bg func(x, y int) color.NRGBA, border color.NRGBA) {
	fillRect(img, image.Rect(r.Min.X+radius, r.Min.Y, r.Max.X-radius, r.Min.Y+chromeBorderW), border)
	fillRect(img, image.Rect(r.Min.X+radius, r.Max.Y-chromeBorderW, r.Max.X-radius, r.Max.Y), border)
	fillRect(img, image.Rect(r.Min.X, r.Min.Y+radius, r.Min.X+chromeBorderW, r.Max.Y-radius), border)
	fillRect(img, image.Rect(r.Max.X-chromeBorderW, r.Min.Y+radius, r.Max.X, r.Max.Y-radius), border)

	// Each corner box is radius×radius; its arc center sits at the box's inner
	// diagonal, on pixel centers (hence the 0.5 offsets).
	type corner struct {
		cx, cy float64
		x0, y0 int
	}
	left, right := float64(r.Min.X+radius)-0.5, float64(r.Max.X-radius)-0.5
	top, bottom := float64(r.Min.Y+radius)-0.5, float64(r.Max.Y-radius)-0.5
	for _, c := range []corner{
		{left, top, r.Min.X, r.Min.Y},
		{right, top, r.Max.X - radius, r.Min.Y},
		{left, bottom, r.Min.X, r.Max.Y - radius},
		{right, bottom, r.Max.X - radius, r.Max.Y - radius},
	} {
		for y := c.y0; y < c.y0+radius; y++ {
			for x := c.x0; x < c.x0+radius; x++ {
				dist := math.Hypot(float64(x)-c.cx, float64(y)-c.cy)
				outer := clamp01(float64(radius) - dist + 0.5)
				ring := outer - clamp01(float64(radius-chromeBorderW)-dist+0.5)
				px := mix(bg(x, y), img.NRGBAAt(x, y), outer)
				img.SetNRGBA(x, y, mix(px, border, ring))
			}
		}
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// mix blends b over a with weight t (t=0 → a, t=1 → b).
func mix(a, b color.NRGBA, t float64) color.NRGBA {
	lerp := func(x, y uint8) uint8 {
		return uint8(math.Round(float64(x)*(1-t) + float64(y)*t))
	}
	return color.NRGBA{lerp(a.R, b.R), lerp(a.G, b.G), lerp(a.B, b.B), lerp(a.A, b.A)}
}

// textWidth is the drawn advance of s, matching what drawString will emit.
func textWidth(face font.Face, s string) int {
	return int(font.MeasureString(face, s) >> 6)
}

func rowWidth(face font.Face, toks []highlight.Token) int {
	w := 0
	for _, tok := range toks {
		w += textWidth(face, tok.Text)
	}
	return w
}

// drawString draws s like font.Drawer does — kerning, then each glyph at the
// pen — but composites the glyph masks itself. image/draw has no fast path for
// an NRGBA destination, and its generic per-pixel interface calls made text
// drawing the second-largest cost of a render.
func drawString(img *image.NRGBA, face font.Face, s string, x, baseline int, c color.NRGBA) {
	dot := fixed.P(x, baseline)
	prev := rune(-1)
	for _, r := range s {
		if prev >= 0 {
			dot.X += face.Kern(prev, r)
		}
		dr, mask, mp, adv, _ := face.Glyph(dot, r)
		if !dr.Empty() {
			if a, ok := mask.(*image.Alpha); ok {
				blendMask(img, dr, a, mp, c)
			} else {
				draw.DrawMask(img, dr, image.NewUniform(c), image.Point{}, mask, mp, draw.Over)
			}
		}
		dot.X += adv
		prev = r
	}
}

// blendMask composites colour c through an alpha mask onto img with Porter-Duff
// "over", the operation font.Drawer requests. dr is the destination rectangle
// and mp the mask point aligned with dr.Min.
func blendMask(img *image.NRGBA, dr image.Rectangle, mask *image.Alpha, mp image.Point, c color.NRGBA) {
	clipped := dr.Intersect(img.Rect)
	if clipped.Empty() {
		return
	}
	mp = mp.Add(clipped.Min.Sub(dr.Min))
	const m = 0xffff
	sr, sg, sb := uint64(c.R)*0x101, uint64(c.G)*0x101, uint64(c.B)*0x101
	ca := uint64(c.A)
	for y := clipped.Min.Y; y < clipped.Max.Y; y++ {
		di := img.PixOffset(clipped.Min.X, y)
		mi := mask.PixOffset(mp.X, mp.Y+y-clipped.Min.Y)
		for x := clipped.Min.X; x < clipped.Max.X; x, di, mi = x+1, di+4, mi+1 {
			ma := uint64(mask.Pix[mi])
			if ma == 0 {
				continue
			}
			a := ma * ca * 0x101 / 0xff // source coverage, 16-bit
			d := img.Pix[di : di+4 : di+4]
			if a == m {
				d[0], d[1], d[2], d[3] = c.R, c.G, c.B, 0xff
				continue
			}
			da := uint64(d[3]) * 0x101
			// Premultiplied over, then back to straight alpha for NRGBA.
			outA := a + da*(m-a)/m
			if outA == 0 {
				continue
			}
			dr := uint64(d[0]) * 0x101 * da / m
			dg := uint64(d[1]) * 0x101 * da / m
			db := uint64(d[2]) * 0x101 * da / m
			d[0] = uint8(((sr*a + dr*(m-a)) / m * m / outA) >> 8)
			d[1] = uint8(((sg*a + dg*(m-a)) / m * m / outA) >> 8)
			d[2] = uint8(((sb*a + db*(m-a)) / m * m / outA) >> 8)
			d[3] = uint8(outA >> 8)
		}
	}
}

// drawStringBold thickens a string by re-drawing it one pixel to the right.
// The binary embeds JetBrains Mono Regular only — a bold face would add ~250KB
// of font for one word per panel — and at 2x a one-pixel smear is what a bold
// weight looks like at this size.
func drawStringBold(img *image.NRGBA, face font.Face, s string, x, baseline int, c color.NRGBA) {
	drawString(img, face, s, x, baseline, c)
	drawString(img, face, s, x+1, baseline, c)
}

// fillRect paints r with c (draw.Src semantics). It writes the pixel bytes
// directly: draw.Draw has no fast path for a uniform source on an NRGBA
// destination and went pixel by pixel through interfaces, which was most of the
// time spent rendering a long capture.
func fillRect(img *image.NRGBA, r image.Rectangle, c color.NRGBA) {
	r = r.Intersect(img.Rect)
	if r.Empty() {
		return
	}
	w := r.Dx() * 4
	i0 := img.PixOffset(r.Min.X, r.Min.Y)
	row := img.Pix[i0 : i0+w : i0+w]
	row[0], row[1], row[2], row[3] = c.R, c.G, c.B, c.A
	for n := 4; n < w; n *= 2 {
		copy(row[n:], row[:n])
	}
	for y := r.Min.Y + 1; y < r.Max.Y; y++ {
		i := img.PixOffset(r.Min.X, y)
		copy(img.Pix[i:i+w], row)
	}
}

func drawRectOutline(img *image.NRGBA, r image.Rectangle, c color.NRGBA, thickness int) {
	fillRect(img, image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+thickness), c)
	fillRect(img, image.Rect(r.Min.X, r.Max.Y-thickness, r.Max.X, r.Max.Y), c)
	fillRect(img, image.Rect(r.Min.X, r.Min.Y, r.Min.X+thickness, r.Max.Y), c)
	fillRect(img, image.Rect(r.Max.X-thickness, r.Min.Y, r.Max.X, r.Max.Y), c)
}

// drawRoundedOutline strokes r with corners softened to radius, anti-aliased
// like the window chrome. Mark boxes use it so the evidence signal shares the
// window's own corner language instead of a square overlay.
func drawRoundedOutline(img *image.NRGBA, r image.Rectangle, radius int, c color.NRGBA, thickness int) {
	radius = min(radius, min(r.Dx(), r.Dy())/2)
	if radius <= 0 {
		drawRectOutline(img, r, c, thickness)
		return
	}
	fillRect(img, image.Rect(r.Min.X+radius, r.Min.Y, r.Max.X-radius, r.Min.Y+thickness), c)
	fillRect(img, image.Rect(r.Min.X+radius, r.Max.Y-thickness, r.Max.X-radius, r.Max.Y), c)
	fillRect(img, image.Rect(r.Min.X, r.Min.Y+radius, r.Min.X+thickness, r.Max.Y-radius), c)
	fillRect(img, image.Rect(r.Max.X-thickness, r.Min.Y+radius, r.Max.X, r.Max.Y-radius), c)

	// Same ring math as roundCorners: an anti-aliased annulus per corner.
	type corner struct {
		cx, cy float64
		x0, y0 int
	}
	left, right := float64(r.Min.X+radius)-0.5, float64(r.Max.X-radius)-0.5
	top, bottom := float64(r.Min.Y+radius)-0.5, float64(r.Max.Y-radius)-0.5
	for _, k := range []corner{
		{left, top, r.Min.X, r.Min.Y},
		{right, top, r.Max.X - radius, r.Min.Y},
		{left, bottom, r.Min.X, r.Max.Y - radius},
		{right, bottom, r.Max.X - radius, r.Max.Y - radius},
	} {
		for y := k.y0; y < k.y0+radius; y++ {
			for x := k.x0; x < k.x0+radius; x++ {
				dist := math.Hypot(float64(x)-k.cx, float64(y)-k.cy)
				ring := clamp01(float64(radius)-dist+0.5) - clamp01(float64(radius-thickness)-dist+0.5)
				if ring > 0 {
					img.SetNRGBA(x, y, mix(img.NRGBAAt(x, y), c, ring))
				}
			}
		}
	}
}

const (
	captionGap     = 15           // separates the annotation from the screenshot
	captionPadX    = chromeMargin // lines the annotation up with the window edge
	captionPadY    = 11
	captionBarW    = 4
	captionMinPad  = 28 // space kept to the right of the text
	captionLineMul = 1.45

	// The footnote is the tool's own voice about the crop: smaller, dimmer and
	// tighter than the analyst's caption, so the two are never mistaken.
	footnoteGap     = 10
	footnotePadY    = 6
	footnoteLineMul = 1.25
)

// warnf reports one caveat about the image, through the caller's channel when
// there is one.
func (cfg *Config) warnf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if cfg.Warn != nil {
		cfg.Warn(msg)
		return
	}
	fmt.Fprintf(os.Stderr, "taker: warning: %s\n", msg)
}

// reportUndrawable warns when the view contains characters the embedded font
// cannot draw. They come out as empty boxes, and a box where a value should be
// is the worst kind of evidence defect: it looks like a rendering quirk rather
// than a missing value, so a reader assumes the pixel is fine and moves on.
func reportUndrawable(cfg *Config, vlines []visualLine) {
	if cfg.noWarn {
		return
	}
	seen := map[rune]bool{}
	var missing []rune
	total := 0
	for _, vl := range vlines {
		if vl.isGap {
			continue
		}
		for _, tok := range vl.tokens {
			for _, r := range tok.Text {
				if unicode.IsSpace(r) || unicode.IsControl(r) || seen[r] {
					continue
				}
				if drawable, known := customfont.CanDraw(r); known && !drawable {
					seen[r] = true
					total++
					if len(missing) < 8 {
						missing = append(missing, r)
					}
				}
			}
		}
	}
	if total == 0 {
		return
	}
	shown := string(missing)
	if total > len(missing) {
		shown += "\u2026"
	}
	cfg.warnf("%d %s in %q have no glyph in the embedded font and render as empty boxes: %s",
		total, plural(total, "character", "characters"), cfg.Title, shown)
}

// marksOutsideView returns the marked lines the view does not contain, so a
// mark that fell out of --lines / --context / --nr / --ns can be reported
// instead of silently disappearing.
func marksOutsideView(marks highlight.Marks, vlines []visualLine) []int {
	visible := make(map[int]bool, len(vlines))
	for _, vl := range vlines {
		if !vl.isGap {
			visible[vl.lineNum] = true
		}
	}
	var absent []int
	for _, ln := range marks.Lines() {
		if !visible[ln] {
			absent = append(absent, ln)
		}
	}
	return absent
}

// compressRanges renders [4 9 10 11] as "4, 9-11": a list of 40 scattered line
// numbers is not a message anyone reads.
func compressRanges(nums []int) string {
	var parts []string
	for i := 0; i < len(nums); {
		j := i
		for j+1 < len(nums) && nums[j+1] == nums[j]+1 {
			j++
		}
		if j == i {
			parts = append(parts, strconv.Itoa(nums[i]))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", nums[i], nums[j]))
		}
		i = j + 1
	}
	return strings.Join(parts, ", ")
}

// plural picks the singular or plural form; it exists so the messages above
// read as sentences rather than as "%d line(s)".
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// stripSpec is the spacing of one annotation strip under the screenshot.
type stripSpec struct {
	gap, padY int
	lineMul   float64
}

var (
	footnoteStrip = stripSpec{footnoteGap, footnotePadY, footnoteLineMul}
	captionStrip  = stripSpec{captionGap, captionPadY, captionLineMul}
)

// stripTextX is where strip text starts: aligned with the caption's text
// rather than with the window edge. The two strips stack, and a note starting
// where the caption's bar is would read as a stray label rather than a second
// line.
const stripTextX = captionPadX + captionBarW + captionPadX

// layout wraps text for a canvas sw wide. It returns the lines, the canvas
// width they need and the height the strip adds, gap included; no lines and
// no height when there is nothing to say.
func (s stripSpec) layout(m *metrics, text string, sw int) ([]string, int, int) {
	if text == "" {
		return nil, sw, 0
	}
	lines := wrapWords(m.face, text, sw-stripTextX-captionMinPad)
	if len(lines) == 0 {
		return nil, sw, 0 // whitespace only: no strip, no empty band
	}
	if len(lines) == 1 {
		if w := stripTextX + textWidth(m.face, lines[0]) + captionMinPad; w > sw {
			sw = w
		}
	}
	return lines, sw, s.gap + s.padY*2 + len(lines)*m.lineHeightFor(s.lineMul)
}

// extendCanvas grows shot to w×(shot height+addH), with the new area painted
// bg. Only the uncovered strips are filled: the shot's own rows are copied in.
func extendCanvas(shot *image.NRGBA, w, addH int, bg color.NRGBA) *image.NRGBA {
	sb := shot.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	out := image.NewNRGBA(image.Rect(0, 0, w, sh+addH))
	for y := 0; y < sh; y++ {
		so := shot.PixOffset(sb.Min.X, sb.Min.Y+y)
		copy(out.Pix[y*out.Stride:y*out.Stride+sw*4], shot.Pix[so:so+sw*4])
	}
	fillRect(out, image.Rect(sw, 0, w, sh), bg)
	fillRect(out, image.Rect(0, sh, w, sh+addH), bg)
	return out
}

// withFootnote appends a muted line stating what the view leaves out. Trimmed
// evidence that does not say it was trimmed reads as the whole message: the
// releaser cannot tell a 9-line response from the first 9 lines of a 3000-line
// one, and an image that hides its own extent is not evidence, it is an
// assertion. Muted, and without the caption's accent bar: this is the tool
// describing the crop, not the analyst making a claim.
func withFootnote(shot *image.NRGBA, text string, ui UIColors, m *metrics) *image.NRGBA {
	lines, sw, h := footnoteStrip.layout(m, text, shot.Bounds().Dx())
	if lines == nil {
		return shot
	}
	out := extendCanvas(shot, sw, h, ui.ChromeBg)
	drawFootnote(out, shot.Bounds().Dy()+footnoteGap, lines, ui, m)
	return out
}

// drawFootnote writes the footnote lines into a strip starting at top.
func drawFootnote(out *image.NRGBA, top int, lines []string, ui UIColors, m *metrics) {
	lineH := m.lineHeightFor(footnoteLineMul)
	for i, line := range lines {
		y := top + footnotePadY + i*lineH
		drawString(out, m.face, line, stripTextX, m.baselineIn(y, lineH), ui.LineNumFg)
	}
}

// cropNote describes a view that does not hold every line of its message. It
// reports the range when the kept lines are contiguous and the bare count when
// they are not, because "lines 4-12 of 87" and "3 of 87 lines" are different
// claims and only one of them is true at a time. Empty when nothing was left
// out: a note on every image would be noise, and noise stops being read.
func cropNote(label string, shown []highlight.LineTokens, total int) string {
	ext := highlight.Extent(shown)
	if ext.Shown == 0 {
		return ""
	}

	// An excerpt (--first-line) leaves the file's length unknown. Nothing was
	// cropped out of what was rendered, but a reader still cannot tell an
	// excerpt from a whole file — the same question this note exists to answer.
	if total <= 0 {
		if ext.First <= 1 {
			return ""
		}
		return prefix(label, fmt.Sprintf("lines %d-%d of the excerpt", ext.First, ext.Last))
	}
	if ext.Shown == total {
		return ""
	}
	if ext.Last-ext.First+1 == ext.Shown {
		return prefix(label, fmt.Sprintf("lines %d-%d of %d", ext.First, ext.Last, total))
	}
	return prefix(label, fmt.Sprintf("%d of %d lines", ext.Shown, total))
}

func prefix(label, note string) string {
	if label == "" {
		return note
	}
	return label + ": " + note
}

// redactNote declares masked regions. Without it the image could pass for an
// unedited capture: the box alone is ambiguous — it might be content — so the
// footnote states the count the way it states a crop.
func redactNote(n int, asked bool) string {
	if n == 0 {
		if asked {
			return "0 regions redacted"
		}
		return ""
	}
	return fmt.Sprintf("%d %s redacted", n, plural(n, "region", "regions"))
}

// joinNotes renders the per-pane crop notes as one strip.
func joinNotes(notes ...string) string {
	var kept []string
	for _, n := range notes {
		if n != "" {
			kept = append(kept, n)
		}
	}
	return strings.Join(kept, "  \u00b7  ")
}

// withCaption appends a strip stating what the image proves. Evidence that
// travels into a report is read by someone who was not there: a red box says
// "look here", the caption says why. Drawn only when asked, so an unlabelled
// render still passes for a plain screenshot.
//
// It wraps to the screenshot's width rather than widening the image: a long
// finding description would otherwise stretch the PNG past the pane.
func withCaption(shot *image.NRGBA, text string, ui UIColors, m *metrics) *image.NRGBA {
	lines, sw, h := captionStrip.layout(m, text, shot.Bounds().Dx())
	if lines == nil {
		return shot
	}
	// ChromeBg, never the app's own palette: this line is the analyst talking,
	// and evidence must not imply the tool under test printed it.
	out := extendCanvas(shot, sw, h, ui.ChromeBg)
	drawCaption(out, shot.Bounds().Dy()+captionGap, h-captionGap, lines, ui, m)
	return out
}

// drawCaption writes the caption and its accent bar into a strip of height
// stripH starting at top.
func drawCaption(out *image.NRGBA, top, stripH int, lines []string, ui UIColors, m *metrics) {
	if len(lines) == 0 {
		return
	}
	fillRect(out, image.Rect(captionPadX, top+captionPadY, captionPadX+captionBarW, top+stripH-captionPadY),
		ui.HighlightBorder)
	lineH := m.lineHeightFor(captionLineMul)
	for i, line := range lines {
		y := top + captionPadY + i*lineH
		drawString(out, m.face, line, stripTextX, m.baselineIn(y, lineH), ui.TabText)
	}
}

// lineHeightFor is the row height this face needs at the given multiplier.
func (m *metrics) lineHeightFor(mul float64) int {
	return int(float64(m.ascent+m.descent) * mul)
}

// wrapWords breaks prose at spaces to fit maxWidth. Unlike the code wrapper it
// never splits mid-word: a caption is read, not inspected character by character.
func wrapWords(face font.Face, text string, maxWidth int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	if maxWidth <= 0 {
		return []string{strings.Join(words, " ")}
	}

	var lines []string
	current := words[0]
	for _, w := range words[1:] {
		if textWidth(face, current+" "+w) <= maxWidth {
			current += " " + w
			continue
		}
		lines = append(lines, current)
		current = w
	}
	return append(lines, current)
}
