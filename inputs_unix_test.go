//go:build unix

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Evidence is meant to be shared: os.CreateTemp's 0600 travelled through the
// rename onto the PNG. A new file now gets 0644 under the umask, and an
// overwritten one keeps the mode it had.
func TestWritePNGMode(t *testing.T) {
	old := syscall.Umask(0o022)
	defer syscall.Umask(old)
	dir := t.TempDir()
	file := writeInput(t, dir, "a.py", "x = 1\n")
	out := filepath.Join(dir, "new.png")
	snap := func() int {
		code, _, _ := runJSON(t, func(r *reporter) int {
			return runSnap(snapCmd(SnapCmd{File: file, Output: out, JSON: true}), r)
		})
		return code
	}
	if code := snap(); code != exitOK {
		t.Fatalf("run = %d", code)
	}
	if fi, err := os.Stat(out); err != nil || fi.Mode().Perm() != 0o644 {
		t.Fatalf("new PNG mode = %v (%v), want 0644 under umask 022", fi.Mode().Perm(), err)
	}
	if err := os.Chmod(out, 0o640); err != nil {
		t.Fatal(err)
	}
	if code := snap(); code != exitOK {
		t.Fatalf("overwrite = %d", code)
	}
	if fi, _ := os.Stat(out); fi.Mode().Perm() != 0o640 {
		t.Errorf("overwritten PNG mode = %v, want the 0640 it had", fi.Mode().Perm())
	}
}
