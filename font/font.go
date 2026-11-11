package font

import (
	_ "embed"
	"fmt"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

//go:embed JetBrainsMono-Regular.ttf
var JetBrainsMonoTTF []byte

// LoadFace parses the embedded JetBrains Mono TTF and returns a font.Face.
// DPI should be 144 for 2x (retina-quality) output.
func LoadFace(sizePt, dpi float64) (font.Face, error) {
	f, err := opentype.Parse(JetBrainsMonoTTF)
	if err != nil {
		return fallbackFace(sizePt, dpi)
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{
		Size:    sizePt,
		DPI:     dpi,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return fallbackFace(sizePt, dpi)
	}
	return face, nil
}

var (
	coverageOnce sync.Once
	coverageFont *sfnt.Font
)

// CanDraw reports whether the embedded code font has a glyph for r, and whether
// that could be established at all. A character with no glyph is drawn as an
// empty box: in an image whose whole purpose is to show a value, that is a value
// the reader cannot read, so the caller needs to be able to say so instead of
// publishing a screenshot that quietly lost it.
//
// The second return value is false only when the font cannot be parsed, in which
// case the fallback face is in use and coverage is not something this package
// can vouch for.
func CanDraw(r rune) (drawable, known bool) {
	coverageOnce.Do(func() {
		coverageFont, _ = opentype.Parse(JetBrainsMonoTTF)
	})
	if coverageFont == nil {
		return true, false
	}
	var buf sfnt.Buffer
	idx, err := coverageFont.GlyphIndex(&buf, r)
	// The sfnt package documents (0, nil) as "no glyph for r", and glyph 0 is
	// .notdef by convention — the empty box that actually gets drawn.
	return err == nil && idx != 0, true
}

func fallbackFace(sizePt, dpi float64) (font.Face, error) {
	f, err := opentype.Parse(gomono.TTF)
	if err != nil {
		return nil, fmt.Errorf("load fallback font: %w", err)
	}
	return opentype.NewFace(f, &opentype.FaceOptions{
		Size:    sizePt,
		DPI:     dpi,
		Hinting: font.HintingFull,
	})
}
