package studio

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"caffe/internal/studio/ui"
)

// overlay is what is drawn over the current screen and captures keys.
type overlay int

const (
	overlayNone overlay = iota
	overlayHelp
	overlayPalette
	overlayLeader
)

// handler performs an action. Every registered action has exactly one;
// TestEveryActionHasAHandler enforces it.
type handler func(m model) (model, tea.Cmd)

// actions builds the registry and its handlers. Keys follow
// ux-specification.md §3: q closes, ctrl+q quits, ? help, : palette,
// space leader.
func actions() (*ui.Registry, map[string]handler) {
	r := &ui.Registry{}
	h := map[string]handler{}
	add := func(a ui.Action, fn handler) {
		r.Add(a)
		h[a.ID] = fn
	}
	g := ui.ScopeGlobal
	r.Group("g", "go to screen")
	r.Group("v", "view")

	add(ui.Action{ID: "app.quit", Title: "Quit Caffè Studio", Scope: g, Keys: []string{"ctrl+q", "ctrl+c"}},
		func(m model) (model, tea.Cmd) { return m, tea.Quit })
	add(ui.Action{ID: "help.toggle", Title: "Show the keys of this view", Scope: g, Keys: []string{"?"}},
		func(m model) (model, tea.Cmd) { return m.toggleOverlay(overlayHelp), nil })
	add(ui.Action{ID: "palette.open", Title: "Command palette", Scope: g, Keys: []string{":", "ctrl+p"}},
		func(m model) (model, tea.Cmd) {
			m.overlay, m.paletteQuery, m.paletteSel = overlayPalette, "", 0
			return m, nil
		})
	add(ui.Action{ID: "leader.open", Title: "Leader menu", Scope: g, Keys: []string{"space"}},
		func(m model) (model, tea.Cmd) { m.overlay, m.leaderGroup = overlayLeader, ""; return m, nil })
	add(ui.Action{ID: "overlay.close", Title: "Close the overlay", Scope: g, Keys: []string{"esc", "q"}},
		func(m model) (model, tea.Cmd) { m.overlay = overlayNone; return m, nil })
	add(ui.Action{ID: "layout.sidebar", Title: "Toggle the sidebar", Scope: g, Keys: []string{"ctrl+b"}, Leader: "v b"},
		func(m model) (model, tea.Cmd) { m.toggles.Sidebar = flip(m.layout().Sidebar.Visible()); return m, nil })
	add(ui.Action{ID: "layout.panel", Title: "Toggle the bottom panel", Scope: g, Keys: []string{"`"}, Leader: "v p"},
		func(m model) (model, tea.Cmd) { m.toggles.Panel = flip(m.layout().Panel.Visible()); return m, nil })
	for i, name := range screenNames {
		s := screen(i)
		leaderKey := strings.ToLower(name[:1])
		if s == screenTerminals {
			leaderKey = "x" // t is Tasks
		}
		add(ui.Action{ID: "screen." + strings.ToLower(name), Title: "Go to " + name, Scope: g,
			Keys: []string{fmt.Sprint(i + 1)}, Leader: "g " + leaderKey},
			func(m model) (model, tea.Cmd) { m.screen, m.overlay = s, overlayNone; return m, nil })
	}
	rs := ui.Scope("runs")
	move := func(d int) handler { return func(m model) (model, tea.Cmd) { return m.runsMove(d), nil } }
	add(ui.Action{ID: "runs.down", Title: "Next run", Scope: rs, Keys: []string{"j", "down"}}, move(1))
	add(ui.Action{ID: "runs.up", Title: "Previous run", Scope: rs, Keys: []string{"k", "up"}}, move(-1))
	add(ui.Action{ID: "runs.top", Title: "First run", Scope: rs, Keys: []string{"g", "home"}}, move(-1<<30))
	add(ui.Action{ID: "runs.bottom", Title: "Last run", Scope: rs, Keys: []string{"G", "end"}}, move(1<<30))
	add(ui.Action{ID: "runs.open", Title: "Open the run", Scope: rs, Keys: []string{"enter"}},
		func(m model) (model, tea.Cmd) { return m.runsOpen() })
	add(ui.Action{ID: "runs.filter", Title: "Filter runs", Scope: rs, Keys: []string{"/"}},
		func(m model) (model, tea.Cmd) { m.runs.filtering = true; return m, nil })
	add(ui.Action{ID: "runs.active", Title: "Show only active runs", Scope: rs, Keys: []string{"a"}},
		func(m model) (model, tea.Cmd) { m.runs.activeOnly = !m.runs.activeOnly; return m, nil })
	ts := ui.Scope("tasks")
	tmove := func(dc, dr int) handler { return func(m model) (model, tea.Cmd) { return m.tasksMove(dc, dr), nil } }
	add(ui.Action{ID: "tasks.left", Title: "Previous column", Scope: ts, Keys: []string{"h", "left"}}, tmove(-1, 0))
	add(ui.Action{ID: "tasks.right", Title: "Next column", Scope: ts, Keys: []string{"l", "right"}}, tmove(1, 0))
	add(ui.Action{ID: "tasks.down", Title: "Next task in the column", Scope: ts, Keys: []string{"j", "down"}}, tmove(0, 1))
	add(ui.Action{ID: "tasks.up", Title: "Previous task in the column", Scope: ts, Keys: []string{"k", "up"}}, tmove(0, -1))
	view := func(v taskView) handler {
		return func(m model) (model, tea.Cmd) { m.tasks.view = v; return m, nil }
	}
	add(ui.Action{ID: "tasks.board", Title: "Task board", Scope: ts, Keys: []string{"b"}}, view(viewBoard))
	add(ui.Action{ID: "tasks.graph", Title: "Task dependency graph", Scope: ts, Keys: []string{"g"}}, view(viewGraph))
	add(ui.Action{ID: "tasks.list", Title: "Task list", Scope: ts, Keys: []string{"L"}}, view(viewList))
	return r, h
}

