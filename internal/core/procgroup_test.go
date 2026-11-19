package core

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"caffe/internal/proctree/ptest"
)

func grandchildPid(t *testing.T, pidFile string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(pidFile); err == nil && len(strings.TrimSpace(string(b))) > 0 {
			pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
			if err != nil {
				t.Fatal(err)
			}
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("grandchild pid never written")
	return 0
}

func expectDead(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for ptest.Alive(pid) {
		if time.Now().After(deadline) {
			ptest.Kill(pid) // do not leak it from the test itself
			t.Fatalf("grandchild %d survived", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A validation command that starts a child, like `go test` starting a test
// binary, must not leave it running: not on timeout, not on cancellation and
// not when the command exits normally. Same test on Unix and Windows.
func TestValidationCommandLeavesNoProcessesBehind(t *testing.T) {
	dir := t.TempDir()
	helper := func(args ...string) []string { return append([]string{os.Args[0]}, args...) }

	pidFile := filepath.Join(dir, "pid-timeout")
	start := time.Now()
	code, err := runCommand(context.Background(), 500*time.Millisecond, dir, helper("spawn", pidFile, "wait"), filepath.Join(dir, "timeout.log"))
	if code != -1 || err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("timeout not enforced: %s", time.Since(start))
	}
	expectDead(t, grandchildPid(t, pidFile))

	pidFile = filepath.Join(dir, "pid-exit")
	code, err = runCommand(context.Background(), time.Minute, dir, helper("spawn", pidFile, "exit"), filepath.Join(dir, "exit.log"))
	if code != 0 || err != nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
	expectDead(t, grandchildPid(t, pidFile))

	pidFile = filepath.Join(dir, "pid-cancel")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(500*time.Millisecond, cancel)
	if code, _ := runCommand(ctx, time.Minute, dir, helper("spawn", pidFile, "wait"), filepath.Join(dir, "cancel.log")); code != -1 {
		t.Fatalf("cancelled command: code=%d", code)
	}
	expectDead(t, grandchildPid(t, pidFile))

	// Exit codes are still the command's own.
	if code, err := runCommand(context.Background(), time.Minute, dir, helper("fail"), filepath.Join(dir, "code.log")); code != 3 || err != nil {
		t.Fatalf("exit code: %d, %v", code, err)
	}
}
