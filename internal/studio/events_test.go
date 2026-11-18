package studio

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	c "caffe/internal/contracts"
)

// eventsModel has a run with 30 events in the database; the live feed holds
// only the newest 10 of them, as after a long session.
func eventsModel(t *testing.T) (model, *fakeSource) {
	m := overviewModel(t)
	f := m.src.(*fakeSource)
	var evs []c.Event
	for i := 1; i <= 30; i++ {
		typ, task := "task.transition", "t1"
		if i%3 == 0 {
			typ, task = c.EventDiffCollected, "t2"
		}
		evs = append(evs, c.Event{Seq: int64(i), RunID: m.live.runID, TaskID: task, Type: typ,
			CreatedAt: testNow.Add(time.Duration(i-60) * time.Second)})
	}
	f.set(func(f *fakeSource) { f.events = evs })
	m.live.feed, m.live.lastSeq = evs[20:], 30
	return m, f
}

func TestEventsPanelFiltersByTaskAndType(t *testing.T) {
	m, _ := eventsModel(t)
	m, _ = press(m, "tab")
	if m.scope() != "events" {
		t.Fatalf("tab did not focus the panel: %s", m.scope())
	}
	m, _ = press(m, "t") // first task: research (t1)
	for _, e := range m.events.filtered(m.panelEvents()) {
		if e.TaskID != "t1" {
			t.Fatalf("task filter let %s through", e.TaskID)
		}
	}
	if !strings.Contains(m.panelTitle(), "task research") {
		t.Errorf("title %q", m.panelTitle())
	}
	m, _ = press(m, "t", "/", "d", "i", "f", "f", "enter")
	got := m.events.filtered(m.panelEvents())
	if len(got) == 0 {
		t.Fatal("impl + diff filtered everything")
	}
	for _, e := range got {
		if e.TaskID != "t2" || e.Type != c.EventDiffCollected {
			t.Fatalf("filters let %s/%s through", e.TaskID, e.Type)
		}
	}
	// While typing, screen keys are text.
	m, _ = press(m, "/", "1")
	if m.screen != screenOverview || m.events.typeFilter != "diff1" {
		t.Errorf("typing: screen %d filter %q", m.screen, m.events.typeFilter)
	}
	m, _ = press(m, "esc", "x")
	if m.events.typeFilter != "" || m.events.taskFilter != "" {
		t.Error("x does not clear the filters")
	}
}

func TestEventsPanelScrollBackLoadsOlderPages(t *testing.T) {
	m, _ := eventsModel(t)
	rows := m.layout().Panel.H - 1
	m, _ = press(m, "tab")
	var cmd tea.Cmd
	for range 10 - rows { // to the top of the live feed
		m, cmd = press(m, "k")
	}
	if cmd == nil || !m.events.loading {
		t.Fatal("reaching the top did not load an older page")
	}
	m = drive(t, m, cmd)
	if all := m.panelEvents(); len(all) != 30 || all[0].Seq != 1 {
		t.Fatalf("after scroll-back: %d events from %d", len(all), all[0].Seq)
	}
	if !m.events.noMore {
		t.Error("a short page did not mark the beginning")
	}
	// No duplicates between the loaded pages and the live feed.
	seen := map[int64]bool{}
	for _, e := range m.panelEvents() {
		if seen[e.Seq] {
			t.Fatalf("seq %d twice", e.Seq)
		}
		seen[e.Seq] = true
	}
	m, _ = press(m, "ctrl+u", "ctrl+u", "ctrl+u", "ctrl+u") // 4 pages of 7 rows: past the top, clamped
	if v := strings.Join(m.panelLines(rows), "\n"); !strings.Contains(v, "beginning of the run") {
		t.Errorf("top not marked:\n%s", v)
	}
	m, _ = press(m, "G")
	if m.events.scroll != 0 || !strings.Contains(m.View().Content, "14:10:") {
		t.Error("G does not follow the newest")
	}
}

func TestEventsPanelShowsTheNewestEvent(t *testing.T) {
	m, _ := eventsModel(t)
	lines := m.panelLines(m.layout().Panel.H - 1)
	if last := lines[len(lines)-1]; !strings.Contains(last, m.live.feed[9].CreatedAt.Local().Format("15:04:05")) {
		t.Errorf("newest event not on the last line: %q", last)
	}
	v := m.View().Content
	if !strings.Contains(v, m.live.feed[9].CreatedAt.Local().Format("15:04:05")) {
		t.Error("the panel cuts off the newest event")
	}
}

func TestFocusNeedsAVisiblePanel(t *testing.T) {
	m, _ := eventsModel(t)
	res, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24}) // compact: panel closed
	m = res.(model)
	if m, _ = press(m, "tab"); m.events.focused {
		t.Error("focused a hidden panel")
	}
}
