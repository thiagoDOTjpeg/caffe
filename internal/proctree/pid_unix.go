//go:build unix

package proctree

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
)

func Alive(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return false
	}

	if stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		if f := strings.Fields(string(stat)); len(f) > 2 && f[2] == "Z" {
			return false
		}
	}
	return true
}

func KillGroup(pid int) { _ = syscall.Kill(-pid, syscall.SIGKILL) }
