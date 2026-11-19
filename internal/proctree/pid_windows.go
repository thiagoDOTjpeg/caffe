//go:build windows

package proctree

import (
	"os/exec"
	"strconv"

	"golang.org/x/sys/windows"
)

const stillActive = 259

func Alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}

func KillGroup(pid int) { _ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run() }
