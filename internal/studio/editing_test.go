package studio

import (
	"fmt"
	"strings"
	"testing"

	c "caffe/internal/contracts"
	"caffe/internal/service"
)

func editModel(t *testing.T, ws string) (model, *fakeSource) {
	t.Helper()
	f := newFake()
	f.files = map[string]string{ws + ":a.go": "hello\nworld\n"}
	f.lists = map[string]*service.FileList{ws: {Paths: []string{"a.go"}}}
	m := sized(newModel(f, Project{Name: "demo", Root: "/p"}, &crashGuard{}))
	m.ed.holder = "studio-test"
	m.screen = screenWorkspace
	if ws != service.MainWorkspace {
		m.ws.selected = service.Workspace{ID: ws, Kind: "task", TaskKey: "impl", TaskStatus: c.TaskCompleted}
	}
	m, cmd := m.openFile(ws, "a.go")
	return drive(t, m, cmd), f
}

func typeText(m model, s string) model {
	for _, r := range s {
		m, _ = press(m, string(r))
	}
	return m
}

func TestEditSaveCommitAndRelease(t *testing.T) {
	m, f := editModel(t, "t1")
	m, cmd := press(m, "i")
	m = drive(t, m, cmd)
	if m.scope() != "editor" || m.leaseFor("t1") == nil || f.leases["t1"] != "studio-test" {
		t.Fatalf("scope %s, lease %v", m.scope(), m.leaseFor("t1"))
	}
	m, _ = press(m, "A")
	m = typeText(m, ", you")
	m, _ = press(m, "enter")
	m = typeText(m, "new")
	m, _ = press(m, "esc")
	if m.ed.insert || !m.file.buf.Dirty() || !strings.Contains(plain(m.View().Content), "● unsaved") {
		t.Fatalf("after insert: insert=%v view:\n%s", m.ed.insert, plain(m.View().Content))
	}
	if _, cmd := press(m, "ctrl+q"); quits(cmd) {
		t.Fatal("quit with unsaved changes")
	}
	m, _ = press(m, "u")
	if got := m.file.buf.Text(); got != "hello\nworld\n" {
		t.Fatalf("one undo removes the whole insert: %q", got)
	}
	m, _ = press(m, "ctrl+r")
	m, cmd = press(m, "ctrl+s")
	m = drive(t, m, cmd)
	if len(f.saves) != 1 || f.saves[0].Text != "hello, you\nnew\nworld\n" || f.saves[0].Lease.HolderID != "studio-test" {
		t.Fatalf("saves: %+v", f.saves)
	}
	if m.file.buf.Dirty() {
		t.Fatal("dirty after save")
	}
	m, cmd = press(m, "L")
	m = drive(t, m, cmd)
	if m.leaseFor("t1") == nil {
		t.Fatal("released with uncommitted changes")
	}
	m, _ = press(m, "C")
	m = typeText(m, "greet")
	m, cmd = press(m, "enter")
	m = drive(t, m, cmd)
	if len(f.commits) != 1 || f.commits[0] != "t1: manual: greet" {
		t.Fatalf("commits: %v", f.commits)
	}
	m, _ = press(m, "esc")
	m, cmd = press(m, "L")
	m = drive(t, m, cmd)
	if m.leaseFor("t1") != nil || len(f.leases) != 0 {
		t.Fatalf("lease kept: %v", f.leases)
	}
}

func TestSaveConflictAsksToOverwrite(t *testing.T) {
	m, f := editModel(t, "t1")
	m, cmd := press(m, "i")
	m = drive(t, m, cmd)
	f.set(func(f *fakeSource) { f.saveErr = fmt.Errorf("a.go changed on disk: %w", service.ErrConflict) })
	m, _ = press(m, "x")
	m, cmd = press(m, "ctrl+s")
	m = drive(t, m, cmd)
	if m.overlay != overlayConfirm {
		t.Fatal("a conflict did not ask")
	}
	m, cmd = press(m, "l", "enter")
	m = drive(t, m, cmd)
	if len(f.saves) != 2 || !f.saves[1].Overwrite || m.file.buf.Dirty() {
		t.Fatalf("saves %+v", f.saves)
	}
}

func TestEditingIsRefusedWhereCaffeMustNotWrite(t *testing.T) {
	m, _ := editModel(t, service.MainWorkspace)
	m, _ = press(m, "i")
	if m.scope() == "editor" || !strings.Contains(plain(m.View().Content), "main checkout is read-only") {
		t.Fatalf("main checkout editable:\n%s", plain(m.View().Content))
	}
	m, f := editModel(t, "t1")
	f.set(func(f *fakeSource) {
		f.leaseErr = fmt.Errorf("an agent is writing in /wt/t1 (task impl): %w", service.ErrLeaseHeld)
	})
	m, cmd := press(m, "i")
	m = drive(t, m, cmd)
	if m.scope() == "editor" || !strings.Contains(plain(m.View().Content), "an agent is writing") {
		t.Fatalf("edited over an agent:\n%s", plain(m.View().Content))
	}
}

func TestLeaveEditorAsksForUnsavedChanges(t *testing.T) {
	m, _ := editModel(t, "t1")
	m, cmd := press(m, "i")
	m = drive(t, m, cmd)
	m, _ = press(m, "D", "esc")
	if m.overlay != overlayConfirm {
		t.Fatal("left with unsaved changes without asking")
	}
	m, cmd = press(m, "l", "enter")
	m = drive(t, m, cmd)
	if m.file.buf != nil || m.scope() == "editor" {
		t.Fatal("still editing")
	}
	m, _ = press(m, "/")
	if m.ed.search != nil {
		t.Fatal("search opened outside the editor")
	}
}

func TestFindInBuffer(t *testing.T) {
	m, _ := editModel(t, "t1")
	m, cmd := press(m, "i")
	m = drive(t, m, cmd)
	m, _ = press(m, "/")
	m = typeText(m, "orl")
	m, _ = press(m, "enter")
	if r, col := m.file.buf.Cursor(); r != 1 || col != 1 {
		t.Fatalf("cursor %d:%d", r, col)
	}
}
