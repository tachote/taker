//go:build windows

package main

import (
	"os"
	"os/exec"
)

// Windows has no process groups — the direct kill still bounds wall time for
// a single-level child; grandchildren outliving the deadline is a documented
// platform gap.
func newChild(argv []string) *exec.Cmd {
	return exec.Command(argv[0], argv[1:]...)
}

// killGroup terminates the child. It reports whether the kill took:
// TerminateProcess fails on a process that has already exited.
func killGroup(c *exec.Cmd) bool {
	return c.Process.Kill() == nil
}

// killedByDeadline says whether the deadline's kill ended the command. The
// wait status cannot tell: on Windows Exited() is always true and a
// terminated process ends with exit code 1, the same as a command that
// failed. The kill's own result is the tell — it fails on a process that was
// already gone.
func killedByDeadline(_ *os.ProcessState, killed bool) bool {
	return killed
}

// signalName is always "": a Windows process ends with an exit code, even
// when it was killed.
func signalName(*os.ProcessState) string { return "" }
