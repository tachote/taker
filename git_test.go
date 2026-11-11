package main

import (
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

	for _, spec := range []string{"HEAD", "src/auth.py", ":src/auth.py", "HEAD:", "HEAD:no/such.go", "nope:src/auth.py"} {
		if _, _, err := readGitBlobIn(dir, spec); err == nil {
			t.Errorf("%q was accepted; it should fail", spec)
		} else if !strings.Contains(err.Error(), "--git") {
			t.Errorf("%q: error %q does not name the flag to fix", spec, err)
		}
	}
}
