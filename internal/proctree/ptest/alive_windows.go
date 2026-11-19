//go:build windows

package ptest

import (
	"golang.org/x/sys/windows"

	"caffe/internal/proctree"
)

func Alive(pid int) bool { return proctree.Alive(pid) }

func Kill(pid int) {
	if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid)); err == nil {
		windows.TerminateProcess(h, 1)
		windows.CloseHandle(h)
	}
}
