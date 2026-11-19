package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"caffe/internal/proctree/ptest"
)

func TestMain(m *testing.M) {
	if os.Getenv("CAFFE_SESSION_HELPER") == "1" {
		os.Exit(helper(os.Args[1:]))
	}
	os.Setenv("CAFFE_SESSION_HELPER", "1")
	os.Setenv("GORACE", strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
	os.Exit(m.Run())
}

func helper(args []string) int {
	switch args[0] {
	case "lines":
		n, _ := strconv.Atoi(args[1])
		code, _ := strconv.Atoi(args[2])
		for i := 1; i <= n; i++ {
			fmt.Printf("line-%d\n", i)
		}
		fmt.Fprintln(os.Stderr, "to-stderr")
		return code
	case "spawn":
		child := exec.Command(os.Args[0], "sleep")
		if err := child.Start(); err != nil {
			return 1
		}
		os.WriteFile(args[1], []byte(strconv.Itoa(child.Process.Pid)), 0o644)
		fallthrough
	case "sleep":
		time.Sleep(time.Minute)
	}
	return 2
}

func wait(t *testing.T, s *Session) Info {
	t.Helper()
	select {
	case <-s.Done():
	case <-time.After(20 * time.Second):
		t.Fatal("session did not end")
	}
	return s.Info()
}

func TestOutputExitCodeAndLog(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "sessions"))
	var notified atomic.Int32
	m.Notify = func(int) { notified.Add(1) }
	s := m.Start("command", "main", t.TempDir(), []string{os.Args[0], "lines", "3", "4"})
	info := wait(t, s)
	if info.State != Exited || info.Code != 4 || info.Err != nil || info.Ended.IsZero() {
		t.Fatalf("info: %+v", info)
	}
	got := strings.Join(s.Lines(), ",")
	if !strings.Contains(got, "line-1,line-2,line-3") || !strings.Contains(got, "to-stderr") {
		t.Fatalf("lines: %q", got)
	}
	log, err := os.ReadFile(info.LogPath)
	if err != nil || !strings.Contains(string(log), "line-3") {
		t.Fatalf("log %q: %q %v", info.LogPath, log, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for notified.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if notified.Load() == 0 {
		t.Fatal("never notified")
	}
	if m.Running() != 0 || !m.Remove(info.ID) || m.Get(info.ID) != nil {
		t.Fatal("remove after exit")
	}
}

func TestBufferIsBounded(t *testing.T) {
	m := NewManager(t.TempDir())
	s := m.Start("command", "main", t.TempDir(), []string{os.Args[0], "lines", strconv.Itoa(BufferLines + 500), "0"})
	wait(t, s)
	lines := s.Lines()
	if len(lines) != BufferLines {
		t.Fatalf("%d lines buffered", len(lines))
	}

	if lines[0] != "line-502" || lines[len(lines)-2] != fmt.Sprintf("line-%d", BufferLines+500) {
		t.Fatalf("first %q, second to last %q", lines[0], lines[len(lines)-2])
	}
}

func TestStartFailureIsASession(t *testing.T) {
	m := NewManager(t.TempDir())
	s := m.Start("command", "main", t.TempDir(), []string{filepath.Join(t.TempDir(), "no-such-binary")})
	info := wait(t, s)
	if info.State != Failed || info.Code != -1 || info.Err == nil {
		t.Fatalf("info: %+v", info)
	}
	if s := m.Start("command", "main", t.TempDir(), nil); wait(t, s).State != Failed {
		t.Fatal("empty argv started")
	}
}

func TestStopLeavesNoProcessBehind(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(t.TempDir())
	var pidFiles []string
	for i := range 2 {
		f := filepath.Join(dir, fmt.Sprintf("pid-%d", i))
		pidFiles = append(pidFiles, f)
		m.Start("command", "main", dir, []string{os.Args[0], "spawn", f})
	}
	var pids []int
	for _, f := range pidFiles {
		pids = append(pids, pidFrom(t, f))
	}
	if m.Running() != 2 {
		t.Fatalf("running: %d", m.Running())
	}
	if m.Remove(1) {
		t.Fatal("removed a running session")
	}
	m.Sessions()[0].Stop()
	if info := m.Sessions()[0].Info(); info.State != Stopped || info.Code != -1 {
		t.Fatalf("stopped: %+v", info)
	}
	m.StopAll()
	if m.Running() != 0 {
		t.Fatal("still running after StopAll")
	}
	for _, pid := range pids {
		deadline := time.Now().Add(5 * time.Second)
		for ptest.Alive(pid) {
			if time.Now().After(deadline) {
				ptest.Kill(pid)
				t.Fatalf("child %d survived", pid)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func pidFrom(t *testing.T, file string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(file); err == nil && len(b) > 0 {
			pid, err := strconv.Atoi(string(b))
			if err == nil {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("pid never written")
	return 0
}
