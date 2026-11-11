package render

import (
	"fmt"
	"image/color"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/styles"
)

// UIColors holds all non-syntax UI element colors for a theme.
type UIColors struct {
	Bg        color.NRGBA
	ChromeBg  color.NRGBA // margin around the window; derived from Bg if unset
	TabBarBg  color.NRGBA
	ActiveTab color.NRGBA
	TabBorder color.NRGBA
	TabAccent color.NRGBA
	TabText   color.NRGBA
	// ControlActive is the plate Burp fills behind an active toolbar toggle.
	ControlActive color.NRGBA
	LineNumFg     color.NRGBA
	GutterSep     color.NRGBA
	// Splitter and SplitterShade are the two hairline columns Burp draws between
	// the Request and Response panes: a light edge with a darker one behind it.
	// They are hairlines, not a filled band, and they run the full height of the
	// image, title strip and tab row included. Snap never uses them.
	Splitter        color.NRGBA
	SplitterShade   color.NRGBA
	GapFg           color.NRGBA
	GapBg           color.NRGBA
	HighlightBg     color.NRGBA
	HighlightBorder color.NRGBA
}

// Theme bundles a chroma syntax style with UI element colors.
type Theme struct {
	Name   string
	Chroma *chroma.Style
	UI     UIColors
	// HTTPOnly marks a palette built for one lexer. Offering it for code is a
	// trap: the style defines only the token types that lexer emits, so every
	// other language falls back to the plain text color and renders flat.
	HTTPOnly bool
}

func rgb(r, g, b uint8) color.NRGBA { return color.NRGBA{r, g, b, 0xff} }

// Standard red highlights work across all dark themes.
var redHighlight = struct{ bg, border color.NRGBA }{
	bg:     rgb(0x3d, 0x15, 0x15),
	border: rgb(0xf1, 0x4c, 0x4c),
}

// ── Chroma styles ────────────────────────────────────────────────────────────

// VSCodeDarkStyle is a custom chroma style matching VS Code Dark+.
var VSCodeDarkStyle = chroma.MustNewStyle("vscode-dark", chroma.StyleEntries{
	chroma.Background:            "bg:#1e1e1e #d4d4d4",
	chroma.Comment:               "#6a9955",
	chroma.CommentSingle:         "#6a9955",
	chroma.CommentMultiline:      "#6a9955",
	chroma.CommentHashbang:       "#6a9955",
	chroma.Keyword:               "#569cd6",
	chroma.KeywordDeclaration:    "#569cd6",
	chroma.KeywordType:           "#4ec9b0",
	chroma.KeywordNamespace:      "#c586c0",
	chroma.KeywordPseudo:         "#569cd6",
	chroma.KeywordReserved:       "#569cd6",
	chroma.KeywordConstant:       "#569cd6",
	chroma.NameFunction:          "#dcdcaa",
	chroma.NameFunctionMagic:     "#dcdcaa",
	chroma.NameClass:             "#4ec9b0",
	chroma.NameBuiltin:           "#4ec9b0",
	chroma.NameBuiltinPseudo:     "#9cdcfe",
	chroma.NameDecorator:         "#dcdcaa",
	chroma.NameAttribute:         "#9cdcfe",
	chroma.NameTag:               "#569cd6",
	chroma.NameVariable:          "#9cdcfe",
	chroma.NameVariableClass:     "#9cdcfe",
	chroma.NameVariableGlobal:    "#9cdcfe",
	chroma.NameVariableInstance:  "#9cdcfe",
	chroma.NameConstant:          "#4fc1ff",
	chroma.LiteralString:         "#ce9178",
	chroma.LiteralStringDouble:   "#ce9178",
	chroma.LiteralStringSingle:   "#ce9178",
	chroma.LiteralStringBacktick: "#ce9178",
	chroma.LiteralStringDoc:      "#ce9178",
	chroma.LiteralStringHeredoc:  "#ce9178",
	chroma.LiteralStringInterpol: "#ce9178",
	chroma.LiteralStringOther:    "#ce9178",
	chroma.LiteralStringRegex:    "#d16969",
	chroma.LiteralStringSymbol:   "#ce9178",
	chroma.LiteralNumber:         "#b5cea8",
	chroma.LiteralNumberFloat:    "#b5cea8",
	chroma.LiteralNumberHex:      "#b5cea8",
	chroma.LiteralNumberInteger:  "#b5cea8",
	chroma.LiteralNumberOct:      "#b5cea8",
	chroma.Operator:              "#d4d4d4",
	chroma.OperatorWord:          "#569cd6",
	chroma.Punctuation:           "#d4d4d4",
	chroma.GenericHeading:        "bold #569cd6",
	chroma.GenericSubheading:     "bold #4ec9b0",
	chroma.GenericEmph:           "italic",
	chroma.GenericStrong:         "bold",
})

