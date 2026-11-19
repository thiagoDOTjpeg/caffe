package studio

import (
	goruntime "runtime"
	"strings"
	"testing"

	c "caffe/internal/contracts"
	"caffe/internal/service"
)

func TestEditorCmd(t *testing.T) {
	cases := map[string]string{
		"":              "vi +12 /w/a.go",
		"nvim":          "nvim +12 /w/a.go",
		"code -w":       "code -w -g /w/a.go:12",
		"/usr/bin/hx":   "/usr/bin/hx /w/a.go:12",
		"emacs -nw":     "emacs -nw +12 /w/a.go",
		`C:\x\Code.exe`: `C:\x\Code.exe -g /w/a.go:12`,
	}
	if goruntime.GOOS == "windows" {
		cases[""] = "notepad /w/a.go"
	}
	for editor, want := range cases {
		env := func(k string) string {
			if k == "EDITOR" {
				return editor
			}
			return ""
		}
		if got := strings.Join(editorCmd(env, "/w/a.go", 12).Args, " "); got != want {
			t.Errorf("EDITOR=%q: %q, want %q", editor, got, want)
		}
	}
	visual := func(k string) string { return map[string]string{"VISUAL": "micro", "EDITOR": "nano"}[k] }
	if got := strings.Join(editorCmd(visual, "f", 1).Args, " "); got != "micro +1 f" {
		t.Errorf("VISUAL not preferred: %q", got)
	}
}

func TestEditHandOffReloadsFileAndTree(t *testing.T) {
	m := explorerModel(t, &service.FileList{Paths: []string{"cmd/app/main.go"}, Marks: map[string]string{}})
	p := &fakePauser{}
	m.watcher = p
	m, cmd := press(m, "enter", "j", "l", "j", "enter")
	m = drive(t, m, cmd)
	m, exec := press(m, "j", "e")
	if exec == nil || p.paused != 1 {
		t.Fatalf("no hand-off: exec %v paused %d", exec != nil, p.paused)
	}
	f := m.src.(*fakeSource)
	f.set(func(f *fakeSource) {
		f.files["main:cmd/app/main.go"] = "package edited\n"
		f.lists["main"] = &service.FileList{Paths: []string{"cmd/app/main.go"}, Marks: map[string]string{"cmd/app/main.go": service.MarkManual}}
	})
	res, after := m.Update(editorDoneMsg{})
	m = drive(t, res.(model), after)
	if p.resumed != 1 {
		t.Error("watcher not resumed")
	}
	v := plain(m.View().Content)
	if !strings.Contains(v, "package edited") || !strings.Contains(v, "●       main.go") {
		t.Errorf("not reloaded, or the tree lost its expansion:\n%s", v)
	}
	if m.scope() != "workspace" {
		t.Error("focus left the viewer")
	}

	m.ws.selected = service.Workspace{ID: "t1", Kind: "task", TaskKey: "impl", TaskStatus: c.TaskRunning}
	if m, exec = press(m, "e"); exec != nil || !strings.Contains(plain(m.View().Content), "an agent is writing") {
		t.Error("editing a running task's worktree was not refused")
	}
}
