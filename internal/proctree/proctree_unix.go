//go:build unix

// Package proctree runs a command together with every process it starts, so
// the whole tree can be stopped at once: a process group on Unix, a
// kill-on-close job object on Windows.
package proctree

import (
	"os/exec"
	"syscall"
)

// Tree is a command and every process it starts: on Unix, a process group
// led by the command.
type Tree struct{ cmd *exec.Cmd }

// Start starts cmd as the leader of a new process group; cancelling cmd's
// context kills the whole group instead of the leader only.
func Start(cmd *exec.Cmd) (*Tree, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Tree{cmd: cmd}, nil
}

// Terminate asks the whole group to stop (SIGTERM).
func (t *Tree) Terminate() { _ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGTERM) }

// Kill kills whatever is left of the group, also after the command exited
// (background children). Finding nothing (ESRCH) is the goal, not an error.
func (t *Tree) Kill() { _ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL) }
