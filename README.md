# taker

A CLI tool for generating professional, publication-ready code, HTTP and terminal evidence screenshots for security audits, SAST pipelines, MCP servers, and AI agents.

Produces VS Code Dark+-styled PNG images with syntax highlighting, line numbers, file icons, and red highlight overlays — without requiring a browser, Electron, or any GUI dependency. Single self-contained binary with embedded fonts.

---

## Installation

### Download binary (recommended)

Download the latest release for your platform from the [releases page](https://github.com/tachote/taker/releases). taker produces audit evidence, so verify the archive against the release's `checksums.txt` before you install it:

```bash
# Pick the archive for your platform:
#   taker_darwin_arm64.tar.gz   macOS (Apple Silicon)
#   taker_darwin_amd64.tar.gz   macOS (Intel)
#   taker_linux_amd64.tar.gz    Linux (x86_64)
#   taker_linux_arm64.tar.gz    Linux (arm64)
#   taker_windows_amd64.zip     Windows (x86_64)
ASSET=taker_darwin_arm64.tar.gz
BASE=https://github.com/tachote/taker/releases/latest/download

# Each step runs only if the previous one succeeded: a failed download or a
# checksum mismatch stops the install before anything is extracted.
# Linux: use `sha256sum --ignore-missing -c checksums.txt` instead of shasum.
curl -fLO "$BASE/$ASSET" &&
  curl -fLO "$BASE/checksums.txt" &&
  shasum -a 256 --ignore-missing -c checksums.txt &&
  tar xzf "$ASSET" taker &&
  sudo mv taker /usr/local/bin/
```

If the check prints `FAILED`, do not install: delete the archive and download it again.

On Windows, compare the `Get-FileHash -Algorithm SHA256 taker_windows_amd64.zip` output with the `taker_windows_amd64.zip` line in `checksums.txt`. Extract `taker.exe` only if the two hashes are identical; if they differ, delete the zip and do not run anything from it.

### go install

```bash
go install github.com/tachote/taker@latest
```

Requires Go 1.26 or newer. An older `go` with the default `GOTOOLCHAIN=auto` downloads the Go 1.26.11 toolchain that `go.mod` names and builds with it.

### Build from source

```bash
git clone https://github.com/tachote/taker
cd taker
go build -o taker .
```

---

## Features

- **VS Code Dark+ aesthetics** — window chrome with rounded corners, tab bar, gutter, syntax highlighting
- **Brand-colored file icons** — 260+ extensions and filenames (`Dockerfile`, `go.mod`, `.env`, `pom.xml`), each a real Nerd Fonts logo in its language color
- **11 built-in themes** — VS Code Dark+, Dracula, Nord, Catppuccin Mocha, GitHub Dark, GitHub Light, Gruvbox, One Dark, Monokai, Solarized Dark, Rosé Pine
- **Window chrome and shadow** — `--chrome window` adds a title bar with traffic lights and the file name centred; `--shadow` lifts the pane off the canvas
- **Line selection** — render only the lines you need (`1,5,10-20,30-40`), with gap bands stating how many lines were skipped
- **Context mode** — automatically include N lines around highlighted lines (`--context 5`)
- **Point at the finding, not the line** — mark a column range (`-H "7:63-85"`), paste the value itself (`-m "sk_live_4xT9mK2pQrN8vW1"`), or match a shape (`--mark-re "eyJ[A-Za-z0-9_-]+"`)
- **Redaction that declares itself** — `--redact` hides secrets under a black box and the footnote says how many regions were masked; `--redact-keep 4` leaves `sk_l…████` legible. Redact literals are scrubbed from the report's `command` and the PNG's metadata too — a hidden value never travels verbatim in the same file's provenance
- **Captions** — `-L` adds a strip stating what the image proves, drawn outside the window so it never reads as something the tool under test printed
- **Word wrap** — wrap long lines with continuation indicators; wraps automatically rather than cropping
- **HTTP evidence mode** — side-by-side Request / Response pane that mimics Burp Suite's message editor, so evidence reads like a Burp screenshot; also single-panel (`-r` or `-s` alone) and combined captures (`-f`, Burp "Save item" format)
- **Terminal evidence mode** — `taker exec -- id` runs a command and renders the prompt plus output under a terminal window; `taker term` renders a saved transcript
- **Provenance built in** — every PNG embeds the command line, timestamp, version, input SHA-256 and source as text chunks (`tEXt`, or `iTXt` for a value that is not ASCII); `--json` reports the same fields
- **Preflight for agents** — `--dry-run` validates the whole render and reports the image's dimensions without writing it
- **Stdin support** — pipe code directly without creating a temp file; if nothing arrives on stdin within 300ms it fails as `usage` instead of hanging on an open pipe; a pipe closed with nothing in it fails as `input` (`stdin is empty`)
- **Self-contained binary** — JetBrains Mono and Symbols Nerd Font are embedded; no runtime dependencies

---

## Usage

### Code evidence

```
taker [file] [flags]
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--lang` | `-l` | auto | Language for syntax highlighting. A name taker does not know is warned, with the lexer used instead |
| `--lines` | `-n` | all | Lines to include, e.g. `1,5,10-20`. A range past the last line stops at it (`40-999999` means 40 to the end). Ignored, with a warning, when `--context` is set |
| `--first-line` | | 1 | Line number of the input's first line (use it when piping an excerpt) |
| `--git` | | — | Render the file `<rev>:<path>` out of the repository, e.g. `HEAD:src/auth/login.py` — the committed bytes, no textconv; a directory is refused (exit 3), a value without `<rev>:<path>` is a usage error (exit 2) |
| `--repo-dir` | | `.` | Repository directory for `--git` |
| `--highlight` | `-H` | — | Regions to highlight in red: `15`, `13,15`, `10-20`, or `15:20-45` for columns. Overlapping regions and marks draw as one box |
| `--mark` | `-m` | — | Highlight every occurrence of this text; a comma is part of it. Repeat `-m` for more |
| `--mark-re` | | — | Highlight every regex match. Repeatable; a comma splits the value in two (write `\,` to keep it), except inside `{}`, `[]` or `()` |
| `--max-marks` | | 0 (all) | Keep at most N matches per `--mark` literal, earliest first |
| `--redact` | | — | Hide every occurrence of this text under a black box. Repeatable; a comma splits the value in two (write `\,` to keep it). The value's parts between `-:=,/.;` and spaces (3 bytes or more) are also hidden where they stand as whole tokens, so a fragment a parser echoed is not left readable |
| `--redact-re` | | — | Hide every regex match under a black box. Repeatable; commas split as for `--mark-re` |
| `--redact-keep` | | 0 | Leave the first N characters of each `--redact` match visible (`4` → `sk_l…████`). A run where any match is N characters or shorter is refused (exit 2): that match would be drawn in full |
| `--label` | `-L` | — | Caption stating what the image proves |
| `--context` | `-c` | 0 | Include N context lines around each highlighted line |
| `--wrap` | `-w` | false | Wrap long lines at a column boundary |
| `--wrap-width` | | 100 | Column width when wrapping (implies `--wrap`; floor 20) |
| `--title` | `-t` | file name | Title shown in the tab bar |
| `--theme` | `-T` | `vscode-dark` | Color theme. `burp` is refused here (exit 2): it is for `taker http` only |
| `--chrome` | | `editor` | Window style: `editor` (bare pane) or `window` (title bar + traffic lights) |
| `--shadow` | | false | Drop shadow under the window |
| `--output` | `-o` | `output.png` | Output PNG path. Written atomically (temp file, then rename). A new file gets `0644` under your umask; an overwritten one keeps its mode. A directory that does not exist is a usage error (exit 2), and so is the input file itself, a symlink to it, or another spelling of its path |
| `--dry-run` | | | Validate and report without writing the PNG |
| `--list-themes` | | | Print available themes and exit |
| `--json` | | | Write a machine-readable report to stdout. Human messages stay on stderr |
| `--toon` | | | Same document in TOON — typically 25-40% fewer tokens than `--json` |

#### Examples

```bash
# Render a full file
taker auth.go -o evidence.png

# Highlight a specific vulnerability (line 42)
taker auth.py -H 42 -o evidence.png

# Box the secret itself — no column counting
taker auth.py -m "sk_live_4xT9mK2pQrN8vW1" -o evidence.png

# Or give the columns explicitly
taker auth.py -H "42:18-41" -o evidence.png

# Hide a live secret but keep the image honest: the box says where it was and
# the footnote declares the count
taker auth.py --redact "sk_live_4xT9mK2pQrN8vW1" --redact-keep 6 -o evidence.png

# Mark a pattern — every bearer-shaped token, not one pasted literal
taker auth.py --mark-re "Bearer [A-Za-z0-9_.-]+" -o evidence.png

# Caption it so the image explains itself in the report
taker auth.py -m "sk_live_4xT9mK2pQrN8vW1" \
  -L "SOLTAU-005 · client secret hardcoded in the mobile build" -o evidence.png

# Highlight with 5 lines of context — ideal for SAST/AI agents
taker auth.py -H 42 -c 5 -o evidence.png

# Render just the vulnerable function out of a 2000-line file, with the gutter
# still showing the real line numbers
sed -n '500,515p' auth.py \
  | taker --first-line 500 -m "sk_live_4xT9mK2pQrN8vW1" -c 3 -o evidence.png

# Render the code as it is in a revision, with no copy of it in between. The
# image carries the file and the commit it came from.
taker --git 9f344fad66cf0387436a3bf0628b44756ce7abfe:src/auth/login.py \
  -H 172 -c 6 -o evidence.png

# Non-contiguous lines with gap indicator
taker config.go -n "1-10,45-60" -H "52,57" -o evidence.png

# Wrap long lines (e.g. YAML with inline comments)
taker application.yml -H "13,15" -c 4 -w -o evidence.png

# From stdin
cat vuln.js | taker -l javascript -t "vuln.js" -H 8 -o evidence.png

# Dracula theme
taker secrets.py -H 15 -T dracula -o evidence.png
```

The four images below come from the fixtures in [`assets/`](assets/); `assets/regen.sh` runs these exact commands in that directory, so each command, its picture and the picture's embedded provenance agree.

**Highlight + context (VS Code Dark+):**

```bash
taker auth.py -m "cursor.execute" -c 4 -o example-highlight-context.png
```

![Highlight with context](assets/example-highlight-context.png)

**Dracula theme, two marks found by content:**

```bash
taker upload.go -m "io.ReadAll" -m "filepath.Join" -c 3 -T dracula -o example-dracula.png
```

![Dracula theme](assets/example-dracula.png)

**Catppuccin Mocha, line selection plus a content mark:**

```bash
taker settings.py -n "1-14" -m "DEBUG = True" -T catppuccin-mocha -o example-catppuccin.png
```

![Catppuccin Mocha](assets/example-catppuccin.png)

### HTTP evidence

```
taker http [-r <request-file>] [-s <response-file>] | -f <capture-file> [flags]
```

Renders a side-by-side Request / Response pane built to look like Burp Suite's message editor: title strip, `Pretty | Raw | Hex` view tabs with the active one underlined, dense line spacing, a gutter with no rule, and wrapped rows left unnumbered the way Burp does it. Headers are near-monochrome with only the JSON body colored, matching Burp's Pretty view.

| Flag | Description |
|------|-------------|
| `-r` | File containing the raw HTTP request |
| `-s` | File containing the raw HTTP response |
| `-f` | One file holding request **and** response (Burp "Save item"). Give either `-f` or `-r`/`-s`, not both (exit 2) |
| `--hr` | Regions to highlight in the request: `8-9`, or `8:20-45` for columns |
| `--hs` | Regions to highlight in the response: `4`, or `4:12-60` for columns |
| `--mark` / `-m` | Highlight every occurrence of this text in either panel; a comma is part of it. Repeat `-m` for more |
| `--mark-re` | Highlight every regex match in either panel. Repeatable; commas split as in code evidence |
| `--max-marks` | Keep at most N matches per `--mark` literal, earliest first |
| `--redact` | Hide every occurrence of this text under a black box. Repeatable; commas and parts as in code evidence |
| `--redact-re` | Hide every regex match under a black box. Repeatable |
| `--redact-keep` | Leave the first N characters of each `--redact` match visible; refused (exit 2) when a match is N characters or shorter |
| `--label` / `-L` | Caption stating what the image proves |
| `--nr` | Lines to include from the request, e.g. `1-20`; a range past the end stops at the last line |
| `--ns` | Lines to include from the response, e.g. `1-30` |
| `-c` | Include N context lines around each highlighted line; in a panel with highlights it overrides `--nr`/`--ns`, with a warning |
| `--wrap-width` | Columns per panel (default: 90, minimum 20) |
| `-o` | Output PNG path (default: `output.png`); never one of the input files (exit 2) |
| `--dry-run` | Validate and report without writing the PNG |
| `--json` | Machine-readable report on stdout; human messages stay on stderr |
| `--toon` | Same document in TOON — typically 25-40% fewer tokens than `--json` |

There is no `-T` here. HTTP evidence always renders in the Burp palette: a reader recognises a proxy capture at a glance, and a Dracula-tinted pane labelled *Request / Response / Pretty / Raw / Hex* is a screenshot of an application that does not exist.

`-c` only applies to a panel that has highlights, so a response-only finding still renders its request in full.

`-r` or `-s` may be given alone — a data leak is often the response by itself — and then the pane drops to a single panel with no splitter. `-f` reads one capture the way Burp's "Save item" writes it: request first, split on the response status line; a file that is not HTTP at all is an input error, and one with no response section renders the request with a warning. A trim or context crop that leaves out the request line (`POST /x HTTP/1.1`) or the response status line emits a warning, because that line names where the message went — the surface of the finding.

#### Examples

```bash
# Basic HTTP evidence
taker http -r request.http -s response.http -o evidence.png

# One Burp "Save item" file — no manual split into request/response
taker http -f capture.http -m "sessionid=42" -o evidence.png

# Response-only evidence (data leak)
taker http -s response.http --hs 7 -o evidence.png

# Mask the live bearer token in both panels; the footnote declares the count
taker http -r request.http -s response.http \
  --redact-re "eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+" -o evidence.png

# Highlight the credential leak line in request and the token in response
taker http -r request.http -s response.http --hr "7" --hs "7" -o evidence.png

# Wider panels for responses with long headers
taker http -r req.http -s res.http --wrap-width 110 -o evidence.png

# A long body: show 6 lines around the finding instead of all 3000
taker http -r req.http -s res.http --hs 1520 -c 6 -o evidence.png

# Response headers only
taker http -r req.http -s res.http --ns 1-30 -o evidence.png

# Box a value wherever it appears, in either panel, and caption the finding
taker http -r req.http -s res.http \
  -m "sk_live_4xT9mK2pQrN8vW1" -m "read:users write:admin" \
  -L "SOLTAU-005 · client_secret in the body; returned token grants write:admin" \
  -o evidence.png
```

**Credential leak — highlighted request line 7, response line 7:**

```bash
taker http -r request.http -s response.http --hr 7 --hs 7 -o example-http.png
```

![HTTP evidence](assets/example-http.png)

**Request file format** (`request.http`):
```
POST /oauth2/token HTTP/2
Host: auth.example.com
Content-Type: application/x-www-form-urlencoded

grant_type=client_credentials&client_secret=EXPOSED_SECRET
```

An HTTP/2 request copied in pseudo-header form (DevTools, HTTP/2 proxies),
with no request line, is accepted too: its first line is `:method:`,
`:path:`, `:authority:` or `:scheme:`.

**Response file format** (`response.http`):
```
HTTP/2 200 OK
Content-Type: application/json

{"access_token": "eyJ..."}
```

### Terminal evidence

```
taker exec [flags] -- <command> [args...]
taker exec [flags] --sh '<pipeline>'
taker term <transcript-file> [flags]
```

The canonical evidence of a command-injection finding is the command and its output — `$ id` answering `uid=33(www-data)`. `taker exec` runs the command itself, prepends the `$ prompt` line, and renders the transcript under a terminal title bar (no source-line gutter); `taker term` draws a transcript you already captured. Tabs expand to 8-column stops, as in a terminal, so a `ps` or `ls -l` table keeps its columns. Command lines are drawn with shell highlighting: the `$ ` in prompt green, then the command name, flags, strings, variables, operators and redirections in the theme's colours (the line `taker exec` ran, and every transcript line that starts with `$ ` in `taker term`). Only colours change; the text, marks and redaction boxes stay where they were.

**Put taker's flags before `--`.** Everything after `--` belongs to the command: `taker exec -- id -o rce.png` runs `id -o rce.png` and writes `output.png` (taker warns). Without `--`, one of exec's own flags inside the command (`taker exec id -o x.png`) is refused (exit 2) instead of guessed at, and an unknown flag in front of the command (`taker exec --bogus`) is a usage error that names it. The command's own flags (`head -n 2`, `grep -v`) are never taken for taker's.

The prompt line is the argv shell-quoted, so it pastes back as the same command (`$ grep -c 'func main' a.go`); `--sh` lines are drawn as typed. The exit status lands in the footnote (`exit 0`, `exit 1`, `killed by SIGSEGV`, `killed after Ns` when `--timeout` stopped it), so a non-zero result is documented, not hidden. The status is the command's own: a background process it leaves holding the output is given 2 more seconds, then killed with the command's process group, and the footnote adds `output cut 2s later`. Output past 10000 lines or 4 MiB cannot render under the 32000px cap, so `taker exec` kills the command and refuses (exit 4) instead of drawing a cut transcript; pipe it through `head`.

**`taker exec`**

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--sh` | | — | Run the string through `sh -c` (pipes, redirections, quoting); mutually exclusive with a command after `--` |
| `--timeout` | | 15 | Kill the command after this many seconds (1 to 9223372036) |
| `--output` | `-o` | `output.png` | Output PNG path |
| `--label` | `-L` | — | Caption stating what the image proves |
| `--mark` | `-m` | — | Highlight every occurrence of this text in the output; a comma is part of it. Repeat `-m` for more |
| `--mark-re` | | — | Highlight every regex match. Repeatable |
| `--max-marks` | | 0 (all) | Keep at most N matches per `--mark` literal |
| `--redact` / `--redact-re` / `--redact-keep` | | — | Mask the output the same way as code evidence; the masking also covers the prompt line and the recorded command. A needle that shell-quoting would split in the prompt line (one holding both `'` and one of `"`, `$`, a backtick, `\` or `!`) is refused (exit 2); use `--sh`. So is a `--redact-re` that matches a word of the command but not the drawn line `$ <command>`, such as one anchored with `^` or `$`; anchor on `\b` instead |
| `--wrap-width` | | 80 | Columns per row before output wraps (minimum 20) |
| `--theme` | `-T` | `vscode-dark` | Color theme (`burp` is refused) |
| `--title` | `-t` | `Terminal` | Title shown in the window bar |
| `--chrome` | | `window` | Window style: `window` (title bar, like a terminal) or `editor` |
| `--shadow` | | false | Drop shadow under the window |
| `--dry-run` | | | Run the command and validate without writing the PNG |
| `--json` | | | Machine-readable report on stdout |
| `--toon` | | | Same document in TOON — typically 25-40% fewer tokens than `--json` |

**`taker term`** takes the same flags except `--sh` and `--timeout`, and its `-o` must not name the transcript (exit 2). Neither mode has `--lines` or `--context`: trim the transcript itself (`head`, `tail`, `sed -n`) before you render it. A transcript too tall to render is refused with that recipe in the report's `help`.

```bash
# Run the proof command and keep its output, exit code included
taker exec -o rce.png -- id
taker exec --sh 'curl -s http://internal:8080/actuator/env | head' -o ssrf.png
taker exec --timeout 5 -o port-open.png -- nc -nv 10.0.0.9 443

# Render a transcript captured during the test window
taker term ./loot/nmap-scan.txt -m "open" -o ports.png
```

---

## Fidelity to Burp

`taker http` takes its colours from Burp Suite's own theme definition — `burp/theme/DarkTheme.json` inside the application bundle, section `colors.ui.editor.message`, where Burp names every role (`headerName`, `cookieValue`, `paramName`, `literalString`, `lineNumbers`, `gutterBorder`). Geometry (row pitch, header height, tab spacing) is measured off a real capture, and the capture is the acceptance test for both.

A structured body keeps the role colours Burp gives it: a json key comes out gold, its string value green, numbers and `true`/`false` their own colours, and the braces, colon and comma stay plain text — measured on a capture, not inferred from the role names.

Reading the palette from the source rather than from screenshot pixels is not pedantry: sampling anti-aliased text returns blends, and one earlier version of this palette carried a rule about "hex-looking" cookie values that came from reading a colour off the wrong row. Burp has a single `cookieValue` colour and it paints every cookie value with it.

Four things are deliberately **not** reproduced, because they are state rather than style:

| Not drawn | Why |
|---|---|
| The caret's current-line band | It marks where the cursor happened to be. Drawing it would claim a cursor position in an image that has no editor behind it. |
| The scrollbar track | Same reason: it reports a scroll offset that does not exist. |
| Non-printing-character dots | They encode invisible characters the analyst never chose to show. |
| The pane drag handle | Interactive chrome; it invites a click that cannot happen. |

Burp's panels are split evenly between request and response. `taker` sizes each pane to its content instead, because the empty half of an even split is not evidence — and an even split costs enough width that the default wrap drops from 90 to about 66 columns, wrapping long cookies and bodies further.

## Themes

```bash
taker --list-themes
```

| Key | Name |
|-----|------|
| `vscode-dark` *(default)* | VS Code Dark+ |
| `burp` | Burp Suite (dark) — used by `taker http` and **only** there: its style covers the HTTP lexer's tokens, so source code would render flat |
| `dracula` | Dracula |
| `nord` | Nord |
| `catppuccin-mocha` | Catppuccin Mocha |
| `github-dark` | GitHub Dark |
| `github-light` | GitHub Light — for reports and PDFs that sit on white |
| `gruvbox` | Gruvbox |
| `one-dark` | One Dark |
| `monokai` | Monokai |
| `solarized-dark` | Solarized Dark |
| `rose-pine` | Rosé Pine |

---

## Machine-readable report (`--json` / `--toon`)

An agent or a pipeline should not have to read the messages a human reads. With
`--json`, **stdout carries one JSON document** describing the run, while stderr
keeps the human lines. `--toon` writes the same document in
[TOON](https://toonformat.dev/) — identical information, typically 25-40% fewer
tokens than the indented JSON for an agent to read. Both examples below are real
output from the `assets/` fixtures; only `output` and `version` are
generalized.

```console
$ taker --git HEAD:assets/auth.py -m cursor.execute -c 4 --toon -o auth.png
```
```
tool: taker
version: 0.3.0
mode: snap
ok: true
exit: 0
output: /abs/path/auth.png
bytes: 129485
width: 1597
height: 964
command: "taker --git HEAD:assets/auth.py -m cursor.execute -c 4 --toon -o auth.png"
generated: "2026-11-27T22:25:23Z"
source:
  kind: git
  path: assets/auth.py
  rev: 45b60c58bb68
crop:
  first: 32
  last: 63
  shown: 15
  total: 63
sha256[1]{name,sum}:
  source,2866313c2bdc57d2fbb8be45f9bb84fc909c4caf4b64e5ced6bd34fdc83cd7f3
redactions: 0
marks[1]{needle,matches,lines,kept,dropped,shown}:
  cursor.execute,2,2,2,0,2
warnings[0]:
```

Without either flag a **successful** run leaves stdout empty — the PNG is the
artifact. A **failed** run always writes a document in the TOON shape above
(`ok: false`, `exit`, `error.kind`, `error.message`), so a caller that never
passed a format flag still gets something it can classify instead of grepping
stderr prose.

```console
$ taker auth.py -m cursor.execute --redact-re 'portal-[a-z-]+' -n 10-14,32-40 --json -o auth.png
```
```json
{
  "tool": "taker",
  "version": "0.3.0",
  "mode": "snap",
  "ok": true,
  "exit": 0,
  "output": "/abs/path/auth.png",
  "bytes": 88964,
  "width": 1421,
  "height": 915,
  "command": "taker auth.py -m cursor.execute --redact-re <redacted> -n 10-14,32-40 --json -o auth.png",
  "generated": "2026-11-27T22:25:36Z",
  "source": {
    "kind": "file",
    "path": "auth.py"
  },
  "crop": {
    "first": 10,
    "last": 40,
    "shown": 14,
    "total": 63
  },
  "sha256": [
    {
      "name": "source",
      "sum": "b637103c9df602a7eaf150faaaf0dff42601e52c273b6317806340a658a3621a"
    }
  ],
  "redactions": 1,
  "marks": [
    {
      "needle": "cursor.execute",
      "matches": 2,
      "lines": 2,
      "kept": 2,
      "dropped": 0,
      "shown": 1
    }
  ],
  "warnings": [
    "marked line 62 is not in the \"auth.py\" output"
  ]
}
```

Here the second `cursor.execute` (line 62) is outside `--lines`, so `kept` is 2
but `shown` is 1, and a warning says so. The report echoes `--mark`/`--mark-re`
needles verbatim — they are instructions to circle, not secrets — while
`--redact`/`--redact-re` values are masked everywhere, including inside
`warnings[]`, `command` and an error message (even one raised before the render
started). Do not pass a mark you would not want recorded.

A run that fails says so in the same document, so a consumer never has to branch
on "did anything come out of stdout":

```json
{
  "tool": "taker",
  "version": "0.3.0",
  "mode": "snap",
  "ok": false,
  "exit": 3,
  "command": "taker nope.py --json -o x.png",
  "generated": "2026-11-27T22:25:36Z",
  "redactions": 0,
  "warnings": [],
  "error": {
    "kind": "input",
    "message": "open nope.py: no such file or directory"
  }
}
```

### Fields

The keys always come in this order, in JSON and in TOON. A key marked
*omitted* is left out when it does not apply; it is never `null` or zero.

| Key | Meaning |
| --- | --- |
| `tool`, `version`, `mode`, `ok`, `exit` | Always present. `mode` is `snap`, `http`, `exec` or `term`. `exit` is the exit code below |
| `output`, `bytes`, `width`, `height` | The PNG as written, read back off disk. Omitted on a failure and on `--dry-run` |
| `command`, `generated` | The invocation and its UTC time |
| `dry_run` | `true` on `--dry-run`; omitted otherwise |
| `source` | Where the bytes came from: `kind` (`file`, `git`, `stdin`, `http`, `exec`, `transcript`), `path`, and `rev` — the short commit — for `--git` |
| `crop` | The extent the image shows (`snap`, `exec`, `term`) |
| `panels` | `taker http` only: one crop per pane, `request` and `response`. A pane that was not given is omitted |
| `sha256` | One `{name, sum}` per input: `source`, `request`, `response` or `transcript` |
| `redactions` | Always present: the black boxes the footnote declares |
| `marks` | One row per `--mark`/`--mark-re`: `needle`, `matches`, `lines`, `kept`, `dropped`, `shown` |
| `themes` | `--list-themes --json` only |
| `exec` | `taker exec` only: the command's `exit`, `timed_out` and `signal` |
| `warnings` | Always present, possibly empty |
| `help` | The next command to run; omitted when there is none |
| `error` | On a failure: `kind` and `message` |

**The fields describe what was written, not what was asked for.** `width`,
`height` and `bytes` are read back off the PNG. `crop` is the extent the image
shows, in the source's own numbering (`first`, `last`, `shown` lines, `total`),
and its `total` is `null` when the input did not start at line 1 — an excerpt's
real length is not something taker knows, and a wrong total is worse than none.
`marks` reports, per literal or pattern, over the whole input: how many matches
`--max-marks` kept (`kept`) and left out (`dropped`), and how many kept matches
sit on a line the image shows (`shown`) — a `--lines`/`--context`/`--nr`/`--ns`
trim can leave a kept match out of the view, so assert on `shown`, not `kept`,
to know a box is in the image. `command` is shell-quoted so it replays, and it
starts with the binary's base name instead of its path. `version` (and
`taker --version`) is the release tag for a release build, the module version
for `go install` or a `go build` in a git clone (a pseudo-version such as
`0.3.1-0.2026…-54baaba5771c`), `dev+<commit>` when only the commit is known,
and `dev` when nothing is. For `taker exec`, `exec` carries the child command's
outcome (`exit`, `timed_out`, and `signal` — `SIGKILL`, `SIGSEGV` — with `exit`
null when a signal ended it) — taker's own `exit` says the evidence was
produced, `exec.exit` says what the command answered. `warnings` holds the same
caveats the terminal shows — a mark that landed outside the view, a redaction
that matched nothing, a trim that cut the request line — so they survive a
consumer that never reads stderr. `--dry-run` runs the whole pipeline and
reports the same document, `crop` and `panels` included, with `dry_run: true`
and no `output`, for a preflight that never writes. `help` carries the next
command that unblocks or completes the run — widen the trim, fix the flag,
rerun without `--dry-run` — so a consumer never has to parse a manual to
recover.

`--list-themes --json` reports the palettes as data too, with `httpOnly` marking
the ones `taker snap`, `exec` and `term` refuse.

### PNG metadata

The PNG carries the same provenance as text chunks, so an image that travels
without the report still describes itself. A value that is plain ASCII is a
`tEXt` chunk; any other value (an accented caption, a non-ASCII path) is an
`iTXt` chunk, whose text is UTF-8, because `tEXt` is Latin-1 and would read
back garbled.

| Key | Value |
| --- | --- |
| `taker:version` | Same as the report's `version` |
| `taker:generated` | Same as `generated` |
| `taker:command` | Same as `command` |
| `taker:sha256` | The `source` sum; `taker:sha256:request`, `taker:sha256:response` and `taker:sha256:transcript` for the other inputs |
| `taker:source` | `<kind>:<path>`, for example `file:auth.py` or `http:request.http`. Absent for stdin and `taker exec` |
| `taker:git-revision` | The short commit, for `--git` only |
| `taker:exec` | `taker exec` only: the command line as the prompt shows it |

Every `--redact` literal, its parts and every `--redact-re` match are replaced
by `<redacted>` in these chunks, as in the report, because a masked value must
not leak through provenance. The `taker:sha256*` chunks are the one exception:
they are never scrubbed. Each sum is already computed over masked text, and
scrubbing it again could only cut holes in the hex (a `--redact-re '[0-9a-f]+'`
would wipe it) and break its agreement with the report.

### Reproducing a sha256

Each `sha256` covers the input as taker matched and drew it, in every mode, not
the raw bytes. A hash of the raw bytes would let anyone who holds the image
confirm guesses at a short secret it hides. To check a sum, apply the same two
steps:

1. **Normalize.** Strip ANSI escape sequences, compose each letter that is
   followed by combining marks (NFC on that sequence only, never on a letter
   NFC would replace, such as U+212A KELVIN SIGN), then remove the BOM,
   zero-width and other invisible format characters, and the combining marks
   left over. On a line where composing would move a `--redact`,
   `--redact-re`, `--mark` or `--mark-re` match (or a `--redact` fragment) off
   its text, the sequences under that match and the shown segment next to it
   are not composed; if that is not enough, the whole line is left
   uncomposed. Those marks are then removed, so an accent there is drawn as
   its base letter.
2. **Mask.** Replace with `<redacted>`, longest match first and left to right:
   every `--redact` literal, its double-quoted form (`"value"`, quotes
   included), each of its parts between `-:=,/.;` and spaces that is 3 bytes or
   more (anywhere, even inside a longer word — stricter than the image, which
   boxes parts only as whole tokens), and every `--redact-re` match.

So `sha256sum <file>` equals the reported sum only for a plain file with
nothing redacted. For the JSON example above, the regex matches only
`portal-dev-key`:

```console
$ sed -E 's/portal-[a-z-]+/<redacted>/g' auth.py | shasum -a 256
b637103c9df602a7eaf150faaaf0dff42601e52c273b6317806340a658a3621a  -
```

For `--git` the input is the committed blob (`git cat-file blob <rev>:<path>`),
and for `taker exec` it is the transcript as drawn, the `$ ` prompt line
included (`printf '$ echo hi\nhi\n' | shasum -a 256` for `taker exec -- echo hi`).

### Exit codes

The code is part of the contract: it says what to fix without parsing a word.

| Code | `error.kind` | Meaning |
| --- | --- | --- |
| `0` | — | The PNG is on disk (or, with `--dry-run`, it would have been) |
| `1` | `output` | The evidence could not be written: encoding, disk, permissions |
| `2` | `usage` | Usage: unknown flag, bad value, a combination that cannot work, an `-o` that names the input or a directory that does not exist |
| `3` | `input` | The input cannot be used as given: unreadable, empty, a directory, no such revision, not HTTP |
| `4` | `refused` | A documented limit refused the render: past 32000px tall, nothing left to show, `taker exec` output past its cap |

Failures are reported on **both** channels: the code, the human line on stderr,
and a document on stdout carrying `error.kind`, the message and any `help`
hints — in the requested format with `--json`/`--toon` (`--json=true` counts
too), in TOON without a flag. Parse errors get the same treatment: `taker http
--bogus` exits 2 with `mode: http`, `error.kind: usage`, and `help` naming
`taker http --help` and listing that mode's flags. A failed run never writes an
image.

## Behavior

Guards against shipping a screenshot that proves nothing:

- **No matching lines** → error, exit 4 (see Exit codes). `-n 500-600` on a 100-line file will not emit a blank PNG. Same for `--nr` / `--ns`.
- **Empty input** → error, exit 3. An empty or whitespace-only file, `--git` blob or stdin, request or response, or transcript is refused instead of rendered as a blank pane.
- **Body too long to look at** → a 3000-line response renders 129 000px tall, past the 32000px cap taker keeps under what browsers decode. `taker` refuses before allocating (which would cost about a gigabyte) and names the fix — `--context` or a line range for code and HTTP, a shorter transcript for `exec` and `term`; past 8000px it warns but still renders, because that call is yours.
- **Nothing highlighted** → warning on stderr. The image is still written, but you are told the `-H` / `--hr` / `--hs` lines are not in the output.
- **`-m` matched nothing** → warning naming the literal. Shipping a screenshot whose marker silently missed is the failure this tool exists to prevent.
- **`-m` matched a lot** → the count, on stderr, every run: `--mark "abc": 25 matches on 25 lines; --max-marks keeps a mark visible`. A literal that lands on 25 lines paints a ladder of 25 boxes and looks exactly as correct as one that lands on the vulnerable line, so the count is the only thing that tells you which image you are about to publish. `--max-marks N` keeps the first N and reports the ones it left out.
- **A trimmed or excerpted view says so, in the image.** `--context`, `--lines` / `--nr` / `--ns` and `--first-line` all narrow the view, and a narrowed view that does not declare itself reads as the whole file: a reader cannot tell a 9-line response from the first 9 lines of a 3000-line one. A muted strip under the panel states it — `lines 4-12 of 87`, `12 of 87 lines` when the kept lines are not contiguous, `lines 59-63 of the excerpt` when the file's length is unknown. The strip is absent when nothing was left out.
- **Content too wide** → wraps automatically at the widest column that fits and says so, instead of cropping the line. Use `--wrap-width` to pick the width yourself.
- **A mark that falls outside the view** → warning naming the lines, because an image with one box while the note says two lines are highlighted is worse than an unmarked one. `-H 2,4 -n 1-3` says `marked line 4 is not in the "auth.py" output`; when nothing is in view the older, blunter message fires instead.
- **Skipped lines** → the gap band states the count (`··· 37 lines skipped`), so a reader can tell how much was left out.
- **Overlapping highlights** → one box. `-H 1:10-16 -m sk_live` on the same value draws the union, not nested frames that read as two findings. The `marks` counts do not change.
- **Invisible characters** → before matching, taker strips ANSI escapes, composes each letter that carries combining marks (so a decomposed `José` keeps its accent; other codepoints, such as a KELVIN SIGN homoglyph, are drawn as they are) and removes the BOM, zero-width and other format characters that would split a needle, in files, stdin, HTTP messages and transcripts alike. `--redact` and `--mark` literals get the same treatment, so a needle holding one of those characters still matches; a literal made only of them is refused (exit 2). Each step that changed something is warned (`stripped 2 ANSI escape sequences before matching`, `removed 1 invisible/format characters ...`), and the `sha256` covers the normalized text.
- **Bytes that are not UTF-8** → drawn as U+FFFD and warned with their count and first line, because the image cannot show their values.
- **An `-o` that names the input** → refused (exit 2), including a symlink to it or another spelling of its path, instead of overwriting the evidence's own source.

---

## Integration

### MCP server (AI agents)

`taker` is designed to be called by MCP servers and AI agents as part of automated security reporting pipelines. The binary is fully autonomous — no dialogs, no GUI, no network access.

```python
import subprocess

def generate_evidence(file_path: str, highlight_lines: str, context: int = 5, output: str = "evidence.png") -> str:
    result = subprocess.run([
        "taker", file_path,
        "--highlight", highlight_lines,
        "--context", str(context),
        "--output", output,
    ], capture_output=True, text=True)
    if result.returncode != 0:
        raise RuntimeError(result.stderr)
    return output
```

### SAST pipeline

```yaml
# GitHub Actions example
- name: Generate vulnerability evidence
  run: |
    taker src/auth/login.py \
      --highlight "${{ env.VULN_LINE }}" \
      --context 5 \
      --theme github-dark \
      --output evidence/${{ env.FINDING_ID }}.png
```

### Fluid Attacks MCP integration

`taker` is the evidence generation backend for the [fluid-mcp](https://github.com/cvargas/fluid-mcp) server. The `generate_code_evidence` tool calls `taker` to produce screenshot evidence that is then uploaded to Fluid Attacks Integrates via GraphQL.

---

## How it works

```
Source file
    │
    ▼
highlight.Tokenize()          ← chroma lexer (200+ languages)
    │  syntax tokens with RGB colors
    ▼
highlight.FilterLines()       ← apply --lines spec, insert gap sentinels
    │
    ▼
render.buildVisualLines()     ← apply word wrap if --wrap
    │
    ▼
render.Render()               ← 4-pass image composition
    │   Pass 1: fill backgrounds (editor, tab bar, gutter)
    │   Pass 2: draw header (tab bar or HTTP panel header)
    │   Pass 3: draw visual lines (line numbers, tokens, highlights)
    │   Pass 4: draw highlight border
    ▼
png.Encode()                  ← output PNG at 144 DPI (2× retina quality)
```

For HTTP mode, `RenderHTTP()` runs two parallel render passes and composites the panels side by side with a vertical divider.

---

## Supported languages

Any language supported by [chroma](https://github.com/alecthomas/chroma) — 200+ lexers including Go, Python, JavaScript, TypeScript, Java, Rust, PHP, Ruby, C/C++, C#, YAML, JSON, SQL, Bash, HCL/Terraform, XML, HTML, and HTTP.

Language is auto-detected from the file extension (for `--git`, from the path in the revision). Override with `--lang`:

```bash
taker payload.txt --lang sql -H 3 -o evidence.png
```

A `--lang` that names no lexer is not an error, but it is warned, with the
language used instead (`--lang "pyhton" is not a language taker knows;
highlighted as plain text instead`). Bytes that are not UTF-8 draw as U+FFFD;
the run warns with their count and the first line that holds one (for `taker
http`, only in the start line and headers — the body has its own binary-body
warning).

---

## File type icons

Icons are rendered using [Symbols Nerd Font](https://www.nerdfonts.com/) embedded in the binary. Supported file types with distinct icons:

`.go` `.py` `.js` `.ts` `.tsx` `.jsx` `.rs` `.java` `.rb` `.php` `.cs` `.c` `.cpp` `.h` `.html` `.css` `.scss` `.yaml` `.yml` `.md` `.sh` `.sql` `.tf` `.hcl` `.xml`

---

## Dependencies

| Package | Purpose |
|---------|---------|
| [alecthomas/chroma](https://github.com/alecthomas/chroma) | Syntax highlighting (200+ languages) |
| [alecthomas/kong](https://github.com/alecthomas/kong) | CLI flag parsing |
| [golang.org/x/image](https://pkg.go.dev/golang.org/x/image) | Font rendering, image primitives |
| [golang.org/x/text](https://pkg.go.dev/golang.org/x/text) | NFC composition of input text |

**Embedded assets:**
- [JetBrains Mono](https://www.jetbrains.com/lp/mono/) — code font (SIL Open Font License 1.1)
- [Symbols Nerd Font](https://www.nerdfonts.com/) — file type icons (MIT License)

---

## License

MIT — see [LICENSE](LICENSE).

Third-party licenses:
- **JetBrains Mono**: [SIL Open Font License 1.1](https://scripts.sil.org/OFL)
- **Symbols Nerd Font**: [MIT License](https://github.com/ryanoasis/nerd-fonts/blob/master/LICENSE)
- **chroma**: [MIT License](https://github.com/alecthomas/chroma/blob/master/COPYING)
- **kong**: [MIT License](https://github.com/alecthomas/kong/blob/master/LICENSE)
