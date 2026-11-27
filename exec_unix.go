//go:build !windows

package main

import (
	"fmt"
	"os"
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
// It reports whether the signal was sent.
func killGroup(c *exec.Cmd) bool {
	return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) == nil
}

// killedByDeadline says whether the deadline's kill ended the command. The
// group can still be signalled after the command exited — a zombie, or a
// process it left behind — so the wait status decides: a command that exited
// on its own in the same instant keeps its exit status.
func killedByDeadline(ps *os.ProcessState, _ bool) bool {
	return !ps.Exited()
}

// signalNames are the signals a command under test is likely to die of,
// by the name a reader of the evidence knows them by.
var signalNames = map[syscall.Signal]string{
	syscall.SIGHUP: "SIGHUP", syscall.SIGINT: "SIGINT", syscall.SIGQUIT: "SIGQUIT",
	syscall.SIGILL: "SIGILL", syscall.SIGTRAP: "SIGTRAP", syscall.SIGABRT: "SIGABRT",
	syscall.SIGBUS: "SIGBUS", syscall.SIGFPE: "SIGFPE", syscall.SIGKILL: "SIGKILL",
	syscall.SIGUSR1: "SIGUSR1", syscall.SIGSEGV: "SIGSEGV", syscall.SIGUSR2: "SIGUSR2",
	syscall.SIGPIPE: "SIGPIPE", syscall.SIGALRM: "SIGALRM", syscall.SIGTERM: "SIGTERM",
	syscall.SIGXCPU: "SIGXCPU", syscall.SIGXFSZ: "SIGXFSZ", syscall.SIGSYS: "SIGSYS",
}

// signalName is the signal that ended the process, or "" when it exited.
func signalName(ps *os.ProcessState) string {
	ws, ok := ps.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		return ""
	}
	if name, ok := signalNames[ws.Signal()]; ok {
		return name
	}
	return fmt.Sprintf("signal %d", int(ws.Signal()))
}
