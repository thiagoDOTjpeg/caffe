package studio

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

const (
	altScreenOn  = "\x1b[?1049h"
	altScreenOff = "\x1b[?1049l"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func runStudio(t *testing.T, ctx context.Context, in io.Reader) (*syncBuffer, error) {
	t.Helper()
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, nil, Project{Name: "demo", Root: "/p"}, Options{Input: in, Output: out, Dir: t.TempDir(), Log: zerolog.Nop()})
	}()
	select {
	case err := <-done:
		return out, err
	case <-time.After(10 * time.Second):
		t.Fatal("Studio did not exit")
		return nil, nil
	}
}

func expectRestored(t *testing.T, out string) {
	t.Helper()
	on, off := strings.Index(out, altScreenOn), strings.LastIndex(out, altScreenOff)
	if on < 0 || off < on {
		t.Errorf("alternate screen not entered and left (on at %d, off at %d)", on, off)
	}
}

func TestQuitRestoresTheTerminal(t *testing.T) {
	r, w := io.Pipe()
	go func() {
		time.Sleep(300 * time.Millisecond)
		w.Write([]byte{0x11})
	}()
	out, err := runStudio(t, context.Background(), r)
	if err != nil {
		t.Fatalf("quit: %v", err)
	}
	expectRestored(t, out.String())
	if !strings.Contains(out.String(), "demo") {
		t.Error("project name never rendered")
	}
}

func TestSignalStopsStudioCleanly(t *testing.T) {
	r, _ := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	out, err := runStudio(t, ctx, r)
	if err != nil {
		t.Fatalf("a signal is a normal way to stop, got %v", err)
	}
	expectRestored(t, out.String())
}

func TestCrashGuardWritesLogAndRepanics(t *testing.T) {
	g := &crashGuard{dir: t.TempDir(), log: zerolog.Nop()}
	func() {
		defer func() {
			if r := recover(); r != "boom" {
				t.Errorf("panic not re-raised: %v", r)
			}
		}()
		g.protect(func() { panic("boom") })
	}()
	if g.file == "" {
		t.Fatal("no crash log written")
	}
	body, err := os.ReadFile(g.file)
	if err != nil || !strings.Contains(string(body), "panic: boom") || !strings.Contains(string(body), "goroutine") {
		t.Errorf("crash log = %q, %v", body, err)
	}
	if filepath.Dir(g.file) != g.dir {
		t.Errorf("crash log written outside the studio directory: %s", g.file)
	}

	g2 := &crashGuard{dir: t.TempDir(), log: zerolog.Nop()}
	g2.protect(func() {})
	if g2.file != "" {
		t.Error("crash log written without a crash")
	}
}

func TestModelQuitKeys(t *testing.T) {
	m := newModel(nil, Project{Name: "demo"}, &crashGuard{log: zerolog.Nop()})
	for _, key := range []string{"ctrl+c", "ctrl+q"} {
		_, cmd := m.Update(keyPress(key))
		if cmd == nil {
			t.Errorf("%s does not quit", key)
		}
	}
}
