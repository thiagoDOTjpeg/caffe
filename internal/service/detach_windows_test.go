//go:build windows

package service

import (
	"fmt"
	"strings"
	"syscall"
	"testing"
)

var getConsoleWindow = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleWindow")

// detachEvidence runs in the launched child: whether it has a console.
func detachEvidence() string {
	hwnd, _, _ := getConsoleWindow.Call()
	return fmt.Sprintf("console=%d", hwnd)
}

// assertDetached: DETACHED_PROCESS starts the child without a console, so
// closing the caller's console window or Ctrl-C in it cannot reach it.
// Surviving an actually closed console window is checked by hand (W-04).
func assertDetached(t *testing.T, evidence string) {
	t.Helper()
	if strings.TrimSpace(evidence) != "console=0" {
		t.Errorf("child still has a console (%s): it would share the caller's console fate", evidence)
	}
}
