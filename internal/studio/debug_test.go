package studio

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	c "caffe/internal/contracts"
	"caffe/internal/dap"
	"caffe/internal/dap/daptest"
	"caffe/internal/service"
)

func TestHelperDAP(t *testing.T) {
	if os.Getenv(daptest.Env) == "1" {
		daptest.Serve(os.Args)
		os.Exit(0)
	}
}

func TestEnclosingTest(t *testing.T) {
	lines := []string{"package a", "", "func TestA(t *testing.T) {", "\tx()", "}", "func helper() {}"}
	if n, ok := enclosingTest(lines, 3); !ok || n != "TestA" {
		t.Fatalf("%q %v", n, ok)
	}
	if _, ok := enclosingTest(lines, 1); ok {
		t.Fatal("found a test above the first one")
	}
}

func TestDebugATestFromItsFile(t *testing.T) {
	t.Setenv(daptest.Env, "1")
	f := newFake()
	f.dir = t.TempDir()
	f.files = map[string]string{"t1:hello_test.go": "package hello\n\nfunc TestHello(t *testing.T) {\n\tgot := greet(\"x\")\n\tif got != \"hello x\" {\n\t\tt.Fatal(got)\n\t}\n}\n"}
	f.lists = map[string]*service.FileList{"t1": {Paths: []string{"hello_test.go"}}}
	m := sized(newModel(f, Project{Name: "demo", Root: f.dir}, &crashGuard{}))
	m.dbg.cfg = dap.Config{Name: "fake", Command: []string{os.Args[0], "-test.run=^TestHelperDAP$", "--", "--client-addr", "{addr}"}}
	m.screen = screenWorkspace
	m.ws.selected = service.Workspace{ID: "t1", Kind: "task", TaskKey: "impl", TaskStatus: c.TaskCompleted}
	m, cmd := m.openFile("t1", "hello_test.go")
	m = drive(t, m, cmd)
	m, _ = press(m, "j", "j", "j", "b")
	if !m.hasBreakpoint(m.file.file.Path, 4) || !strings.Contains(plain(m.View().Content), "◆") {
		t.Fatalf("breakpoint: %v", m.dbg.bps)
	}
	t.Cleanup(func() {
		if m.dbg.sess != nil {
			m.dbg.sess.Disconnect(t.Context())
		}
	})
	d := newDriver()
	step := func(cond func(model) bool, keys ...string) {
		t.Helper()
		var cmd tea.Cmd
		m, cmd = press(m, keys...)
		d.run(cmd)
		m = d.until(t, m, cond)
	}
	step(func(m model) bool { return m.dbg.state == "paused" && len(m.dbg.vars) > 0 && len(m.dbg.source) > 0 }, "T")
	v := plain(m.View().Content)
	for _, want := range []string{"Debug · fake · TestHello · paused at hello_test.go:4", "main.greet", `n string = "from herd"`, "▶    4 │", "◆ hello_test.go:4"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	step(func(m model) bool { return len(m.dbg.vars) == 3 }, "v", "j", "enter")
	if !strings.Contains(plain(m.View().Content), "Port int = 8080") {
		t.Fatal("variable not expanded")
	}
	m, _ = press(m, "e")
	m = typeText(m, "len(n)")
	step(func(m model) bool { return strings.Contains(strings.Join(m.dbg.console, "\n"), "value of len(n)") }, "enter")
	step(func(m model) bool { return len(m.dbg.frames) > 0 && m.dbg.frames[0].Line == 5 }, "n")
	step(func(m model) bool { return m.dbg.state == "finished" && m.dbg.sess == nil }, "c")
	if v := plain(m.View().Content); !strings.Contains(v, "PASS") || !strings.Contains(v, "debug session ended") {
		t.Fatalf("end:\n%s", v)
	}
}

type driver struct{ msgs chan tea.Msg }

func newDriver() *driver { return &driver{msgs: make(chan tea.Msg, 256)} }

func (d *driver) run(c tea.Cmd) {
	if c == nil {
		return
	}
	go func() {
		msg := c()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, b := range batch {
				d.run(b)
			}
			return
		}
		if msg != nil {
			d.msgs <- msg
		}
	}()
}

func (d *driver) until(t *testing.T, m model, cond func(model) bool) model {
	t.Helper()
	deadline := time.After(20 * time.Second)
	for !cond(m) {
		select {
		case msg := <-d.msgs:
			res, more := m.Update(msg)
			m = res.(model)
			d.run(more)
		case <-deadline:
			t.Fatal("condition never held")
		}
	}
	return m
}