// Burp's Pretty-view palette, taken from Burp Suite's own theme definition
// (burp/theme/DarkTheme.json inside the application bundle, section
// colors.ui.editor.message) rather than read off a screenshot. The screenshot is
// still the acceptance test, but the values come from the source: sampling
// anti-aliased text returns blends, and reading a colour off the wrong row is
// how an invented rule gets into a renderer and looks measured.
//
// Burp names these by role, and the names are the specification: a header's name
// and its value are different colours, a cookie's name is separated from its
// value, and cookie and form-field roles share the same pair.
var (
	burpLineColour  = rgb(0xd1, 0xe8, 0xf9) // httpFirstLine, headerName, operator, separator
	burpHeaderValue = rgb(0xba, 0xba, 0xba) // headerValue
	burpParamName   = rgb(0xbb, 0xcd, 0xff) // cookieName, paramName
	burpParamValue  = rgb(0xa5, 0xc3, 0x5b) // cookieValue, paramValue
	burpBodyText    = rgb(0xce, 0xce, 0xce) // text, tagDelimiter, literalQuote
	burpString      = rgb(0x93, 0xc7, 0x63) // literalString
	burpNumber      = rgb(0x79, 0xc1, 0xf4) // literalNumber
	burpLiteral     = rgb(0xff, 0x9e, 0x57) // literalBoolean, reservedWord, datatype
	burpNameTag     = rgb(0xe9, 0xc0, 0x63) // tagName, function, variable
	burpComment     = rgb(0xa3, 0xba, 0xba) // comment
)

// hex renders a colour for a chroma style entry, so the style and the recolour
// pass in http.go read their colours from the same four values.
func hex(c color.NRGBA) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

// BurpStyle mirrors Burp Suite's Pretty view: the header block is painted in
// two tones (name brighter than value) and only the body carries more colour.
// Token types are the ones chroma's http lexer actually emits — NameFunction
// for the method, Name/Literal for header name/value, and the delegated json
// lexer's NameTag for keys. The finer splits Burp shows inside a header value
// or a form body are not expressible here; http.go repaints those.
var BurpStyle = chroma.MustNewStyle("burp", chroma.StyleEntries{
	chroma.Background:      "bg:#1b1b1b " + hex(burpBodyText),
	chroma.Text:            hex(burpBodyText),
	chroma.NameFunction:    hex(burpLineColour), // request method
	chroma.NameNamespace:   hex(burpLineColour), // request path
	chroma.KeywordReserved: hex(burpLiteral),    // "HTTP"
	chroma.NameException:   hex(burpLineColour), // status text
	chroma.Operator:        hex(burpLineColour),
	chroma.Punctuation:     hex(burpLineColour),
	chroma.LiteralNumber:   hex(burpNumber),      // version, status code, json number
	chroma.Name:            hex(burpLineColour),  // header name
	chroma.Literal:         hex(burpHeaderValue), // header value
	// A json key is not a string to Burp: measured on a capture of its pretty
	// view, the key and its quotes came out gold while the string value came out
	// green, so tagName/variable is the role the key is wearing.
	chroma.NameTag:              hex(burpNameTag), // json key, xml tag
	chroma.NameAttribute:        hex(burpNameTag),
	chroma.LiteralStringDouble:  hex(burpString),
	chroma.LiteralString:        hex(burpString),
	chroma.LiteralNumberInteger: hex(burpNumber),
	chroma.LiteralNumberFloat:   hex(burpNumber),
	chroma.KeywordConstant:      hex(burpLiteral), // true / false / null
	chroma.Comment:              hex(burpComment),
})

