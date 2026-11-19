package studio

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"caffe/internal/service"
)

func explorerModel(t *testing.T, list *service.FileList) model {
	t.Helper()
	m := overviewModel(t)
	f := m.src.(*fakeSource)
	f.set(func(f *fakeSource) {
		f.lists = map[string]*service.FileList{"main": list}
		f.files = map[string]string{"main:cmd/app/main.go": goSource}
	})
	m, cmd := press(m, "5")
	return drive(t, m, cmd)
}

func TestExplorerTreeMarksAndOpen(t *testing.T) {
	m := explorerModel(t, &service.FileList{
		Paths: []string{"README.md", "cmd/app/main.go", "cmd/tool.go", "go.mod", "old.txt"},
		Marks: map[string]string{"cmd/app/main.go": service.MarkModified, "old.txt": service.MarkDeleted},
	})
	v := plain(m.View().Content)
	for _, want := range []string{"▸• ▸ cmd", "README.md", "D   old.txt"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "tool.go") {
		t.Error("a collapsed directory is drawn open")
	}
	m, _ = press(m, "enter", "j", "l") // open cmd, then cmd/app
	v = plain(m.View().Content)
	if !strings.Contains(v, "▾ cmd") || !strings.Contains(v, "M       main.go") || !strings.Contains(v, "tool.go") {
		t.Errorf("expanded:\n%s", v)
	}
	m, cmd := press(m, "j", "enter")
	m = drive(t, m, cmd)
	if m.file == nil || m.file.path != "cmd/app/main.go" || m.scope() != "workspace" {
		t.Fatalf("file not opened: %+v scope %s", m.file, m.scope())
	}
	if v := plain(m.View().Content); !strings.Contains(v, "4 │ func hello() {") {
		t.Errorf("viewer:\n%s", v)
	}
	m, _ = press(m, "h") // back to the tree, at main.go
	if m.scope() != "explorer" {
		t.Fatal("h does not return to the explorer")
	}
	m, _ = press(m, "h", "h") // to cmd/app, collapse it
	if v := plain(m.View().Content); strings.Contains(v, "main.go  ") && strings.Contains(v, "▾ app") {
		t.Errorf("not collapsed:\n%s", v)
	}
	m, _ = press(m, "G", "enter")
	if !strings.Contains(plain(m.View().Content), "was deleted") {
		t.Error("opening a deleted file does not explain")
	}
}

func TestExplorerLargeListingStaysResponsive(t *testing.T) {
	paths := make([]string, 0, 100_000)
	for i := range 100_000 {
		paths = append(paths, fmt.Sprintf("pkg%03d/sub%02d/file%d.go", i%500, i%20, i))
	}
	start := time.Now()
	m := explorerModel(t, &service.FileList{Paths: paths, Marks: map[string]string{}})
	load := time.Since(start)
	start = time.Now()
	for range 50 {
		m, _ = press(m, "j")
		_ = m.View()
	}
	if frames := time.Since(start); load > 3*time.Second || frames > time.Second {
		t.Errorf("100 k files: load %v, 50 frames %v", load, frames)
	}
}