func (m model) toggleOverlay(o overlay) model {
	if m.overlay == o {
		m.overlay = overlayNone
	} else {
		m.overlay = o
	}
	return m
}

func (m model) scope() ui.Scope { return ui.Scope(strings.ToLower(screenNames[m.screen])) }

// run performs a registered action by id.
func (m model) run(id string) (tea.Model, tea.Cmd) {
	if fn, ok := m.handlers[id]; ok {
		return fn(m)
	}
	return m, nil
}

// key routes one key press. In normal mode every key goes through the
// registry; the palette and the leader menu capture keys while open.
func (m model) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	switch m.overlay {
	case overlayPalette:
		return m.paletteKey(msg)
	case overlayLeader:
		return m.leaderKey(k)
	}
	if m.screen == screenRuns && m.runs.filtering {
		return m.runsFilterKey(msg)
	}
	if a, ok := m.registry.Match(m.scope(), k); ok {
		return m.run(a.ID)
	}
	return m, nil
}

func (m model) paletteKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	matches := m.registry.Filter(m.paletteQuery)
	switch msg.String() {
	case "esc":
		m.overlay = overlayNone
	case "ctrl+q", "ctrl+c":
		return m.run("app.quit")
	case "enter":
		if m.paletteSel < len(matches) {
			m.overlay = overlayNone
			return m.run(matches[m.paletteSel].ID)
		}
	case "up", "ctrl+k":
		m.paletteSel = max(0, m.paletteSel-1)
	case "down", "ctrl+j":
		m.paletteSel = min(len(matches)-1, m.paletteSel+1)
	case "backspace":
		if r := []rune(m.paletteQuery); len(r) > 0 {
			m.paletteQuery, m.paletteSel = string(r[:len(r)-1]), 0
		}
	default:
		if msg.Text != "" {
			m.paletteQuery, m.paletteSel = m.paletteQuery+msg.Text, 0
		}
	}
	return m, nil
}

func (m model) leaderKey(k string) (tea.Model, tea.Cmd) {
	if k == "esc" {
		m.overlay = overlayNone
		return m, nil
	}
	if m.leaderGroup == "" {
		if title, _ := m.registry.LeaderGroup(k); title != "" {
			m.leaderGroup = k
		} else {
			m.overlay = overlayNone // not a group: leave the menu
		}
		return m, nil
	}
	if a, ok := m.registry.MatchLeader(m.leaderGroup + " " + k); ok {
		m.overlay = overlayNone
		return m.run(a.ID)
	}
	m.overlay = overlayNone
	return m, nil
}

// overlayView is the text of the open overlay, drawn in the main region.
// ponytail: drawn in place of the screen; floating overlays (lipgloss layers)
// can come when a screen needs to stay visible underneath.
func (m model) overlayView() (title, body string) {
	var b strings.Builder
	switch m.overlay {
	case overlayHelp:
		fmt.Fprintf(&b, " Keys · %s\n\n", screenNames[m.screen])
		for _, a := range m.registry.Help(m.scope()) {
			fmt.Fprintf(&b, "  %-20s %s\n", ui.KeyLabel(a), a.Title)
		}
		b.WriteString("\n  esc or ? closes")
		return "HELP", b.String()
	case overlayPalette:
		fmt.Fprintf(&b, " : %s▏\n\n", m.paletteQuery)
		matches := m.registry.Filter(m.paletteQuery)
		if len(matches) == 0 {
			b.WriteString("  no matching command")
		}
		for i, a := range matches {
			marker := " "
			if i == m.paletteSel {
				marker = "▸"
			}
			fmt.Fprintf(&b, " %s %-34s %s\n", marker, a.Title, ui.KeyLabel(a))
		}
		return "PALETTE", b.String()
	case overlayLeader:
		if m.leaderGroup == "" {
			b.WriteString(" space …\n\n")
			for _, g := range m.registry.LeaderGroups() {
				fmt.Fprintf(&b, "  %s  %s\n", g[0], g[1])
			}
		} else {
			title, acts := m.registry.LeaderGroup(m.leaderGroup)
			fmt.Fprintf(&b, " space %s · %s\n\n", m.leaderGroup, title)
			for _, a := range acts {
				_, key, _ := strings.Cut(a.Leader, " ")
				fmt.Fprintf(&b, "  %s  %s\n", key, a.Title)
			}
		}
		b.WriteString("\n  esc closes")
		return "LEADER", b.String()
	}
	return "", ""
}
