#!/bin/sh
# subset.sh rebuilds the embedded Symbols Nerd Font from the full release font,
# keeping only the codepoints the renderer can draw. The full font is ~2.3 MB;
# the renderer uses about a hundred of its ten thousand glyphs.
#
# Usage: font/subset.sh /path/to/full/SymbolsNerdFontMono-Regular.ttf
#
# Run it after adding or changing an icon: the codepoints are every Private
# Use Area escape in render/*.go (non-test), which is how icons are written.
# TestIconGlyphsExist fails if one is missing from the embedded font, and the
# comparison test run at the end proves the subset draws each kept glyph
# exactly as the full font does. Needs fonttools (pyftsubset).
set -eu

full=${1:?usage: font/subset.sh /path/to/SymbolsNerdFontMono-Regular.ttf}
root=$(cd "$(dirname "$0")/.." && pwd)
out="$root/font/SymbolsNerdFontMono-Regular.ttf"
codes=$(mktemp)
trap 'rm -f "$codes"' EXIT

# \uXXXX and \UXXXXXXXX escapes in the PUA planes (U+E000-U+F8FF, U+F0000-).
for f in "$root"/render/*.go; do
	case $f in *_test.go) continue ;; esac
	grep -oE '\\u[0-9a-fA-F]{4}|\\U[0-9a-fA-F]{8}' "$f" || true
done | sed -E 's/^\\[uU]0*//' | tr 'a-f' 'A-F' | sort -u |
	awk '{ v = 0; for (i = 1; i <= length($0); i++) v = v * 16 + index("0123456789ABCDEF", substr($0, i, 1)) - 1
	       if ((v >= 57344 && v <= 63743) || v >= 983040) printf "U+%s\n", $0 }' >"$codes"

echo "subset.sh: keeping $(wc -l <"$codes" | tr -d ' ') codepoints"
# Names are kept whole: they carry the copyright and licence notices.
pyftsubset "$full" --unicodes-file="$codes" --layout-features='*' \
	--name-IDs='*' --name-legacy --name-languages='*' --output-file="$out"
ls -l "$out"

cd "$root"
TAKER_FULL_NERD_FONT="$full" go test ./render -run 'TestIconGlyphsExist|TestIconSubsetMatchesFullFont' -v
