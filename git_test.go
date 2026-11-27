package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Evidence has to show the code of the revision being reported. Reading it out
// of the object store is what removes the transcription step, so this test
// guards the two things that make the feature usable: the bytes come from the
// revision asked for, and a bad rev or path fails loudly instead of rendering
// something else.
func TestReadGitBlob(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := "def login():\n    return 1\n"
	path := filepath.Join(dir, "src", "auth.py")
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-qm", "add auth")

	src, origin, err := readGitBlobIn(dir, "HEAD:src/auth.py")
	if err != nil {
		t.Fatalf("readGitBlobIn: %v", err)
	}
	if string(src) != want {
		t.Errorf("content = %q, want the bytes committed (%q)", src, want)
	}
	if origin.Kind != "git" {
		t.Errorf("kind = %q, want git", origin.Kind)
	}
	if origin.Path != "src/auth.py" {
		t.Errorf("path = %q, want the path inside the repo", origin.Path)
	}
	// The label travels in the image, so it has to name the file and a revision
	// a reader could check out.
	if !regexp.MustCompile(`^src/auth\.py @ [0-9a-f]{12}$`).MatchString(origin.Ref) {
		t.Errorf("ref = %q, want 'path @ short sha'", origin.Ref)
	}
	// The revision also travels as data, so a report can be checked against the
	// commit without reading the image.
	if !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(origin.Rev) {
		t.Errorf("rev = %q, want the short commit on its own", origin.Rev)
	}

	// A malformed spec is the caller's mistake (exit 2); a well-formed one git
	// cannot resolve is an input error (exit 3). A directory used to render
	// git's tree listing as a file named after it, at exit 0.
	for _, tc := range []struct {
		spec  string
		usage bool
	}{
		{"HEAD", true}, {"src/auth.py", true}, {":src/auth.py", true}, {"HEAD:", true},
		{"--output=/tmp/x:src/auth.py", true},
		{"HEAD:no/such.go", false}, {"nope:src/auth.py", false},
		{"HEAD:src", false}, {"HEAD:src/", false},
	} {
		_, _, err := readGitBlobIn(dir, tc.spec)
		switch {
		case err == nil:
			t.Errorf("%q was accepted; it should fail", tc.spec)
		case !strings.Contains(err.Error(), "--git"):
			t.Errorf("%q: error %q does not name the flag to fix", tc.spec, err)
		case errors.Is(err, errGitUsage) != tc.usage:
			t.Errorf("%q: usage error = %v, want %v (%v)", tc.spec, !tc.usage, tc.usage, err)
		}
	}
	if _, _, err := readGitBlobIn(dir, "HEAD:src"); err == nil || !strings.Contains(err.Error(), "tree (directory)") {
		t.Errorf("HEAD:src error = %v, want it to say the path is a directory", err)
	}

	// Through the CLI: the exit codes the README promises.
	for _, tc := range []struct {
		spec string
		want int
	}{{"HEAD", exitUsage}, {"HEAD:src", exitInput}, {"HEAD:src/auth.py", exitOK}} {
		code, stdout, _ := runTaker(t, dir, "--git", tc.spec, "--repo-dir", dir, "--dry-run", "--json")
		if code != tc.want {
			t.Errorf("taker --git %s = %d, want %d\n%s", tc.spec, code, tc.want, stdout)
		}
	}
}
