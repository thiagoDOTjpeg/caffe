package studio

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"caffe/internal/service"
	"caffe/internal/studio/ui"
)

// workspaces is the selected workspace and the selector overlay's state.
type workspaces struct {
	selected service.Workspace // zero: main
	list     []service.Workspace
	loaded   bool
	err      error
	cursor   int
}

type workspacesMsg struct {
	list []service.Workspace
	err  error
}

// label names the selected workspace for the header and the status bar.
func (w workspaces) label() string {
	if w.selected.Kind != "task" {
		return service.MainWorkspace
	}
	return w.selected.TaskKey + " · run " + short(w.selected.RunID)
}

func (m model) openWorkspaces() (model, tea.Cmd) {
	if m.src == nil {
		return m, nil
	}
	m.overlay, m.ws.loaded, m.ws.err = overlayWorkspaces, false, nil
	src := m.src
	return m, func() tea.Msg {
		list, err := src.Workspaces(context.Background())
		return workspacesMsg{list, err}
	}
}

func (m model) updateWorkspaces(msg workspacesMsg) model {
	m.ws.list, m.ws.err, m.ws.loaded, m.ws.cursor = msg.list, msg.err, true, 0
	for i, w := range msg.list {
		if w.ID == m.ws.selected.ID {
			m.ws.cursor = i
		}
	}
	return m
}

func (m model) workspacesKey(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "esc", "q":
		m.overlay = overlayNone
	case "ctrl+q", "ctrl+c":
		return m.run("app.quit")
	case "j", "down":
		m.ws.cursor = min(m.ws.cursor+1, max(len(m.ws.list)-1, 0))
	case "k", "up":
		m.ws.cursor = max(m.ws.cursor-1, 0)
	case "enter":
		if m.ws.cursor >= len(m.ws.list) {
			return m, nil
		}
		w := m.ws.list[m.ws.cursor]
		if w.Missing {
			return m.notify("the worktree of " + w.TaskKey + " was removed; nothing to show")
		}
		m.ws.selected, m.overlay = w, overlayNone
		if w.Kind != "task" {
			m.ws.selected = service.Workspace{}
		}
		if m.file != nil && m.file.ws != m.wsID() {
			m.file = nil // it belongs to the workspace left
		}
		if m.screen == screenWorkspace {
			return m.loadTree()
		}
	}
	return m, nil
}

func (m model) workspacesView() string {
	var b strings.Builder
	b.WriteString(" Select a workspace\n\n")
	switch {
	case !m.ws.loaded:
		b.WriteString("  loading…")
	case m.ws.err != nil:
		b.WriteString("  " + m.theme.Render(ui.ToneError, "✗ "+m.ws.err.Error()))
	}
	for i, w := range m.ws.list {
		marker := " "
		if i == m.ws.cursor {
			marker = "▸"
		}
		name, where := service.MainWorkspace, w.Path
		if w.Kind == "task" {
			name = fmt.Sprintf("%s · run %s · %s", w.TaskKey, short(w.RunID), w.TaskStatus)
		}
		var tags []string
		if w.Branch != "" {
			tags = append(tags, w.Branch)
		}
		if w.Dirty {
			tags = append(tags, m.theme.Render(ui.ToneWarn, "● uncommitted changes"))
		}
		if w.Missing {
			tags, where = append(tags, m.theme.Render(ui.ToneMuted, "removed")), ""
		}
		fmt.Fprintf(&b, " %s %-40s %s\n", marker, name, strings.Join(tags, " · "))
		if where != "" {
			fmt.Fprintf(&b, "     %s\n", m.theme.Render(ui.ToneMuted, where))
		}
	}
	b.WriteString("\n  enter selects · esc closes")
	return b.String()
}
