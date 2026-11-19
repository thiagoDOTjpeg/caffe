package studio

import (
	"fmt"
	"strings"
	"time"

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
	src      source                // nil: no project data (tests of layout only)
	changes  <-chan service.Change // nil: no watcher
	live     live
	health   health
	runs     runsView
	tasks    tasksView
	agents   agentsView
	inspect  *inspector  // nil: closed
	report   *reportView // nil: closed
	events   eventsPanel
	confirm  *confirmDialog // set while overlayConfirm is open
	toasts   []toast
	inflight map[string]string // run id (or cleanTarget) → action in progress
	form     *newRunForm       // set while overlayNewRun is open
	output   agentOutput       // the Agent Room's output pane
	ws       workspaces
	find     finder
	diff     *diffView // nil: closed
	tree     explorer
	file     *viewer // the Workspace screen's open file; nil: none
	rt       rtObs   // last runtime status of the agents on screen
	watcher  pauser  // paused while the terminal is handed over
	toastSeq int
	now      func() time.Time
	project  Project
	// noProject: opened outside a Caffè project (project.Root is the
	// directory); only a message and quitting are possible.
	noProject bool
	guard     *crashGuard

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
	return model{src: src, project: p, guard: g, registry: r, handlers: h, now: time.Now, theme: ui.ThemeByName("caffe", false)}
}

// Init loads the initial state and starts listening to the watcher.
func (m model) Init() tea.Cmd {
	if m.src == nil {
		return nil
	}
	return tea.Batch(m.live.load(m.src), waitChange(m.changes), checkRuntime(m.src), liveTick(0))
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
		if m.noProject {
			if k := msg.String(); k == "q" || k == "esc" || k == "ctrl+q" || k == "ctrl+c" {
				return m, tea.Quit
			}
			return m, nil
		}
		return m.key(msg) // every key goes through the action registry
	case loadedMsg:
		next, cmd := m.updateLive(msg)
		if msg.err == nil {
			cmd = tea.Batch(cmd, next.refreshInspector())
		}
		return next, cmd
	case changeMsg, refreshMsg:
		return m.updateLive(msg)
	case inspectedMsg:
		return m.updateInspector(msg)
	case olderMsg:
		return m.updateOlder(msg)
	case attachReadyMsg, attachDoneMsg:
		return m.updateAttach(msg)
	case liveTickMsg, outputMsg, rtStatusMsg:
		return m.updateLiveAgents(msg)
	case actionDoneMsg:
		return m.actionDone(msg)
	case diffMsg:
		return m.updateDiff(msg), nil
	case editorDoneMsg:
		return m.editorDone(msg)
	case filesMsg:
		return m.updateFiles(msg), nil
	case fileMsg:
		return m.updateFile(msg), nil
	case workspacesMsg:
		return m.updateWorkspaces(msg), nil
	case reportMsg:
		return m.updateReport(msg), nil
	case toastExpiredMsg:
		return m.expireToast(msg.id), nil
	case healthMsg, healthTickMsg:
		return m.updateHealth(msg)
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
	if m.noProject {
		v := tea.NewView(noProjectView(m.project.Root, m.width, m.height))
		v.AltScreen = true
		return v
	}
	nav := make([]ui.NavItem, len(screenNames))
	for i, name := range screenNames {
		nav[i] = ui.NavItem{Key: fmt.Sprint(i + 1), Label: name, Active: screen(i) == m.screen}
	}
	mode := "NORMAL"
	main := fmt.Sprintf(" %s\n\n Coming in the next increments of the Command Center.\n\n project  %s", screenNames[m.screen], m.project.Root)
	panel := " (no project data)"
	if m.src != nil {
		stale := m.live.staleLine()
		h := l.Main.H
		if stale != "" {
			h--
		}
		switch {
		case m.diff != nil:
			main = m.diffScreen(l.Main.W, h)
		case m.report != nil:
			main = m.reportScreen(l.Main.W, h)
		case m.inspect != nil:
			main = m.inspectorScreen(l.Main.W, h)
		case m.screen == screenOverview:
			main = m.overviewScreen(h)
		case m.screen == screenTasks:
			main = m.tasksScreen(l.Main.W, h)
		case m.screen == screenAgents:
			main = m.agentsScreen(l.Main.W, h)
		case m.screen == screenRuns:
			main = m.runsScreen(l.Main.W)
		case m.screen == screenWorkspace:
			main = m.workspaceScreen(l.Main.W, h)
		}
		if stale != "" {
			main = " " + m.theme.Render(ui.ToneWarn, stale) + "\n" + main
		}
		panel = strings.Join(m.panelLines(l.Panel.H-1), "\n") // the title takes a row
		if panel == "" {
			panel = " no events yet"
			if m.events.typeFilter != "" || m.events.taskFilter != "" {
				panel = " no event matches the filters (x clears them)"
			}
		}
	}
	switch m.overlay {
	case overlayNone:
	case overlayConfirm:
		mode, main = "CONFIRM", m.confirmView(l.Main.W, l.Main.H)
	case overlayNewRun:
		mode, main = "NEW RUN", m.newRunView(l.Main.W)
	case overlayWorkspaces:
		mode, main = "WORKSPACE", m.workspacesView()
	case overlayFinder:
		mode, main = "FIND", m.finderView(l.Main.H)
	default:
		mode, main = m.overlayView()
	}
	status := fmt.Sprintf(" %s · ws: %s · ? help · : commands · space menu · ctrl+q quit", mode, m.ws.label())
	if t := m.toastLine(); t != "" {
		status = " " + mode + " · ws: " + m.ws.label() + " ·" + t
	}
	v := tea.NewView(ui.Frame(l, ui.Content{
		Header:     m.headerText(),
		Nav:        nav,
		Main:       main,
		PanelTitle: m.panelTitle(),
		Panel:      panel,
		Status:     status,
	}, m.theme))
	v.AltScreen = true
	v.WindowTitle = "caffè · " + m.project.Name
	return v
}
