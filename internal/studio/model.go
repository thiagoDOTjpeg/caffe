package studio

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"caffe/internal/service"
	"caffe/internal/studio/ui"
)

// screen is one of Studio's top-level screens, in sidebar order.
type screen int

const (
	screenOverview screen = iota
	screenTasks
	screenAgents
	screenRuns
	screenWorkspace
	screenTerminals
)

var screenNames = [...]string{"Overview", "Tasks", "Agents", "Runs", "Workspace", "Terminals"}

// model is the root of Studio's state (docs/studio/architecture.md §7). All
// I/O runs in commands, never in Update or View.
type model struct {
	src     source                // nil: no project data (tests of layout only)
	changes <-chan service.Change // nil: no watcher
	live    live
	project Project
	guard   *crashGuard

	registry *ui.Registry
	handlers map[string]handler

	width, height int
	toggles       ui.Toggles
	screen        screen
	theme         ui.Theme

	overlay      overlay
	paletteQuery string
	paletteSel   int
	leaderGroup  string
}

func newModel(src source, p Project, g *crashGuard) model {
	r, h := actions()
	return model{src: src, project: p, guard: g, registry: r, handlers: h, theme: ui.ThemeByName("caffe", false)}
}

// Init loads the initial state and starts listening to the watcher.
func (m model) Init() tea.Cmd {
	if m.src == nil {
		return nil
	}
	return tea.Batch(m.live.load(m.src), waitChange(m.changes))
}

// Update is guarded: a panic anywhere below is written to the crash log
// before Bubble Tea restores the terminal.
func (m model) Update(msg tea.Msg) (next tea.Model, cmd tea.Cmd) {
	m.guard.protect(func() { next, cmd = m.update(msg) })
	return next, cmd
}

func (m model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		return m.key(msg) // every key goes through the action registry
	case changeMsg, refreshMsg, loadedMsg:
		return m.updateLive(msg)
	}
	return m, nil
}

// flip toggles a region relative to what is shown now.
func flip(shown bool) *bool {
	v := !shown
	return &v
}

func (m model) layout() ui.Layout { return ui.Compute(m.width, m.height, m.toggles) }

func (m model) View() (v tea.View) {
	m.guard.protect(func() { v = m.view() })
	return v
}

func (m model) view() tea.View {
	l := m.layout()
	nav := make([]ui.NavItem, len(screenNames))
	for i, name := range screenNames {
		nav[i] = ui.NavItem{Key: fmt.Sprint(i + 1), Label: name, Active: screen(i) == m.screen}
	}
	mode := "NORMAL"
	main := fmt.Sprintf(" %s\n\n Coming in the next increments of the Command Center.\n\n project  %s", screenNames[m.screen], m.project.Root)
	panel := " (no project data)"
	if m.src != nil {
		main = fmt.Sprintf(" %s\n\n%s", screenNames[m.screen], m.live.summary())
		if s := m.live.staleLine(); s != "" {
			main = " " + m.theme.Render(ui.ToneWarn, s) + "\n" + main
		}
		panel = strings.Join(m.live.feedLines(l.Panel.H), "\n")
		if panel == "" {
			panel = " no events yet"
		}
	}
	if m.overlay != overlayNone {
		mode, main = m.overlayView()
	}
	v := tea.NewView(ui.Frame(l, ui.Content{
		Header:     fmt.Sprintf("caffè ─ %s", m.project.Name),
		Nav:        nav,
		Main:       main,
		PanelTitle: "events",
		Panel:      panel,
		Status:     fmt.Sprintf(" %s · ws: main · ? help · : commands · space menu · ctrl+q quit", mode),
	}, m.theme))
	v.AltScreen = true
	v.WindowTitle = "caffè · " + m.project.Name
	return v
}
