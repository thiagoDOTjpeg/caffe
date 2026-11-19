//go:build windows

package service

import (
	"fmt"
	"strings"
	"syscall"
	"testing"
)

var getConsoleWindow = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleWindow")

func detachEvidence() string {
	hwnd, _, _ := getConsoleWindow.Call()
	return fmt.Sprintf("console=%d", hwnd)
}

func assertDetached(t *testing.T, evidence string) {
	t.Helper()
	if strings.TrimSpace(evidence) != "console=0" {
		t.Errorf("child still has a console (%s): it would share the caller's console fate", evidence)
	}
}
