//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// newChild puts the command in its own process group so a forked sleeper is
// killed with it — CommandContext alone kills only the direct child and we
// would block reading a pipe the orphaned grandchild still holds open.
func newChild(argv []string) *exec.Cmd {
	c := exec.Command(argv[0], argv[1:]...)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return c
}

// killGroup SIGKILLs every process in the child's group, not just the child.
func killGroup(c *exec.Cmd) {
	syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
}
