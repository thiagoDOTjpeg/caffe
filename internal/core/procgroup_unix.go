//go:build unix

package core

import (
	"os/exec"
	"syscall"
)

// procTree is a command and every process it starts: on Unix, a process
// group led by the command.
type procTree struct{ cmd *exec.Cmd }

// startTree starts cmd as the leader of a new process group; cancelling
// cmd's context kills the whole group instead of the leader only.
func startTree(cmd *exec.Cmd) (*procTree, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &procTree{cmd: cmd}, nil
}

// kill kills whatever is left of the group after the command exited
// (background children). Finding nothing (ESRCH) is the goal, not an error.
func (t *procTree) kill() { _ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL) }
