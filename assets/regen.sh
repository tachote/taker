#!/usr/bin/env bash
# regen.sh rewrites the README screenshots from the frozen fixtures in this
# directory (auth.py, upload.go, settings.py, request.http, response.http).
#
# Why fixtures and not the repo's own source: an image of render/render.go
# goes stale with the next commit, and then the README shows a command that
# no longer produces its picture. The fixtures only change together with the
# images, so each README command, its PNG, and the PNG's embedded provenance
# (taker:command, taker:version, taker:sha256) keep saying the same thing.
#
# Why run from this directory as plain `taker`: the PNG embeds argv as
# taker:command. A build path or a home directory in it would publish a local
# path, and the README command would not match what the image says produced
# it.
#
# Usage: assets/regen.sh            (from anywhere in the repo)
#        VERSION=v0.4.0 assets/regen.sh
# VERSION defaults to `git describe`, so a dirty tree is stamped as dirty.
set -euo pipefail

assets="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(dirname "$assets")"

version="${VERSION:-$(git -C "$root" describe --tags --always --dirty)}"

bin="$(mktemp -d)"
trap 'rm -rf "$bin"' EXIT

# -trimpath keeps build paths out of the binary; -X stamps the version the
# PNGs record, the same flag GoReleaser uses.
(cd "$root" && CGO_ENABLED=0 go build -trimpath \
	-ldflags "-X main.version=$version" -o "$bin/taker" .)
export PATH="$bin:$PATH"

cd "$assets"

# taker warns before it overwrites a file, and shot treats any warning as a
# failure, so clear the old images first. git still has them if a shot fails.
rm -f example-highlight-context.png example-dracula.png example-catppuccin.png example-http.png

# shot runs one README command and refuses an image that came with a
# warning: a caveat on a README example means the picture does not show what
# the command promises, and nobody reads stderr once the PNG is committed.
shot() {
	# %q quotes each word, so the echoed line pastes back as the same argv.
	printf '+ taker'
	printf ' %q' "$@"
	printf '\n'
	local errlog
	errlog="$(mktemp)"
	if ! taker "$@" 2>"$errlog"; then
		cat "$errlog" >&2
		rm -f "$errlog"
		exit 1
	fi
	cat "$errlog" >&2
	if grep -q 'warning:' "$errlog"; then
		rm -f "$errlog"
		echo "regen.sh: the command above warned; fix the fixture or the command" >&2
		exit 1
	fi
	rm -f "$errlog"
}

# Keep these four lines identical to the commands shown in README.md.
shot auth.py -m "cursor.execute" -c 4 -o example-highlight-context.png
shot upload.go -m "io.ReadAll" -m "filepath.Join" -c 3 -T dracula -o example-dracula.png
shot settings.py -n "1-14" -m "DEBUG = True" -T catppuccin-mocha -o example-catppuccin.png
shot http -r request.http -s response.http --hr 7 --hs 7 -o example-http.png

echo "regen.sh: wrote 4 PNGs with taker $version"
