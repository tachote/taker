package render

import (
	"image"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// cachedFace memoises rasterised glyphs. opentype.Face rasterises a glyph's
// outline on every Glyph call, and evidence repeats the same few dozen
// characters thousands of times, so without a cache most of the text cost is
// redrawing an "e" it already drew.
//
// Entries are keyed by rune and the sub-pixel phase of the pen, because that
// phase changes the anti-aliasing. The pixel offset is applied on the way out.
type cachedFace struct {
	font.Face
	mu     sync.Mutex
	glyphs map[glyphKey]glyphEntry
}

type glyphKey struct {
	r      rune
	fx, fy fixed.Int26_6 // sub-pixel phase, 0..63
}

type glyphEntry struct {
	dr      image.Rectangle // relative to the pen's integer position
	mask    *image.Alpha    // private copy; the face reuses its own buffer
	maskp   image.Point
	advance fixed.Int26_6
	ok      bool
}

func newCachedFace(f font.Face) *cachedFace {
	return &cachedFace{Face: f, glyphs: make(map[glyphKey]glyphEntry)}
}

func (c *cachedFace) Glyph(dot fixed.Point26_6, r rune) (image.Rectangle, image.Image, image.Point, fixed.Int26_6, bool) {
	fx, fy := dot.X&63, dot.Y&63
	ix, iy := dot.X.Floor(), dot.Y.Floor()
	key := glyphKey{r, fx, fy}

	c.mu.Lock()
	defer c.mu.Unlock()
	e, hit := c.glyphs[key]
	if !hit {
		dr, mask, maskp, adv, ok := c.Face.Glyph(fixed.Point26_6{X: fx, Y: fy}, r)
		e = glyphEntry{dr: dr, maskp: image.Point{}, advance: adv, ok: ok}
		if !dr.Empty() && mask != nil {
			cp := image.NewAlpha(image.Rect(0, 0, dr.Dx(), dr.Dy()))
			for y := 0; y < dr.Dy(); y++ {
				for x := 0; x < dr.Dx(); x++ {
					_, _, _, a := mask.At(maskp.X+x, maskp.Y+y).RGBA()
					cp.Pix[y*cp.Stride+x] = uint8(a >> 8)
				}
			}
			e.mask = cp
		}
		c.glyphs[key] = e
	}
	var mask image.Image
	if e.mask != nil {
		mask = e.mask
	}
	return e.dr.Add(image.Pt(ix, iy)), mask, e.maskp, e.advance, e.ok
}
