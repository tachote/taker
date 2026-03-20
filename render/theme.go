package render

import (
	"image/color"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/styles"
)

// UIColors holds all non-syntax UI element colors for a theme.
type UIColors struct {
	Bg              color.NRGBA
	TabBarBg        color.NRGBA
	ActiveTab       color.NRGBA
	TabBorder       color.NRGBA
	TabAccent       color.NRGBA
	TabText         color.NRGBA
	LineNumFg       color.NRGBA
	GutterSep       color.NRGBA
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

func builtinStyle(name string) *chroma.Style {
	s := styles.Get(name)
	if s == nil {
		s = styles.Fallback
	}
	return s
}

// ── Theme definitions ────────────────────────────────────────────────────────

var Themes = map[string]*Theme{
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

// ── File icon helpers ────────────────────────────────────────────────────────

// fileIconGlyph returns the Nerd Font codepoint for the file type icon.
func fileIconGlyph(filename string) rune {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".go"):
		return '\ue627' // nf-seti-go
	case strings.HasSuffix(lower, ".py"):
		return '\ue606' // nf-dev-python
	case strings.HasSuffix(lower, ".js"), strings.HasSuffix(lower, ".mjs"):
		return '\ue60c' // nf-dev-javascript_badge
	case strings.HasSuffix(lower, ".ts"), strings.HasSuffix(lower, ".tsx"):
		return '\ue628' // nf-seti-typescript
	case strings.HasSuffix(lower, ".jsx"):
		return '\ue61c' // nf-dev-react
	case strings.HasSuffix(lower, ".rs"):
		return '\ue7a8' // nf-dev-rust
	case strings.HasSuffix(lower, ".rb"):
		return '\ue739' // nf-dev-ruby
	case strings.HasSuffix(lower, ".java"):
		return '\ue738' // nf-dev-java
	case strings.HasSuffix(lower, ".php"):
		return '\ue73d' // nf-dev-php
	case strings.HasSuffix(lower, ".cs"):
		return '\ue77f' // nf-dev-visualstudio
	case strings.HasSuffix(lower, ".c"), strings.HasSuffix(lower, ".h"):
		return '\ue61e' // nf-dev-c
	case strings.HasSuffix(lower, ".cpp"), strings.HasSuffix(lower, ".hpp"):
		return '\ue61d' // nf-dev-cplusplus
	case strings.HasSuffix(lower, ".html"), strings.HasSuffix(lower, ".htm"):
		return '\ue60e' // nf-dev-html5
	case strings.HasSuffix(lower, ".css"):
		return '\ue60b' // nf-dev-css3
	case strings.HasSuffix(lower, ".scss"):
		return '\ue603' // nf-dev-sass
	case strings.HasSuffix(lower, ".json"):
		return '\ue60b'
	case strings.HasSuffix(lower, ".yaml"), strings.HasSuffix(lower, ".yml"):
		return '\ue6a8' // nf-seti-yml
	case strings.HasSuffix(lower, ".md"):
		return '\ue609' // nf-seti-markdown
	case strings.HasSuffix(lower, ".sh"), strings.HasSuffix(lower, ".bash"):
		return '\ue795' // nf-seti-shell
	case strings.HasSuffix(lower, ".sql"):
		return '\ue706' // nf-dev-mysql
	case strings.HasSuffix(lower, ".tf"), strings.HasSuffix(lower, ".hcl"):
		return '\ue61f'
	case strings.HasSuffix(lower, ".xml"):
		return '\ue619'
	default:
		return '\uf15b' // nf-fa-file
	}
}

// fileIconColor returns the Seti-style icon color by extension.
func fileIconColor(filename string) color.NRGBA {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".go"):
		return rgb(0x00, 0xad, 0xd8)
	case strings.HasSuffix(lower, ".py"):
		return rgb(0x3d, 0x79, 0xd8)
	case strings.HasSuffix(lower, ".js"), strings.HasSuffix(lower, ".mjs"):
		return rgb(0xf1, 0xbe, 0x42)
	case strings.HasSuffix(lower, ".ts"), strings.HasSuffix(lower, ".tsx"):
		return rgb(0x02, 0x7a, 0xcb)
	case strings.HasSuffix(lower, ".yml"), strings.HasSuffix(lower, ".yaml"):
		return rgb(0xcb, 0x17, 0x1e)
	case strings.HasSuffix(lower, ".json"):
		return rgb(0xf1, 0xbe, 0x42)
	case strings.HasSuffix(lower, ".sh"), strings.HasSuffix(lower, ".bash"):
		return rgb(0x4e, 0xaa, 0x25)
	case strings.HasSuffix(lower, ".java"):
		return rgb(0xb0, 0x70, 0x19)
	case strings.HasSuffix(lower, ".rb"):
		return rgb(0xcc, 0x34, 0x2d)
	case strings.HasSuffix(lower, ".rs"):
		return rgb(0xde, 0x80, 0x27)
	case strings.HasSuffix(lower, ".php"):
		return rgb(0x77, 0x7b, 0xb4)
	case strings.HasSuffix(lower, ".c"), strings.HasSuffix(lower, ".cpp"),
		strings.HasSuffix(lower, ".h"), strings.HasSuffix(lower, ".hpp"):
		return rgb(0x00, 0x5f, 0x87)
	case strings.HasSuffix(lower, ".cs"):
		return rgb(0x51, 0x8c, 0xff)
	case strings.HasSuffix(lower, ".xml"), strings.HasSuffix(lower, ".html"),
		strings.HasSuffix(lower, ".htm"):
		return rgb(0xe3, 0x7e, 0x33)
	case strings.HasSuffix(lower, ".css"), strings.HasSuffix(lower, ".scss"):
		return rgb(0x56, 0x3d, 0x7c)
	case strings.HasSuffix(lower, ".md"):
		return rgb(0x51, 0x9a, 0xba)
	case strings.HasSuffix(lower, ".sql"):
		return rgb(0xff, 0xd0, 0x00)
	case strings.HasSuffix(lower, ".tf"), strings.HasSuffix(lower, ".hcl"):
		return rgb(0x7b, 0x42, 0xbc)
	default:
		return rgb(0x89, 0x89, 0x89)
	}
}
