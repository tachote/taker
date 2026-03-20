package font

import (
	_ "embed"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
)

//go:embed SymbolsNerdFontMono-Regular.ttf
var symbolsTTF []byte

// LoadIconFace loads the Symbols Nerd Font Mono face for rendering file-type icons.
func LoadIconFace(sizePt, dpi float64) (font.Face, error) {
	f, err := opentype.Parse(symbolsTTF)
	if err != nil {
		return nil, err
	}
	return opentype.NewFace(f, &opentype.FaceOptions{
		Size:    sizePt,
		DPI:     dpi,
		Hinting: font.HintingFull,
	})
}