func builtinStyle(name string) *chroma.Style {
	s := styles.Get(name)
	if s == nil {
		s = styles.Fallback
	}
	return s
}

// ── Theme definitions ────────────────────────────────────────────────────────

var Themes = map[string]*Theme{
	// Default for `taker http`: evidence that reads as a Burp screenshot.
	// The UI fields are reused by the Burp panel header — TabBarBg is the title
	// strip, ActiveTab the tab row, TabAccent the active tab's underline.
	"burp": {
		Name:     "Burp Suite (dark)",
		HTTPOnly: true,
		Chroma:   BurpStyle,
		UI: UIColors{
			Bg:              rgb(0x1b, 0x1b, 0x1b),
			ChromeBg:        rgb(0x0e, 0x0e, 0x0e),
			TabBarBg:        rgb(0x2b, 0x2b, 0x2b),
			ActiveTab:       rgb(0x2b, 0x2b, 0x2b), // the tab row shares the title strip
			TabBorder:       rgb(0x52, 0x52, 0x55),
			TabAccent:       rgb(0xcc, 0x68, 0x3d),
			TabText:         rgb(0xff, 0xff, 0xff),
			ControlActive:   rgb(0x37, 0x63, 0x99),
			LineNumFg:       rgb(0xa0, 0xa0, 0xa0),
			GutterSep:       rgb(0x55, 0x55, 0x55),
			Splitter:        rgb(0x52, 0x52, 0x55),
			SplitterShade:   rgb(0x3e, 0x3e, 0x40),
			GapFg:           rgb(0x81, 0x81, 0x81),
			GapBg:           rgb(0x24, 0x24, 0x24),
			HighlightBg:     redHighlight.bg,
			HighlightBorder: redHighlight.border,
		},
	},
	"vscode-dark": {
		Name:   "VS Code Dark+",
		Chroma: VSCodeDarkStyle,
		UI: UIColors{
			Bg:              rgb(0x1e, 0x1e, 0x1e),
			TabBarBg:        rgb(0x25, 0x25, 0x26),
			ActiveTab:       rgb(0x1e, 0x1e, 0x1e),
			TabBorder:       rgb(0x3c, 0x3c, 0x3c),
			TabAccent:       rgb(0x00, 0x78, 0xd4),
			TabText:         rgb(0xcc, 0xcc, 0xcc),
			LineNumFg:       rgb(0x85, 0x85, 0x85),
			GutterSep:       rgb(0x3c, 0x3c, 0x3c),
			Splitter:        rgb(0x3c, 0x3c, 0x3c),
			SplitterShade:   rgb(0x2a, 0x2a, 0x2a),
			GapFg:           rgb(0x4e, 0x4e, 0x4e),
			GapBg:           rgb(0x25, 0x25, 0x26),
			HighlightBg:     redHighlight.bg,
			HighlightBorder: redHighlight.border,
		},
	},
	"monokai": {
		Name:   "Monokai",
		Chroma: builtinStyle("monokai"),
		UI: UIColors{
			Bg:              rgb(0x27, 0x28, 0x22),
			TabBarBg:        rgb(0x1e, 0x1f, 0x1c),
			ActiveTab:       rgb(0x27, 0x28, 0x22),
			TabBorder:       rgb(0x3e, 0x3d, 0x32),
			TabAccent:       rgb(0xf9, 0x26, 0x72),
			TabText:         rgb(0xf8, 0xf8, 0xf2),
			LineNumFg:       rgb(0x75, 0x71, 0x5e),
			GutterSep:       rgb(0x3e, 0x3d, 0x32),
			GapFg:           rgb(0x49, 0x48, 0x3e),
			GapBg:           rgb(0x1e, 0x1f, 0x1c),
			HighlightBg:     redHighlight.bg,
			HighlightBorder: redHighlight.border,
		},
	},
	"dracula": {
		Name:   "Dracula",
		Chroma: builtinStyle("dracula"),
		UI: UIColors{
			Bg:              rgb(0x28, 0x2a, 0x36),
			TabBarBg:        rgb(0x21, 0x22, 0x2c),
			ActiveTab:       rgb(0x28, 0x2a, 0x36),
			TabBorder:       rgb(0x44, 0x47, 0x5a),
			TabAccent:       rgb(0xbd, 0x93, 0xf9),
			TabText:         rgb(0xf8, 0xf8, 0xf2),
			LineNumFg:       rgb(0x62, 0x72, 0xa4),
			GutterSep:       rgb(0x44, 0x47, 0x5a),
			GapFg:           rgb(0x44, 0x47, 0x5a),
			GapBg:           rgb(0x21, 0x22, 0x2c),
			HighlightBg:     redHighlight.bg,
			HighlightBorder: redHighlight.border,
		},
	},
	"github-dark": {
		Name:   "GitHub Dark",
		Chroma: builtinStyle("github-dark"),
		UI: UIColors{
			Bg:              rgb(0x0d, 0x11, 0x17),
			TabBarBg:        rgb(0x16, 0x1b, 0x22),
			ActiveTab:       rgb(0x0d, 0x11, 0x17),
			TabBorder:       rgb(0x30, 0x36, 0x3d),
			TabAccent:       rgb(0xf7, 0x81, 0x66),
			TabText:         rgb(0xc9, 0xd1, 0xd9),
			LineNumFg:       rgb(0x6e, 0x76, 0x81),
			GutterSep:       rgb(0x30, 0x36, 0x3d),
			GapFg:           rgb(0x30, 0x36, 0x3d),
			GapBg:           rgb(0x16, 0x1b, 0x22),
			HighlightBg:     redHighlight.bg,
			HighlightBorder: redHighlight.border,
		},
	},
	"nord": {
		Name:   "Nord",
		Chroma: builtinStyle("nord"),
		UI: UIColors{
			Bg:              rgb(0x2e, 0x34, 0x40),
			TabBarBg:        rgb(0x24, 0x29, 0x33),
			ActiveTab:       rgb(0x2e, 0x34, 0x40),
			TabBorder:       rgb(0x3b, 0x42, 0x52),
			TabAccent:       rgb(0x88, 0xc0, 0xd0),
			TabText:         rgb(0xec, 0xef, 0xf4),
			LineNumFg:       rgb(0x4c, 0x56, 0x6a),
			GutterSep:       rgb(0x3b, 0x42, 0x52),
			GapFg:           rgb(0x4c, 0x56, 0x6a),
			GapBg:           rgb(0x24, 0x29, 0x33),
			HighlightBg:     redHighlight.bg,
			HighlightBorder: redHighlight.border,
		},
	},
	"catppuccin-mocha": {
		Name:   "Catppuccin Mocha",
		Chroma: builtinStyle("catppuccin-mocha"),
		UI: UIColors{
			Bg:              rgb(0x1e, 0x1e, 0x2e),
			TabBarBg:        rgb(0x18, 0x18, 0x25),
			ActiveTab:       rgb(0x1e, 0x1e, 0x2e),
			TabBorder:       rgb(0x31, 0x32, 0x44),
			TabAccent:       rgb(0xcb, 0xa6, 0xf7),
			TabText:         rgb(0xcd, 0xd6, 0xf4),
			LineNumFg:       rgb(0x6c, 0x70, 0x86),
			GutterSep:       rgb(0x31, 0x32, 0x44),
			GapFg:           rgb(0x45, 0x47, 0x5a),
			GapBg:           rgb(0x18, 0x18, 0x25),
			HighlightBg:     redHighlight.bg,
			HighlightBorder: redHighlight.border,
		},
	},
	"gruvbox": {
		Name:   "Gruvbox",
		Chroma: builtinStyle("gruvbox"),
		UI: UIColors{
			Bg:              rgb(0x28, 0x28, 0x28),
			TabBarBg:        rgb(0x1d, 0x20, 0x21),
			ActiveTab:       rgb(0x28, 0x28, 0x28),
			TabBorder:       rgb(0x3c, 0x38, 0x36),
			TabAccent:       rgb(0xd7, 0x99, 0x21),
			TabText:         rgb(0xeb, 0xdb, 0xb2),
			LineNumFg:       rgb(0x92, 0x83, 0x74),
			GutterSep:       rgb(0x3c, 0x38, 0x36),
			GapFg:           rgb(0x50, 0x49, 0x45),
			GapBg:           rgb(0x1d, 0x20, 0x21),
			HighlightBg:     redHighlight.bg,
			HighlightBorder: redHighlight.border,
		},
	},
	"one-dark": {
		Name:   "One Dark",
		Chroma: builtinStyle("onedark"),
		UI: UIColors{
			Bg:              rgb(0x28, 0x2c, 0x34),
			TabBarBg:        rgb(0x21, 0x25, 0x2b),
			ActiveTab:       rgb(0x28, 0x2c, 0x34),
			TabBorder:       rgb(0x3e, 0x44, 0x51),
			TabAccent:       rgb(0x61, 0xaf, 0xef),
			TabText:         rgb(0xab, 0xb2, 0xbf),
			LineNumFg:       rgb(0x5c, 0x63, 0x70),
			GutterSep:       rgb(0x3e, 0x44, 0x51),
			GapFg:           rgb(0x3e, 0x44, 0x51),
			GapBg:           rgb(0x21, 0x25, 0x2b),
			HighlightBg:     redHighlight.bg,
			HighlightBorder: redHighlight.border,
		},
	},
	"solarized-dark": {
		Name:   "Solarized Dark",
		Chroma: builtinStyle("solarized-dark"),
		UI: UIColors{
			Bg:              rgb(0x00, 0x2b, 0x36),
			TabBarBg:        rgb(0x07, 0x36, 0x42),
			ActiveTab:       rgb(0x00, 0x2b, 0x36),
			TabBorder:       rgb(0x09, 0x4e, 0x5b),
			TabAccent:       rgb(0x26, 0x8b, 0xd2),
			TabText:         rgb(0x83, 0x94, 0x96),
			LineNumFg:       rgb(0x58, 0x6e, 0x75),
			GutterSep:       rgb(0x09, 0x4e, 0x5b),
			GapFg:           rgb(0x58, 0x6e, 0x75),
			GapBg:           rgb(0x07, 0x36, 0x42),
			HighlightBg:     redHighlight.bg,
			HighlightBorder: redHighlight.border,
		},
	},
	"rose-pine": {
		Name:   "Rosé Pine",
		Chroma: builtinStyle("rose-pine"),
		UI: UIColors{
			Bg:              rgb(0x19, 0x17, 0x24),
			TabBarBg:        rgb(0x1f, 0x1d, 0x2e),
			ActiveTab:       rgb(0x19, 0x17, 0x24),
			TabBorder:       rgb(0x26, 0x23, 0x3a),
			TabAccent:       rgb(0xc4, 0xa7, 0xe7),
			TabText:         rgb(0xe0, 0xde, 0xf4),
			LineNumFg:       rgb(0x6e, 0x6a, 0x86),
			GutterSep:       rgb(0x26, 0x23, 0x3a),
			GapFg:           rgb(0x40, 0x3d, 0x52),
			GapBg:           rgb(0x1f, 0x1d, 0x2e),
			HighlightBg:     redHighlight.bg,
			HighlightBorder: redHighlight.border,
		},
	},
}

// DefaultTheme is used when no --theme flag is provided.
var DefaultTheme = Themes["vscode-dark"]

// BurpTheme is the palette every HTTP capture uses. It is not selectable:
// evidence that claims to be a proxy capture has to look like one.
var BurpTheme = Themes["burp"]

// Derive the chrome margin from each theme's background instead of hand-picking
// ten more colors. The hairline border does the separating work.
func init() {
	for _, t := range Themes {
		if t.UI.ChromeBg == (color.NRGBA{}) {
			t.UI.ChromeBg = mix(t.UI.Bg, rgb(0x00, 0x00, 0x00), 0.45)
		}
	}
}
