//go:build unix

// Package ptest has process helpers for tests.
package ptest

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// Alive reports whether pid is a running (not zombie) process.
func Alive(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return false
	}
	// Linux: a reparented, not-yet-reaped child is a zombie; it runs nothing.
	if stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		if f := strings.Fields(string(stat)); len(f) > 2 && f[2] == "Z" {
			return false
		}
	}
	return true
}

// Kill kills pid.
func Kill(pid int) { _ = syscall.Kill(pid, syscall.SIGKILL) }
