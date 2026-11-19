//go:build unix

package proctree

import (
	"os/exec"
	"syscall"
)

type Tree struct{ cmd *exec.Cmd }

func Start(cmd *exec.Cmd) (*Tree, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Tree{cmd: cmd}, nil
}

func (t *Tree) Terminate() { _ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGTERM) }

func (t *Tree) Kill() { _ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL) }
