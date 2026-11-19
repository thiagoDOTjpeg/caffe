package studio

import (
	"strings"
	"testing"
)

func TestContentSearchOpensTheHit(t *testing.T) {
	m := overviewModel(t)
	f := m.src.(*fakeSource)
	f.set(func(f *fakeSource) {
		f.files = map[string]string{"main:a.go": strings.Repeat("filler\n", 20) + "the needle\n"}
	})
	m, _ = press(m, "space", "w", "g")
	if m.overlay != overlayGrep {
		t.Fatal("space w g does not open the search")
	}
	m, _ = press(m, "n", "e", "e", "d", "l", "e")
	m, cmd := press(m, "enter")
	if !strings.Contains(plain(m.View().Content), "searching…") {
		t.Error("no searching state")
	}
	m = drive(t, m, cmd)
	v := plain(m.View().Content)
	if !strings.Contains(v, "1 hits · fake") || !strings.Contains(v, "▸ a.go:21  the needle") {
		t.Fatalf("results:\n%s", v)
	}
	m, cmd = press(m, "enter")
	m = drive(t, m, cmd)
	if m.overlay != overlayNone || m.screen != screenWorkspace || m.file == nil || m.file.path != "a.go" {
		t.Fatalf("hit not opened: %+v", m.file)
	}
	if v := plain(m.View().Content); !strings.Contains(v, "21 │ the needle") {
		t.Errorf("hit line not visible:\n%s", v)
	}
	m, _ = press(m, "space", "w", "g")
	if m.grep.ran != "needle" {
		t.Error("the search is not kept when reopened in the same workspace")
	}
}
