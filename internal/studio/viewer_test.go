package studio

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/chroma/v2/lexers"

	"caffe/internal/studio/ui"
)

const goSource = "package main\n\n// hello prints a greeting.\nfunc hello() {\n\tfmt.Println(\"hi\", 42)\n}\n"

func TestViewerOpensHighlightsAndScrolls(t *testing.T) {
	m := overviewModel(t)
	f := m.src.(*fakeSource)
	f.set(func(f *fakeSource) {
		f.files = map[string]string{"main:main.go": goSource, "main:long.txt": strings.Repeat("line\n", 200)}
	})
	m, _ = press(m, "5")
	m, cmd := m.openFile("main", "main.go")
	if !strings.Contains(m.View().Content, "loading…") {
		t.Error("no loading state")
	}
	m = drive(t, m, cmd)
	golden(t, "viewer-go-120x40", m.View().Content)
	text := plain(m.View().Content)
	if !strings.Contains(text, "4 │ func hello() {") || !strings.Contains(text, `│     fmt.Println("hi", 42)`) {
		t.Errorf("viewer:\n%s", text)
	}

	m, cmd = m.openFile("main", "long.txt")
	m = drive(t, m, cmd)
	m, _ = press(m, "G")
	if v := plain(m.View().Content); !strings.Contains(v, "200 │ line") || strings.Contains(v, "  1 │ line") {
		t.Error("G does not reach the end")
	}
	m, _ = press(m, "g")
	if v := plain(m.View().Content); !strings.Contains(v, "  1 │ line") {
		t.Error("g does not reach the top")
	}

	m, cmd = m.openFile("main", "missing.go")
	m = drive(t, m, cmd)
	if v := m.View().Content; !strings.Contains(v, "not found") {
		t.Error("read error not shown")
	}
}

func TestHighlightCostDependsOnTheWindowOnly(t *testing.T) {
	var b strings.Builder
	for i := range 200_000 {
		fmt.Fprintf(&b, "var x%d = %d // c\n", i, i)
	}
	m := overviewModel(t)
	f := m.src.(*fakeSource)
	f.set(func(f *fakeSource) { f.files = map[string]string{"main:big.go": b.String()} })
	m, cmd := m.openFile("main", "big.go")
	m = drive(t, m, cmd)
	m.screen = screenWorkspace
	start := time.Now()
	for range 20 {
		m, _ = press(m, "ctrl+d")
		_ = m.View()
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("20 frames of a 200 k-line file took %v", d)
	}
}

func TestHighlightClipsAndFollowsTheTheme(t *testing.T) {
	lines := strings.Split(strings.TrimSuffix(goSource, "\n"), "\n")
	mono := highlight(lexers.Get("go"), lines, 10, ui.ThemeByName("mono", false))
	if len(mono) != len(lines) {
		t.Fatalf("%d lines for %d", len(mono), len(lines))
	}
	if got := plain(mono[2]); got != "// hello …" {
		t.Errorf("clipped comment = %q", got)
	}
	if mono[2] != "// hello …" {
		t.Error("mono theme colours comments")
	}
}
