package studio

import (
	"strings"
	"testing"

	"caffe/internal/service"
)

func TestTabsAndSplit(t *testing.T) {
	m := overviewModel(t)
	f := m.src.(*fakeSource)
	f.set(func(f *fakeSource) {
		f.files = map[string]string{"main:a.go": "package a\n", "main:b.go": "package b\n", "t2:c.go": "package c\n"}
	})
	m, _ = press(m, "5")
	open := func(ws, p string) {
		mm, c := m.openFile(ws, p)
		m = drive(t, mm, c)
	}
	open("main", "a.go")
	m.file.scroll = 0
	open("main", "b.go")
	if len(m.tabs) != 2 || m.file.path != "b.go" {
		t.Fatalf("tabs %d, active %s", len(m.tabs), m.file.path)
	}
	open("main", "a.go") // already open: activated, not duplicated
	if len(m.tabs) != 2 || m.file.path != "a.go" || m.tabs[0] != m.file {
		t.Fatalf("reopen: tabs %d, active %s", len(m.tabs), m.file.path)
	}
	m.ws.selected = service.Workspace{ID: "t2", Kind: "task", TaskKey: "impl", RunID: "0112dd6a-1"}
	open("t2", "c.go")
	m.ws.selected = service.Workspace{}
	v := plain(m.View().Content)
	if !strings.Contains(v, " a.go   b.go  [impl · run 0112dd6a:c.go]") {
		t.Errorf("tab bar (workspace shown when it differs):\n%s", v)
	}
	m, _ = press(m, "[")
	if m.file.path != "b.go" {
		t.Errorf("[ goes to %s", m.file.path)
	}
	m, _ = press(m, "|")
	v = plain(m.View().Content)
	if m.split == nil || m.split.path != "a.go" || !strings.Contains(v, "package a") || !strings.Contains(v, "package b") {
		t.Fatalf("split:\n%s", v)
	}
	m, _ = press(m, "\\")
	if m.file.path != "a.go" || m.split.path != "b.go" {
		t.Errorf("swap: active %s split %v", m.file.path, m.split)
	}
	m, _ = press(m, "x")
	if len(m.tabs) != 2 || m.file.path == "a.go" || m.split != nil && m.split.path == "a.go" {
		t.Errorf("close: tabs %d active %s", len(m.tabs), m.file.path)
	}
	m, _ = press(m, "x", "x")
	if m.file != nil || len(m.tabs) != 0 || m.scope() != "explorer" {
		t.Error("closing the last tab does not return to the explorer")
	}
}
