//go:build unix

package ptest

import (
	"syscall"

	"caffe/internal/proctree"
)

func Alive(pid int) bool { return proctree.Alive(pid) }

func Kill(pid int) { _ = syscall.Kill(pid, syscall.SIGKILL) }
