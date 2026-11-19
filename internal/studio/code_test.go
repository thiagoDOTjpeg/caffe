package studio

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	c "caffe/internal/contracts"
	"caffe/internal/lsp"
	"caffe/internal/lsp/lsptest"
	"caffe/internal/service"
)

func TestHelperLSP(t *testing.T) {
	if mode := os.Getenv(lsptest.Env); mode != "" {
		lsptest.Serve(mode)
		os.Exit(0)
	}
}

func codeModel(t *testing.T) (model, *fakeSource) {
	t.Helper()
	t.Setenv(lsptest.Env, "ok")
	f := newFake()
	f.dir = t.TempDir()
	text := "package a\n\nfunc F() { x }\n\ntype T struct{}\n\nfunc (T) M() {}\n\nfunc G() {}\n"
	f.files = map[string]string{"t1:a.go": text}
	f.lists = map[string]*service.FileList{"t1": {Paths: []string{"a.go"}}}
	m := sized(newModel(f, Project{Name: "demo", Root: f.dir}, &crashGuard{}))
	m.ed.holder = "studio-test"
	m.code = newCode(lsp.ServerConfig{Language: "go", Command: []string{os.Args[0], "-test.run=^TestHelperLSP$"}, RootMarkers: []string{"go.mod"}, Extensions: []string{".go"}})
	t.Cleanup(func() { m.code.mgr.Shutdown(t.Context()) })
	m.screen = screenWorkspace
	m.ws.selected = service.Workspace{ID: "t1", Kind: "task", TaskKey: "impl", TaskStatus: c.TaskCompleted}
	m, cmd := m.openFile("t1", "a.go")
	m = until(t, m, tea.Batch(cmd, waitDiag(m.code.notes)), func(m model) bool { return len(m.fileDiags(m.file)) == 2 })
	return m, f
}

func TestDiagnosticsAreShownAndListed(t *testing.T) {
	m, _ := codeModel(t)
	v := plain(m.View().Content)
	if !strings.Contains(v, "1 error(s), 1 warning(s)") || !strings.Contains(v, "●") {
		t.Fatalf("view:\n%s", v)
	}
	m, _ = press(m, "!")
	if m.code.list == nil || len(m.code.list.items) != 2 || !strings.Contains(m.code.list.items[1].label, "3: error: undefined: x") {
		t.Fatalf("list %+v", m.code.list)
	}
	m, cmd := press(m, "j", "enter")
	m = drive(t, m, cmd)
	if m.code.list != nil || m.file.scroll != 0 {
		t.Fatalf("jump: list %v scroll %d", m.code.list, m.file.scroll)
	}
}

func TestSymbolsReferencesAndOutsideLocations(t *testing.T) {
	m, _ := codeModel(t)
	m, cmd := press(m, "S")
	m = drive(t, m, cmd)
	if m.code.list == nil || len(m.code.list.items) != 2 || !strings.Contains(m.code.list.items[1].label, "M") {
		t.Fatalf("symbols %+v", m.code.list)
	}
	m, cmd = press(m, "r")
	m = drive(t, m, cmd)
	l := m.code.list
	if l == nil || l.title != "References" || len(l.items) != 2 || !l.items[0].inside || l.items[1].inside || !strings.Contains(l.items[1].label, "outside the workspace") {
		t.Fatalf("references %+v", l)
	}
	m, _ = press(m, "j", "enter")
	if !strings.Contains(plain(m.View().Content), "outside the workspace; Studio only opens") {
		t.Fatalf("outside location opened:\n%s", plain(m.View().Content))
	}
	m, _ = press(m, "esc", "esc")
	m, cmd = press(m, "i")
	m = drive(t, m, cmd)
	m, cmd = press(m, "ctrl+]")
	m = drive(t, m, cmd)
	if r, _ := m.file.buf.Cursor(); r != 7 {
		t.Fatalf("definition moved the cursor to %d", r)
	}
}

func TestFailedTestLineFindsTheTest(t *testing.T) {
	if name, ok := failedTest("--- FAIL: TestHello/sub (0.00s)"); !ok || name != "TestHello" {
		t.Fatalf("%q %v", name, ok)
	}
	if _, ok := failedTest("ok  pkg 0.1s"); ok {
		t.Fatal("matched a passing line")
	}
}
