package studio

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	c "caffe/internal/contracts"
)

// olderPage is how many events one scroll-back load reads.
const olderPage = 200

// eventsPanel is the bottom panel's event log of the selected run: newest
// at the bottom, filterable by task and type, scrolled back page by page
// beyond what the live feed holds.
type eventsPanel struct {
	focused bool
	scroll  int // lines above the newest; 0 follows new events

	typeFilter string // substring of the event type
	typing     bool   // keys edit typeFilter
	taskFilter string // task id; "" = all tasks

	// Pages read backwards from the database, older than the live feed.
	olderRun string
	older    []c.Event // oldest first
	loading  bool
	noMore   bool // the beginning of the run's log was reached
}

type olderMsg struct {
	runID  string
	events []c.Event
	err    error
}

// panelEvents is the selected run's log as the panel knows it: loaded older
// pages, then the live feed.
func (m model) panelEvents() []c.Event {
	var out []c.Event
	if m.events.olderRun == m.live.runID {
		out = append(out, m.events.older...)
	}
	oldest := int64(1) << 62
	if len(out) > 0 {
		oldest = out[len(out)-1].Seq
	}
	for _, e := range m.live.feed {
		if e.RunID == m.live.runID && (len(m.events.older) == 0 || e.Seq > oldest) {
			out = append(out, e)
		}
	}
	return out
}

// filtered applies the task and type filters.
func (p eventsPanel) filtered(evs []c.Event) []c.Event {
	q := strings.ToLower(p.typeFilter)
	var out []c.Event
	for _, e := range evs {
		if p.taskFilter != "" && e.TaskID != p.taskFilter {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(e.Type), q) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// loadOlder reads the page before the oldest event the panel holds.
func (m model) loadOlder() (model, tea.Cmd) {
	p := &m.events
	if p.olderRun != m.live.runID {
		p.olderRun, p.older, p.noMore = m.live.runID, nil, false
	}
	if p.loading || p.noMore || m.src == nil || m.live.runID == "" {
		return m, nil
	}
	var before int64
	if all := m.panelEvents(); len(all) > 0 {
		before = all[0].Seq
	}
	p.loading = true
	src, runID := m.src, m.live.runID
	return m, func() tea.Msg {
		evs, err := src.EventsBefore(context.Background(), before, runID, olderPage)
		return olderMsg{runID, evs, err}
	}
}

func (m model) updateOlder(msg olderMsg) (model, tea.Cmd) {
	p := &m.events
	p.loading = false
	if msg.runID != p.olderRun {
		return m, nil // the run changed meanwhile
	}
	if msg.err != nil {
		m.live.fail(msg.err, m.now())
		return m, nil
	}
	p.older = append(append([]c.Event(nil), msg.events...), p.older...)
	p.noMore = len(msg.events) < olderPage
	return m, nil
}

// eventsScroll moves the view; reaching the top of what is loaded loads the
// previous page.
func (m model) eventsScroll(delta, rows int) (model, tea.Cmd) {
	top := max(len(m.panelAll())-rows, 0)
	m.events.scroll = min(max(m.events.scroll+delta, 0), top)
	if delta > 0 && m.events.scroll >= top {
		return m.loadOlder()
	}
	return m, nil
}

// cycleTask moves the task filter to the next task of the run, then off.
func (m model) cycleTask() model {
	if m.live.snap == nil {
		return m
	}
	tasks := m.live.snap.Tasks
	next := ""
	if m.events.taskFilter == "" && len(tasks) > 0 {
		next = tasks[0].ID
	}
	for i, t := range tasks {
		if t.ID == m.events.taskFilter && i+1 < len(tasks) {
			next = tasks[i+1].ID
		}
	}
	m.events.taskFilter, m.events.scroll = next, 0
	return m
}

func (m model) eventsFilterKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.events.typing = false
	case "esc":
		m.events.typing, m.events.typeFilter = false, ""
	case "ctrl+q", "ctrl+c":
		return m.run("app.quit")
	case "backspace":
		if r := []rune(m.events.typeFilter); len(r) > 0 {
			m.events.typeFilter = string(r[:len(r)-1])
		}
	default:
		m.events.typeFilter += msg.Text
	}
	m.events.scroll = 0
	return m, nil
}

// panelTitle names the panel, its focus and its filters.
func (m model) panelTitle() string {
	t := "events"
	if m.events.focused {
		t = "▶ events"
	}
	if id := m.events.taskFilter; id != "" {
		key := id
		if m.live.snap != nil {
			for _, tk := range m.live.snap.Tasks {
				if tk.ID == id {
					key = tk.Key
				}
			}
		}
		t += " · task " + key
	}
	switch {
	case m.events.typing:
		t += " · type " + m.events.typeFilter + "▏"
	case m.events.typeFilter != "":
		t += " · type " + m.events.typeFilter
	}
	if m.events.scroll > 0 {
		t += fmt.Sprintf(" · ↑ %d", m.events.scroll)
	}
	if m.events.loading {
		t += " · loading older…"
	}
	if m.events.focused && !m.events.typing {
		t += " · j/k scroll · / type · t task · x clear"
	}
	return t
}

// panelAll is every line the panel can show for the filtered log, oldest
// first, with a marker once the beginning of the run was reached.
func (m model) panelAll() []string {
	evs := m.events.filtered(m.panelEvents())
	keys := map[string]string{}
	if m.live.snap != nil {
		for _, t := range m.live.snap.Tasks {
			keys[t.ID] = t.Key
		}
	}
	var out []string
	if m.events.noMore && m.events.olderRun == m.live.runID && len(evs) > 0 {
		out = append(out, "  ─ beginning of the run ─")
	}
	for _, e := range evs {
		out = append(out, fmt.Sprintf("  %s  %-18s%s", e.CreatedAt.Local().Format("15:04:05"), e.Type, keys[e.TaskID]))
	}
	return out
}

// panelLines is the visible window of panelAll, rows tall.
func (m model) panelLines(rows int) []string {
	if rows <= 0 {
		return nil // panel hidden
	}
	all := m.panelAll()
	end := max(len(all)-m.events.scroll, 0)
	return all[max(end-rows, 0):end]
}
