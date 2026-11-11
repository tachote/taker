package font

import "testing"

// A glyph the font does not have is drawn as an empty box. Anything that reports
// on that has to be right about which characters are covered, or the warning
// becomes noise the analyst learns to ignore.
func TestCanDraw(t *testing.T) {
	for _, r := range []rune{'a', 'Z', '0', 'é', 'ñ', '↩', '·'} {
		ok, known := CanDraw(r)
		if !known {
			t.Skipf("coverage unavailable for %q", r)
		}
		if !ok {
			t.Errorf("CanDraw(%q) = false; the code font should cover it", r)
		}
	}
	for _, r := range []rune{'☕', '日', '本', '😀'} {
		if ok, known := CanDraw(r); known && ok {
			t.Errorf("CanDraw(%q) = true; the embedded font does not carry it", r)
		}
	}
}
