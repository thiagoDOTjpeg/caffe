package studio

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"caffe/internal/studio/ui"
)

func (m model) syncTabs() model {
	if m.file == nil {
		m.tabs, m.split = nil, nil
		return m
	}
	for i, t := range m.tabs {
		if t.ws == m.file.ws && t.path == m.file.path {
			if t != m.file {
				m.tabs = append(append([]*viewer(nil), m.tabs[:i]...), append([]*viewer{m.file}, m.tabs[i+1:]...)...)
			}
			m.split = m.findTab(m.split)
			return m
		}
	}
	m.tabs = append(append([]*viewer(nil), m.tabs...), m.file)
	return m
}

func (m model) findTab(v *viewer) *viewer {
	if v == nil {
		return nil
	}
	for _, t := range m.tabs {
		if t.ws == v.ws && t.path == v.path {
			return t
		}
	}
	return nil
}

func (m model) activeTab() int {
	for i, t := range m.tabs {
		if t == m.file {
			return i
		}
	}
	return -1
}

func (m model) activate(i int) (model, tea.Cmd) {
	if i < 0 || i >= len(m.tabs) {
		return m, nil
	}
	m.file = m.tabs[i]
	if m.split == m.file {
		m.split = nil
	}
	m.fileGen++
	return m, watchFile(m.fileGen)
}

func (m model) nextTab(d int) (model, tea.Cmd) {
	if len(m.tabs) < 2 {
		return m, nil
	}
	return m.activate((m.activeTab() + d + len(m.tabs)) % len(m.tabs))
}

func (m model) closeTab() (model, tea.Cmd) {
	i := m.activeTab()
	if i < 0 {
		return m, nil
	}
	m.tabs = append(append([]*viewer(nil), m.tabs[:i]...), m.tabs[i+1:]...)
	if len(m.tabs) == 0 {
		m.file, m.split, m.tree.focused = nil, nil, true
		return m, nil
	}
	return m.activate(min(i, len(m.tabs)-1))
}

func (m model) toggleSplit() (model, tea.Cmd) {
	if m.split != nil {
		m.split = nil
		return m, nil
	}
	if len(m.tabs) < 2 {
		return m.notify("a split needs two open files")
	}
	i := m.activeTab()
	m.split = m.tabs[(i-1+len(m.tabs))%len(m.tabs)]
	return m, nil
}

func (m model) swapSplit() (model, tea.Cmd) {
	if m.split == nil {
		return m, nil
	}
	other := m.file
	for i, t := range m.tabs {
		if t == m.split {
			m, cmd := m.activate(i)
			m.split = other
			return m, cmd
		}
	}
	return m, nil
}

func (m model) tabBar(width int) string {
	var parts []string
	for _, t := range m.tabs {
		name := t.path[strings.LastIndex(t.path, "/")+1:]
		if t.ws != m.wsID() {
			name = t.label + ":" + name
		}
		switch t {
		case m.file:
			name = m.theme.Render(ui.ToneAccent, "["+name+"]")
		case m.split:
			name = "|" + name + "|"
		default:
			name = " " + name + " "
		}
		parts = append(parts, name)
	}
	return ui.Fit(" "+strings.Join(parts, " "), width)
}

func (m model) buffersScreen(width, height int) string {
	out := []string{m.tabBar(width)}
	if m.split == nil {
		return strings.Join(append(out, m.viewerScreen(m.file, width, height-1)), "\n")
	}
	lw := (width - 1) / 2
	left := strings.Split(m.viewerScreen(m.split, lw, height-1), "\n")
	right := strings.Split(m.viewerScreen(m.file, width-lw-1, height-1), "\n")
	for i := range height - 1 {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out = append(out, ui.Fit(l, lw)+m.theme.Render(ui.ToneMuted, "│")+r)
	}
	return strings.Join(out, "\n")
}
