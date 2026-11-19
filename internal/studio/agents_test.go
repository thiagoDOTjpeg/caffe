package studio

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	c "caffe/internal/contracts"
	"caffe/internal/service"
)

func agentsModel(t *testing.T) model {
	m := overviewModel(t)
	unknown := service.ExecutionView{Execution: c.Execution{Status: c.ExecUnknown}}
	m.live.snap.Agents = []service.AgentView{
		{Agent: c.Agent{ID: "caa8bdb64-1", Role: c.RoleDeveloper, Kind: "claude"}, TaskKey: "impl", Visual: service.VisualCompleted,
			Execution: &service.ExecutionView{}, Elapsed: 39 * time.Second},
		{Agent: c.Agent{ID: "c8eccebbd-2", Role: c.RoleReviewer, Kind: "claude"}, TaskKey: "review", Visual: service.VisualWorking,
			Execution: &service.ExecutionView{}, Elapsed: 12 * time.Second},
		{Agent: c.Agent{ID: "bbbbbbbbb-3", Role: c.RoleDeveloper, Kind: "codex"}, TaskKey: "docs", Visual: service.VisualBlocked},
		{Agent: c.Agent{ID: "fffffffff-4", Role: c.RoleValidator, Kind: "claude"}, TaskKey: "lint", Visual: service.VisualFailed,
			Execution: &unknown, Elapsed: 3 * time.Second},
		{Agent: c.Agent{ID: "ddddddddd-5", Role: c.RolePlanner, Kind: "claude"}, Visual: service.VisualStarting},
	}
	m.live.feed = append(m.live.feed, c.Event{Seq: 9, RunID: m.live.runID, AgentID: "c8eccebbd-2", Type: "task.running",
		CreatedAt: time.Date(2026, 11, 18, 14, 11, 5, 0, time.Local)})
	m, _ = press(m, "3")
	return m
}

func TestAgentSortOrder(t *testing.T) {
	m := agentsModel(t)
	var got []string
	for _, a := range sortedAgents(m.live.snap.Agents) {
		got = append(got, string(a.Visual))
	}
	if want := "blocked working starting completed failed"; strings.Join(got, " ") != want {
		t.Errorf("order %v, want %s", got, want)
	}
}

func TestGoldenAgentRoom(t *testing.T) {
	m := agentsModel(t)
	golden(t, "agents-120x40", m.View().Content)
	res, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	golden(t, "agents-80x24", res.(model).View().Content)
}

func TestAgentCardsContent(t *testing.T) {
	v := agentsModel(t).View().Content
	for _, want := range []string{"3 live · 2 done", "● WORKING  12s", "reviewer · claude", "task  review",
		"ws    caffe/0112dd6a/impl", "last  task.running 14:11:05", "✗ OUTCOME UNKNOWN  3s", "⚠ BLOCKED", "╔"} {
		if !strings.Contains(v, want) {
			t.Errorf("agent room lacks %q", want)
		}
	}
}

func TestAgentCursorMovesAndKeepsID(t *testing.T) {
	m := agentsModel(t)
	if n := perRow(m.layout().Main.W); n != 3 {
		t.Fatalf("per row %d", n)
	}
	m, _ = press(m, "l")
	if m.agents.cursor != "c8eccebbd-2" {
		t.Errorf("l: %q", m.agents.cursor)
	}
	m, _ = press(m, "j")
	if m.agents.cursor != "fffffffff-4" {
		t.Errorf("j moves a row: %q", m.agents.cursor)
	}
	m, _ = press(m, "j", "l", "l")
	if m.agents.cursor != "fffffffff-4" {
		t.Errorf("clamped at the last card: %q", m.agents.cursor)
	}

	m, _ = press(m, "k", "k", "l")
	m.live.snap.Agents[1].Visual = service.VisualCompleted
	if agents := sortedAgents(m.live.snap.Agents); agents[m.agents.index(agents)].ID != "c8eccebbd-2" {
		t.Error("cursor lost its agent after a re-sort")
	}
}
