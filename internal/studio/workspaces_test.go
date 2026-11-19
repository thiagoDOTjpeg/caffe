package studio

import (
	"strings"
	"testing"

	"caffe/internal/service"
)

func TestWorkspaceSelector(t *testing.T) {
	m := overviewModel(t)
	f := m.src.(*fakeSource)
	f.set(func(f *fakeSource) {
		f.ws = []service.Workspace{
			{ID: service.MainWorkspace, Kind: "main", Path: "/p", Branch: "main"},
			{ID: "t1", Kind: "task", Path: "/p/.caffe/worktrees/r/impl", Branch: "caffe/r/impl", RunID: "0112dd6a-1", TaskKey: "impl", Dirty: true},
			{ID: "t2", Kind: "task", RunID: "0112dd6a-1", TaskKey: "gone", Missing: true},
		}
	})
	if v := m.View().Content; !strings.Contains(v, "ws: main") {
		t.Fatal("status bar does not show the main workspace")
	}
	m, cmd := press(m, "ctrl+w")
	m = drive(t, m, cmd)
	v := m.View().Content
	if !strings.Contains(v, "uncommitted changes") || !strings.Contains(v, "removed") {
		t.Errorf("selector:\n%s", v)
	}
	m, _ = press(m, "j", "j", "enter") // the removed one cannot be selected
	if m.overlay != overlayWorkspaces || m.ws.selected.ID != "" {
		t.Fatal("a removed worktree was selected")
	}
	m, _ = press(m, "k", "enter")
	if m.overlay != overlayNone || m.ws.selected.ID != "t1" {
		t.Fatalf("selected %+v", m.ws.selected)
	}
	v = m.View().Content
	if !strings.Contains(v, "ws: impl · run 0112dd6a") || !strings.Contains(v, "── ws impl") {
		t.Errorf("header or status bar does not show the workspace:\n%s", v)
	}
	m, cmd = press(m, "space", "w", "s")
	m = drive(t, m, cmd)
	if m.ws.cursor != 1 {
		t.Error("the selector does not start on the selected workspace")
	}
	m, _ = press(m, "k", "enter")
	if m.ws.label() != "main" {
		t.Error("cannot go back to main")
	}
}
