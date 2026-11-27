package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// readmeInstallBlock returns the first ```bash block of the README's
// "Download binary" section: the install recipe users paste into a shell.
func readmeInstallBlock(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	i := strings.Index(s, "### Download binary")
	if i < 0 {
		t.Fatal(`README.md has no "### Download binary" section`)
	}
	s = s[i:]
	start := strings.Index(s, "```bash\n")
	if start < 0 {
		t.Fatal("Download binary section has no ```bash block")
	}
	s = s[start+len("```bash\n"):]
	end := strings.Index(s, "\n```")
	if end < 0 {
		t.Fatal("unterminated ```bash block in Download binary section")
	}
	return s[:end+1]
}

// TestREADMEInstallGatedOnChecksum pins that the checksum step in the
// README install recipe gates extraction and installation. taker produces
// audit evidence; a verification that only prints FAILED while the next
// line still installs the tampered binary protects nobody.
func TestREADMEInstallGatedOnChecksum(t *testing.T) {
	block := readmeInstallBlock(t)
	var cmds []string
	for _, l := range strings.Split(block, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		cmds = append(cmds, l)
	}
	joined := strings.Join(cmds, "\n")
	re := regexp.MustCompile(`(?m)^shasum -a 256 --ignore-missing -c checksums\.txt &&\n` +
		`tar xzf "\$ASSET" taker &&\n` +
		`sudo mv taker /usr/local/bin/$`)
	if !re.MatchString(joined) {
		t.Fatalf("README install block must chain checksum -> extract -> install with &&; got:\n%s", joined)
	}
	if strings.Contains(joined, ";") {
		t.Fatalf("README install block must not use ';' (it ignores failures):\n%s", joined)
	}
}

// TestREADMEInstallBlockRefusesTamperedArchive runs the README install
// block verbatim in bash, with curl and sudo replaced by local stubs, and
// checks that a tampered archive is never extracted or installed while a
// genuine one is.
func TestREADMEInstallBlockRefusesTamperedArchive(t *testing.T) {
	for _, tool := range []string{"bash", "shasum", "tar"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available: %v", tool, err)
		}
	}
	block := readmeInstallBlock(t)
	const asset = "taker_darwin_arm64.tar.gz"
	if !strings.Contains(block, "ASSET="+asset) {
		t.Fatalf("test assumes the README example uses ASSET=%s", asset)
	}

	genuine := tarGz(t, "taker", "GENUINE")
	sum := sha256.Sum256(genuine)
	checksums := hex.EncodeToString(sum[:]) + "  " + asset + "\n"

	for _, tc := range []struct {
		name    string
		archive []byte
		wantOK  bool
	}{
		{"genuine", genuine, true},
		{"tampered", tarGz(t, "taker", "EVIL"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			srv, stub, dest, cwd := filepath.Join(dir, "srv"), filepath.Join(dir, "stub"),
				filepath.Join(dir, "dest"), filepath.Join(dir, "cwd")
			for _, d := range []string{srv, stub, dest, cwd} {
				if err := os.Mkdir(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			writeFile(t, filepath.Join(srv, asset), tc.archive, 0o644)
			writeFile(t, filepath.Join(srv, "checksums.txt"), []byte(checksums), 0o644)
			// curl -fLO URL: copy the URL's basename from srv into cwd.
			writeFile(t, filepath.Join(stub, "curl"), []byte("#!/bin/bash\n"+
				`cp "$SRV/$(basename "${@: -1}")" .`+"\n"), 0o755)
			// sudo mv taker /usr/local/bin/: move into dest instead.
			writeFile(t, filepath.Join(stub, "sudo"), []byte("#!/bin/bash\n"+
				`if [ "$1" = mv ]; then exec mv "$2" "$DEST/"; fi`+"\n"+
				`exec "$@"`+"\n"), 0o755)

			cmd := exec.Command("bash", "-c", block)
			cmd.Dir = cwd
			cmd.Env = append(os.Environ(), "PATH="+stub+string(os.PathListSeparator)+os.Getenv("PATH"),
				"SRV="+srv, "DEST="+dest)
			out, err := cmd.CombinedOutput()

			installed, readErr := os.ReadFile(filepath.Join(dest, "taker"))
			_, extractErr := os.Stat(filepath.Join(cwd, "taker"))
			if tc.wantOK {
				if err != nil || readErr != nil || string(installed) != "GENUINE" {
					t.Fatalf("genuine archive not installed: err=%v installed=%q\n%s", err, installed, out)
				}
				return
			}
			if err == nil {
				t.Errorf("install block exited 0 on a checksum mismatch\n%s", out)
			}
			if readErr == nil {
				t.Errorf("tampered binary was installed: %q\n%s", installed, out)
			}
			if extractErr == nil {
				t.Errorf("tampered archive was extracted\n%s", out)
			}
		})
	}
}

func tarGz(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

// TestREADMEScreenshotCommandsMatchRegen pins the README's screenshot
// commands to the ones assets/regen.sh runs. The images embed the command
// that made them; a README that shows a different command (the repo's own
// source instead of the frozen fixture) promises a picture it cannot produce.
func TestREADMEScreenshotCommandsMatchRegen(t *testing.T) {
	regen, err := os.ReadFile(filepath.Join("assets", "regen.sh"))
	if err != nil {
		t.Fatal(err)
	}
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, l := range strings.Split(string(regen), "\n") {
		if !strings.HasPrefix(l, "shot ") {
			continue
		}
		n++
		want := "taker " + strings.TrimPrefix(l, "shot ")
		if !strings.Contains(string(readme), "\n"+want+"\n") {
			t.Errorf("README.md does not show the regen.sh command on a line of its own:\n%s", want)
		}
	}
	if n == 0 {
		t.Fatal("assets/regen.sh has no shot lines")
	}
}
