package studio

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	c "caffe/internal/contracts"
	"caffe/internal/service"
)

func overviewModel(t *testing.T) model {
	f := newFake()
	run := service.RunSummary{ID: "0112dd6a-1", Request: "Add a hello file to the project", Status: c.RunReviewing,
		BaseCommit: "7f5d944aa", CreatedAt: testNow.Add(-2 * time.Minute), StartedAt: at(-121 * time.Second),
		HeartbeatAt: at(-time.Second), OwnerAlive: true}
	f.runs = []service.RunSummary{run}
	f.snaps = map[string]*service.RunView{run.ID: {Run: run, Seq: 4,
		Tasks: []service.TaskView{
			{Task: c.Task{ID: "t1", Key: "research", Role: c.RoleResearcher, Status: c.TaskCompleted, StartedAt: at(-120 * time.Second), CompletedAt: at(-65 * time.Second)}},
			{Task: c.Task{ID: "t2", Key: "impl", Role: c.RoleDeveloper, Status: c.TaskCompleted, BranchName: "caffe/0112dd6a/impl", StartedAt: at(-64 * time.Second), CompletedAt: at(-25 * time.Second)}},
			{Task: c.Task{ID: "t3", Key: "review", Role: c.RoleReviewer, Status: c.TaskRunning, AssignedAgentID: "c8eccebbd-77", StartedAt: at(-12 * time.Second)}},
			{Task: c.Task{ID: "t4", Key: "validate", Role: c.RoleValidator, Status: c.TaskPending}, Reason: "waiting on review (running)"},
		},
		Agents: []service.AgentView{{Agent: c.Agent{ID: "c8eccebbd-77", Role: c.RoleReviewer, Kind: "claude"}, Visual: service.VisualWorking}},
	}}
	f.events = []c.Event{
		{Seq: 1, RunID: run.ID, TaskID: "t2", Type: "task.completed", CreatedAt: testNow.Add(-25 * time.Second)},
		{Seq: 2, RunID: run.ID, TaskID: "t2", Type: c.EventDiffCollected, CreatedAt: testNow.Add(-25 * time.Second)},
		{Seq: 3, RunID: run.ID, TaskID: "t3", Type: "task.running", CreatedAt: testNow.Add(-12 * time.Second)},
		{Seq: 4, RunID: run.ID, Type: c.EventWarning, CreatedAt: testNow.Add(-5 * time.Second)},
	}
	m := liveModel(f)
	m.now = func() time.Time { return testNow }
	m = drive(t, m, m.Init())
	res, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return res.(model)
}

func TestGoldenOverview(t *testing.T) {
	m := overviewModel(t)
	golden(t, "overview-120x40", m.View().Content)
	res, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	golden(t, "overview-80x24", res.(model).View().Content)
}

func TestOverviewFields(t *testing.T) {
	v := plain(overviewModel(t).View().Content)
	for _, want := range []string{
		"run 0112dd6a · reviewing ── 2/4 tasks ── ● runtime 0.9.5", // header
		"Add a hello file to the project", "14:09:29 (2m 01s)", "Phase  ↻ reviewing", "Base  7f5d944",
		"■■□□  2 completed · 1 running · 1 pending",
		"caffe/0112dd6a/impl", "agent c8eccebbd", "waiting on review (running)", "55s",
		"● working", "claude",
		"run heartbeat 1s ago", "⚠ warnings 1",
		"task.running", "review",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("overview lacks %q", want)
		}
	}
}

func TestOverviewEmptyAndHealthStates(t *testing.T) {
	f := newFake()
	f.runs, f.snaps, f.events = nil, nil, nil
	m := drive(t, liveModel(f), liveModel(f).Init())
	m.width, m.height = 120, 40
	if v := m.View().Content; !strings.Contains(v, "No runs yet") {
		t.Error("no empty state")
	}
	if got := (health{checked: true, err: errors.New("dial: refused")}).badge().Label; got != "runtime: dial: refused" {
		t.Errorf("unreachable: %q", got)
	}
	r := service.RunSummary{Status: c.RunExecuting, HeartbeatAt: at(-40 * time.Second)}
	if b := heartbeat(r, testNow); b.Label != "no heartbeat for 40s" {
		t.Errorf("dead owner: %q", b.Label)
	}
}

func TestHealthIsRecheckedOnATick(t *testing.T) {
	m := liveModel(newFake())
	res, cmd := m.Update(healthMsg{err: errors.New("down")})
	if m = res.(model); !m.health.checked || cmd == nil {
		t.Fatal("no next check scheduled")
	}
	_, cmd = m.Update(healthTickMsg{})
	if msg := cmd().(healthMsg); msg.err != nil || msg.info.Version != "0.9.5" {
		t.Errorf("recheck: %+v", msg)
	}
}
