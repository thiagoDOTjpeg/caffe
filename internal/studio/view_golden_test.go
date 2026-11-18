package studio

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rs/zerolog"

	"caffe/internal/studio/ui"
)

// Re-record with: go test ./internal/studio/ -run Golden -update
var update = flag.Bool("update", false, "rewrite golden files")

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to record)", err)
	}
	if got != string(want) {
		t.Errorf("%s changed.\n--- want\n%s\n--- got\n%s", name, want, got)
	}
}

func render(m model, w, h int, keys ...string) string {
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	for _, k := range keys {
		next, _ = next.Update(keyPress(k))
	}
	return next.(model).View().Content
}

func TestGoldenLayout(t *testing.T) {
	m := newModel(nil, Project{Name: "demo", Root: "/p"}, &crashGuard{log: zerolog.Nop()})
	for _, size := range [][2]int{{60, 16}, {80, 24}, {120, 40}, {200, 60}, {50, 12}} {
		golden(t, fmt.Sprintf("layout-%dx%d", size[0], size[1]), render(m, size[0], size[1]))
	}
	for _, name := range ui.Themes {
		themed := m
		themed.theme = ui.ThemeByName(name, false)
		golden(t, "theme-"+name+"-120x40", render(themed, 120, 40))
	}
	// Toggles: panel opened in compact mode, sidebar hidden in full mode, screen 3.
	golden(t, "layout-80x24-panel", render(m, 80, 24, "`"))
	golden(t, "layout-120x40-nosidebar-agents", render(m, 120, 40, "ctrl+b", "3"))
}
