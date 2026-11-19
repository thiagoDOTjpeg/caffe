package studio

import (
	"strings"
	"testing"

	"caffe/internal/service"
)

func TestFuzzy(t *testing.T) {
	if _, ok := fuzzy("internal/studio/model.go", "xyz"); ok {
		t.Error("non-match matched")
	}
	name, _ := fuzzy("internal/studio/model.go", "model")
	dirs, _ := fuzzy("model/x/internal.go", "model")
	if name >= dirs {
		t.Errorf("file-name match (%d) does not beat directory match (%d)", name, dirs)
	}
	tight, _ := fuzzy("a/vie.go", "vie")
	loose, _ := fuzzy("a/vxixe.go", "vie")
	if tight >= loose {
		t.Errorf("consecutive (%d) does not beat scattered (%d)", tight, loose)
	}
}

func TestFinderOpensTheChosenFile(t *testing.T) {
	m := explorerModel(t, &service.FileList{Paths: []string{"README.md", "cmd/app/main.go", "internal/main_test.go"}, Marks: map[string]string{}})
	m, cmd := press(m, "space", "w", "f")
	m = drive(t, m, cmd)
	if m.overlay != overlayFinder {
		t.Fatal("space w f does not open the finder")
	}
	m, _ = press(m, "m", "a", "i", "n")
	v := plain(m.View().Content)
	if !strings.Contains(v, "▸ cmd/app/main.go") || !strings.Contains(v, "internal/main_test.go") || strings.Contains(v, "README") {
		t.Errorf("matches:\n%s", v)
	}
	m, cmd = press(m, "enter")
	m = drive(t, m, cmd)
	if m.overlay != overlayNone || m.file == nil || m.file.path != "cmd/app/main.go" || !strings.Contains(plain(m.View().Content), "func hello") {
		t.Errorf("not opened: %+v", m.file)
	}
}
