package studio

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"caffe/internal/service"
	"caffe/internal/session"
)

// TestHelperCommand is the command the sessions of these tests run: the test
// binary itself, selected by -test.run, only when the environment says so.
func TestHelperCommand(t *testing.T) {
	switch os.Getenv("CAFFE_STUDIO_HELPER") {
	case "fail":
		fmt.Println("--- FAIL: TestHello (0.00s)")
		fmt.Println("    hello_test.go:12: got \"x\", want \"hello\"")
		fmt.Println("FAIL")
		os.Exit(1)
	case "ok":
		fmt.Println("ok")
		os.Exit(0)
	case "sleep":
		time.Sleep(time.Minute)
		os.Exit(0)
	}
}

func helperArgv(mode string) string {
	return os.Args[0] + " -test.run=^TestHelperCommand$ " + mode
}

func TestFindLink(t *testing.T) {
	cases := []struct {
		in   string
		want link
		ok   bool
	}{
		{"    hello_test.go:12: got \"x\"", link{"hello_test.go", 12, 0}, true},
		{"./internal/x/y.go:3:7: undefined: z", link{"./internal/x/y.go", 3, 7}, true},
		{"\t/home/me/p/main.go:42 +0x1d", link{"/home/me/p/main.go", 42, 0}, true},
		{`C:\work\p\main.go:9:2: err`, link{`C:\work\p\main.go`, 9, 2}, true},
		{"ok  	caffe/internal/session	0.046s", link{}, false},
		{"12:30:45 started", link{}, false},
	}
	for _, c := range cases {
		got, ok := findLink(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("findLink(%q) = %+v, %v; want %+v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestResolveLink(t *testing.T) {
	f := newFake()
	f.files = map[string]string{"main:pkg/hello_test.go": "x", "main:a/dup.go": "", "main:b/dup.go": ""}
	f.lists = map[string]*service.FileList{"main": {Paths: []string{"pkg/hello_test.go", "a/dup.go", "b/dup.go"}}}
	ctx := context.Background()
	if p, err := resolveLink(ctx, f, "main", link{path: "hello_test.go", line: 1}); err != nil || p != "pkg/hello_test.go" {
		t.Errorf("suffix match: %q %v", p, err)
	}
	if p, err := resolveLink(ctx, f, "main", link{path: "pkg/hello_test.go", line: 1}); err != nil || p != "pkg/hello_test.go" {
		t.Errorf("root relative: %q %v", p, err)
	}
	for _, bad := range []string{"dup.go", "../../etc/passwd", "/etc/passwd", "nope.go"} {
		if p, err := resolveLink(ctx, f, "main", link{path: bad, line: 1}); err == nil {
			t.Errorf("%s resolved to %q", bad, p)
		}
	}
}

// until runs cmd and what follows from it, as Bubble Tea does, until cond
// holds: sessions end on their own time, after drive's quiet period.
func until(t *testing.T, m model, cmd tea.Cmd, cond func(model) bool) model {
	t.Helper()
	msgs := make(chan tea.Msg, 256)
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			msg := c()
			if batch, ok := msg.(tea.BatchMsg); ok {
				for _, b := range batch {
					run(b)
				}
				return
			}
			if msg != nil {
				msgs <- msg
			}
		}()
	}
	run(cmd)
	deadline := time.After(20 * time.Second)
	for !cond(m) {
		select {
		case msg := <-msgs:
			res, more := m.Update(msg)
			m = res.(model)
			run(more)
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			t.Fatal("condition never held")
		}
	}
	return m
}

// ended: every session ended and n exist.
func ended(n int) func(model) bool {
	return func(m model) bool {
		all := m.terms.mgr.Sessions()
		return len(all) == n && m.terms.mgr.Running() == 0
	}
}

func terminalsModel(t *testing.T) (model, *fakeSource) {
	f := newFake()
	f.dir = t.TempDir()
	m := sized(newModel(f, Project{Name: "demo", Root: f.dir}, &crashGuard{}))
	m.terms = newTerminals(t.TempDir())
	t.Cleanup(m.terms.mgr.StopAll)
	m, _ = press(m, "6")
	return m, f
}

func TestCommandSessionShowsOutputAndOpensItsLinks(t *testing.T) {
	t.Setenv("CAFFE_STUDIO_HELPER", "fail")
	m, f := terminalsModel(t)
	f.files = map[string]string{"main:pkg/hello_test.go": strings.Repeat("line\n", 20)}
	f.lists = map[string]*service.FileList{"main": {Paths: []string{"pkg/hello_test.go"}}}

	m, _ = press(m, "n")
	if !m.terms.typing {
		t.Fatal("n did not open the command line")
	}
	m.terms.input = helperArgv("")
	m, cmd := press(m, "enter")
	m = until(t, m, cmd, ended(1))
	v := m.View().Content
	for _, want := range []string{"command", "exit 1", "--- FAIL: TestHello", "hello_test.go:12"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	m, _ = press(m, "enter") // focus the output: cursor on the last link
	if m.scope() != "termout" || !strings.Contains(m.View().Content, "enter opens hello_test.go:12") {
		t.Fatalf("scope %s, view:\n%s", m.scope(), m.View().Content)
	}
	m, cmd = press(m, "enter")
	m = drive(t, m, cmd)
	if m.screen != screenWorkspace || m.file == nil || m.file.path != "pkg/hello_test.go" || m.file.scroll != 9 {
		t.Fatalf("screen %d, file %+v", m.screen, m.file)
	}
}

func TestValidationStopsAtTheFirstFailure(t *testing.T) {
	m, f := terminalsModel(t)
	f.validation = []string{helperArgv(""), helperArgv(""), helperArgv("")}
	// The helper's mode comes from the environment, shared by every step.
	t.Setenv("CAFFE_STUDIO_HELPER", "fail")
	m, cmd := press(m, "v")
	m = until(t, m, cmd, ended(1))
	time.Sleep(100 * time.Millisecond) // a second step would have started by now
	m = drive(t, m, nil)
	if all := m.terms.mgr.Sessions(); len(all) != 1 || all[0].Info().Kind != "validation" || all[0].Info().Code != 1 {
		t.Fatalf("%d sessions after a failing first step", len(all))
	}

	t.Setenv("CAFFE_STUDIO_HELPER", "ok")
	m, cmd = press(m, "v")
	m = until(t, m, cmd, ended(4)) // 1 + every step of the second run
	if s := m.terms.mgr.Sessions()[3].Info(); s.Code != 0 {
		t.Fatalf("last step: %+v", s)
	}
}

func TestQuitAsksWhileSessionsRunAndStopsThem(t *testing.T) {
	t.Setenv("CAFFE_STUDIO_HELPER", "sleep")
	m, _ := terminalsModel(t)
	m, _ = press(m, "n")
	m.terms.input = helperArgv("")
	m, cmd := press(m, "enter")
	m = until(t, m, cmd, func(m model) bool { return m.terms.mgr.Running() == 1 })
	if m.terms.mgr.Running() != 1 {
		t.Fatal("session not running")
	}
	m, cmd = press(m, "ctrl+q")
	if quits(cmd) || m.overlay != overlayConfirm || !strings.Contains(m.View().Content, "1 terminal session(s) still running") {
		t.Fatalf("quit did not ask:\n%s", m.View().Content)
	}
	// Closing a running session asks too, and stopping ends it.
	m, _ = press(m, "esc", "x")
	if m.overlay != overlayConfirm {
		t.Fatal("closing a running session did not ask")
	}
	m, _ = press(m, "esc", "c")
	m.terms.mgr.StopAll()
	if s := m.terms.mgr.Sessions()[0].Info(); s.State != session.Stopped {
		t.Fatalf("state %s", s.State)
	}
	if _, cmd = press(m, "ctrl+q"); !quits(cmd) {
		t.Error("quit with nothing running did not quit")
	}
}

func TestShellCommand(t *testing.T) {
	env := func(vals map[string]string) func(string) string { return func(k string) string { return vals[k] } }
	if c := shellCmd(env(map[string]string{"SHELL": "/bin/zsh"}), "linux", "/w"); c.Path != "/bin/zsh" || c.Dir != "/w" {
		t.Errorf("unix: %s in %s", c.Path, c.Dir)
	}
	if c := shellCmd(env(nil), "linux", "/w"); c.Args[0] != "sh" {
		t.Errorf("unix default: %v", c.Args)
	}
	if c := shellCmd(env(map[string]string{"SHELL": "/bin/zsh"}), "windows", `C:\w`); c.Args[0] != "cmd.exe" {
		t.Errorf("windows default: %v", c.Args)
	}
}

func TestPlainTextStripsEscapes(t *testing.T) {
	if got := plainText("\x1b[31mFAIL\x1b[0m\tx\a"); got != "FAIL    x" {
		t.Errorf("%q", got)
	}
}
