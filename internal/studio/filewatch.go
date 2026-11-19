package studio

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
)

// File watching (WS-10). The viewer is read-only in M2a, so a file that
// changes on disk (the agent, the user's editor) is simply reloaded.
// ponytail: one os.Stat of the open file per second instead of fsnotify —
// one buffer, no new dependency, same on Windows; switch to fsnotify on the
// buffers' directories when tabs (WS-04) hold many files.

var defaultStat = os.Stat

var (
	fileWatchEvery = time.Second
	statFile       = defaultStat
)

type (
	fileTickMsg struct{ gen int }
	fileStatMsg struct {
		gen  int
		mod  time.Time
		size int64
		err  error
	}
)

func watchFile(gen int) tea.Cmd {
	return tea.Tick(fileWatchEvery, func(time.Time) tea.Msg { return fileTickMsg{gen} })
}

func (m model) updateFileWatch(msg tea.Msg) (model, tea.Cmd) {
	switch msg := msg.(type) {
	case fileTickMsg:
		v := m.file
		if msg.gen != m.fileGen || v == nil || v.file == nil {
			return m, nil // replaced or closed: this chain ends
		}
		if m.screen != screenWorkspace || m.diff != nil || m.overlay != overlayNone {
			return m, watchFile(msg.gen) // not visible: no I/O (NFR-P6)
		}
		path, gen := v.file.Path, msg.gen
		return m, func() tea.Msg {
			fi, err := statFile(path)
			if err != nil {
				return fileStatMsg{gen: gen, err: err}
			}
			return fileStatMsg{gen, fi.ModTime(), fi.Size(), nil}
		}
	case fileStatMsg:
		v := m.file
		if msg.gen != m.fileGen || v == nil || v.file == nil {
			return m, nil
		}
		switch {
		case errors.Is(msg.err, fs.ErrNotExist):
			if v.disk != "deleted" {
				nv := *v
				nv.disk = "deleted"
				m.file = &nv
			}
		case msg.err != nil:
		case !msg.mod.Equal(v.file.ModTime) || msg.size != v.file.Size:
			nv := *v
			nv.disk = "reloaded"
			m.file = &nv
			src, ws, p := m.src, v.ws, v.path
			return m, func() tea.Msg { // keeps the old text on screen until the new one is read
				f, err := src.ReadFile(context.Background(), ws, p, maxFileRead)
				return fileMsg{ws, p, f, err}
			}
		}
		return m, watchFile(msg.gen)
	}
	return m, nil
}
