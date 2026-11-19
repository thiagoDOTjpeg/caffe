package studio

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	c "caffe/internal/contracts"
	"caffe/internal/service"
)

var testNow = time.Date(2026, 11, 18, 14, 11, 30, 0, time.Local)

func at(d time.Duration) *time.Time { t := testNow.Add(d); return &t }

func sampleRuns() []service.RunSummary {
	return []service.RunSummary{
		{ID: "0112dd6a-1", Request: "Add a hello file", Status: c.RunExecuting, CreatedAt: testNow.Add(-2 * time.Minute),
			StartedAt: at(-121 * time.Second), OwnerAlive: true},
		{ID: "531fd2a9-2", Request: "Create slow.txt", Status: c.RunCompleted, CreatedAt: testNow.Add(-5 * time.Minute),
			StartedAt: at(-5 * time.Minute), CompletedAt: at(-5*time.Minute + 77*time.Second)},
		{ID: "9c41aa02-3", Request: "Refactor config\nloading", Status: c.RunInterrupted, CreatedAt: testNow.Add(-20 * time.Hour),
			StartedAt: at(-20 * time.Hour)},
	}
}

func runsModel(t *testing.T) (model, *fakeSource) {
	f := newFake()
	f.runs = sampleRuns()
	f.snaps = map[string]*service.RunView{}
	for _, r := range f.runs {
		f.snaps[r.ID] = &service.RunView{Run: r}
	}
	m := liveModel(f)
	m.now = func() time.Time { return testNow }
	m = drive(t, m, m.Init())
	res, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return res.(model), f
}

func TestGoldenRunsScreen(t *testing.T) {
	m, _ := runsModel(t)
	m, _ = press(m, "4")
	golden(t, "runs-120x40", m.View().Content)
}

func TestRunsCursorKeptByIDAcrossRefresh(t *testing.T) {
	m, f := runsModel(t)
	m, _ = press(m, "4", "j")
	if m.runs.cursor != "531fd2a9-2" {
		t.Fatalf("cursor %q", m.runs.cursor)
	}
	f.set(func(f *fakeSource) {
		f.runs = append([]service.RunSummary{{ID: "ffff0000-new", Status: c.RunPending}}, f.runs...)
	})
	m = drive(t, m, func() tea.Msg { return refreshMsg{} })
	if !strings.Contains(m.View().Content, "▸ 531fd2a9") {
		t.Error("cursor moved when a run was added")
	}
	m, _ = press(m, "G")
	if m.runs.cursor != "9c41aa02-3" {
		t.Errorf("G: cursor %q", m.runs.cursor)
	}
	m, _ = press(m, "g", "k")
	if m.runs.cursor != "ffff0000-new" {
		t.Errorf("g then k clamps at the top: %q", m.runs.cursor)
	}
}

func TestRunsFilterAndActiveOnly(t *testing.T) {
	m, _ := runsModel(t)
	m, _ = press(m, "4", "/", "s", "l", "o", "w")
	if got := m.runs.visibleRuns(m.live.runs); len(got) != 1 || got[0].ID != "531fd2a9-2" {
		t.Fatalf("filter slow: %v", got)
	}

	if m, _ = press(m, "j"); m.runs.filter != "slowj" || m.screen != screenRuns {
		t.Errorf("filter %q screen %d", m.runs.filter, m.screen)
	}
	m, _ = press(m, "backspace", "enter")
	if m.runs.filtering || m.runs.filter != "slow" {
		t.Errorf("enter keeps the filter: %v %q", m.runs.filtering, m.runs.filter)
	}
	m, _ = press(m, "/", "esc", "a")
	got := m.runs.visibleRuns(m.live.runs)
	if m.runs.filter != "" || len(got) != 1 || got[0].ID != "0112dd6a-1" {
		t.Errorf("esc clears, a shows active only: %q %v", m.runs.filter, got)
	}
	if v := m.View().Content; !strings.Contains(v, "active only") {
		t.Error("active-only toggle not shown")
	}
}

func TestRunsOpenSelectsTheRun(t *testing.T) {
	m, _ := runsModel(t)
	if m.live.runID != "0112dd6a-1" {
		t.Fatalf("initial selection %q", m.live.runID)
	}
	m, cmd := press(m, "4", "G", "enter")
	if m.screen != screenOverview || m.live.runID != "9c41aa02-3" || cmd == nil {
		t.Fatalf("screen %d run %q cmd %v", m.screen, m.live.runID, cmd != nil)
	}
	m = drive(t, m, cmd)
	if m.live.runID != "9c41aa02-3" || m.live.snap.Run.ID != "9c41aa02-3" {
		t.Errorf("opened run not loaded: %q", m.live.runID)
	}
}

func TestRunDurationAndWhen(t *testing.T) {
	runs := sampleRuns()
	for i, want := range []string{"2m 01s", "1m 17s", "—"} {
		if got := duration(runs[i], testNow); got != want {
			t.Errorf("run %d duration %q, want %q", i, got, want)
		}
	}
	for d, want := range map[time.Duration]string{-time.Minute: "today 14:10", -20 * time.Hour: "yesterday 18:11", -72 * time.Hour: "2026-11-15 14:11"} {
		if got := when(testNow.Add(d), testNow); got != want {
			t.Errorf("when(%v) = %q, want %q", d, got, want)
		}
	}
}
