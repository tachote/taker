//go:build windows

package main

import "os/exec"

// Windows has no process groups — the direct kill still bounds wall time for
// a single-level child; grandchildren outliving the deadline is a documented
// platform gap.
func newChild(argv []string) *exec.Cmd {
	return exec.Command(argv[0], argv[1:]...)
}

func killGroup(c *exec.Cmd) {
	c.Process.Kill()
}
