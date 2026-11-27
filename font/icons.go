package font

import (
	_ "embed"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
)

// SymbolsTTF is exported so tests can check glyph coverage with sfnt —
// font.Face.GlyphAdvance reports ok for a missing rune (it falls back to
// .notdef), so it cannot be used to verify a codepoint exists.
//
// The file is a subset of the Nerd Fonts release font holding only the
// codepoints the renderer draws (2.3 MB down to 33 KB of binary). Adding or
// changing an icon means re-running font/subset.sh against the full font;
// TestIconGlyphsExist fails until that is done.
//
//go:embed SymbolsNerdFontMono-Regular.ttf
var SymbolsTTF []byte

// LoadIconFace loads the Symbols Nerd Font Mono face for rendering file-type icons.
func LoadIconFace(sizePt, dpi float64) (font.Face, error) {
	f, err := opentype.Parse(SymbolsTTF)
	if err != nil {
		return nil, err
	}
	return opentype.NewFace(f, &opentype.FaceOptions{
		Size:    sizePt,
		DPI:     dpi,
		Hinting: font.HintingFull,
	})
}
