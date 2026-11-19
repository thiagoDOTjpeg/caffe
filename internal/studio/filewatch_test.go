package studio

import (
	"io/fs"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

type fakeInfo struct {
	fs.FileInfo
	mod  time.Time
	size int64
}

func (f fakeInfo) ModTime() time.Time { return f.mod }
func (f fakeInfo) Size() int64        { return f.size }

// step delivers msg and runs the command it returns, once.
func step(m model, msg tea.Msg) (model, tea.Msg) {
	res, cmd := m.Update(msg)
	m = res.(model)
	if cmd == nil {
		return m, nil
	}
	return m, cmd()
}

func TestOpenFileReloadsWhenChangedOnDisk(t *testing.T) {
	m := overviewModel(t)
	f := m.src.(*fakeSource)
	f.set(func(f *fakeSource) { f.files = map[string]string{"main:a.go": "package a\n"} })
	m, _ = press(m, "5")
	m, cmd := m.openFile("main", "a.go")
	m = drive(t, m, cmd)

	stats := 0
	info := fakeInfo{mod: m.file.file.ModTime, size: m.file.file.Size}
	var statErr error
	statFile = func(string) (fs.FileInfo, error) { stats++; return info, statErr }
	every := fileWatchEvery
	fileWatchEvery = time.Millisecond // step runs tick commands synchronously
	t.Cleanup(func() { statFile, fileWatchEvery = defaultStat, every })

	// Unchanged: no reload, the chain goes on.
	m, msg := step(m, fileTickMsg{m.fileGen})
	if _, ok := msg.(fileStatMsg); !ok || stats != 1 {
		t.Fatalf("tick did not stat: %T", msg)
	}
	if m, msg = step(m, msg); msg == nil || m.file.disk != "" {
		t.Fatalf("unchanged file: disk %q, next %T", m.file.disk, msg)
	}

	// Changed: reloaded in place, marked, and a new chain starts.
	f.set(func(f *fakeSource) { f.files["main:a.go"] = "package b\n" })
	info.size = 99
	gen := m.fileGen
	m, msg = step(m, fileTickMsg{gen})
	m, msg = step(m, msg)
	if _, ok := msg.(fileMsg); !ok {
		t.Fatalf("change not reloaded: %T", msg)
	}
	if v := plain(m.View().Content); !strings.Contains(v, "package a") {
		t.Error("old text not kept while reloading")
	}
	m, _ = step(m, msg)
	if v := plain(m.View().Content); !strings.Contains(v, "package b") || !strings.Contains(v, "changed on disk, reloaded") || m.fileGen == gen {
		t.Errorf("after reload (gen %d→%d):\n%s", gen, m.fileGen, v)
	}
	if _, msg = step(m, fileTickMsg{gen}); msg != nil {
		t.Error("the old chain keeps running")
	}

	// Deleted: marked, nothing read.
	statErr = fs.ErrNotExist
	m, msg = step(m, fileTickMsg{m.fileGen})
	m, _ = step(m, msg)
	if !strings.Contains(plain(m.View().Content), "deleted on disk") {
		t.Error("deletion not shown")
	}

	// Hidden: no I/O.
	m, _ = press(m, "1")
	before := stats
	if _, msg = step(m, fileTickMsg{m.fileGen}); stats != before {
		t.Error("stat while the Workspace screen is hidden")
	}
}
