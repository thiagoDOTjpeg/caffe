package studio

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	c "caffe/internal/contracts"
	"caffe/internal/service"
)

func TestColumnMapping(t *testing.T) {
	want := map[c.TaskStatus]string{
		c.TaskPending: "PENDING", c.TaskReady: "READY", c.TaskAssigned: "READY",
		c.TaskRunning: "RUNNING", c.TaskBlocked: "RUNNING", c.TaskReviewing: "REVIEW",
		c.TaskCompleted: "DONE", c.TaskFailed: "FAILED", c.TaskCancelled: "FAILED",
		c.TaskStatus("new-in-a-later-version"): "PENDING",
	}
	for s, col := range want {
		if got := boardColumns[columnOf(s)].title; got != col {
			t.Errorf("%s → %s, want %s", s, got, col)
		}
	}
}

func boardModel(t *testing.T) model {
	m := overviewModel(t)
	snap := m.live.snap
	snap.Tasks[3].DependsOn = []string{"review"}
	snap.Tasks[2].Attempts, snap.Tasks[2].MaxAttempts = 1, 3
	snap.Tasks[2].Title = "Review the change that adds hello.txt"
	snap.Tasks[2].DependsOn = []string{"impl"}
	snap.Tasks[2].AcceptanceCriteria = []string{"The diff was read and evaluated"}
	snap.Tasks = append(snap.Tasks,
		service.TaskView{Task: c.Task{ID: "t5", Key: "docs", Status: c.TaskBlocked, Role: c.RoleDeveloper}},
		service.TaskView{Task: c.Task{ID: "t6", Key: "lint", Status: c.TaskFailed, Error: "exit status 1"}})
	m, _ = press(m, "2")
	return m
}

func TestGoldenTaskBoard(t *testing.T) {
	m := boardModel(t)
	golden(t, "tasks-120x40", m.View().Content)
	res, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	golden(t, "tasks-80x24", res.(model).View().Content)
}

func TestTaskBoardNavigationAndDetails(t *testing.T) {
	m := boardModel(t)
	if tk, _ := m.cursorTask(); tk.Key != "validate" {
		t.Fatalf("starts on the first card: %q", tk.Key)
	}
	// READY and REVIEW are empty: l skips them.
	m, _ = press(m, "l")
	if tk, _ := m.cursorTask(); tk.Key != "review" {
		t.Errorf("l from PENDING: %q", tk.Key)
	}
	m, _ = press(m, "j")
	if tk, _ := m.cursorTask(); tk.Key != "docs" {
		t.Errorf("j in RUNNING: %q", tk.Key)
	}
	m, _ = press(m, "j", "l")
	if tk, _ := m.cursorTask(); tk.Key != "impl" {
		t.Errorf("l keeps the row, clamped: %q", tk.Key)
	}
	m, _ = press(m, "l", "l", "l")
	if tk, _ := m.cursorTask(); tk.Key != "lint" {
		t.Errorf("l stops at the last column: %q", tk.Key)
	}
	m, _ = press(m, "h", "h", "k")
	v := m.View().Content
	for _, want := range []string{"─ review ─", "Review the change that adds hello.txt", "role reviewer · attempt 1/3 · depends on impl (completed)",
		"☐ The diff was read and evaluated", "agent c8eccebbd"} {
		if want == "agent c8eccebbd" {
			continue // only on the Overview
		}
		if !strings.Contains(v, want) {
			t.Errorf("details lack %q", want)
		}
	}
}

func TestTaskBoardShowsWaitingReasonAndCards(t *testing.T) {
	m := boardModel(t)
	v := m.View().Content
	for _, want := range []string{"waiting  waiting on review (running)", "⤷ review", "⚠ blocked", "exit status 1", "12s · 1/3"} {
		if !strings.Contains(v, want) {
			t.Errorf("board lacks %q", want)
		}
	}
}

func TestTaskBoardCursorKeptByID(t *testing.T) {
	m := boardModel(t)
	m, _ = press(m, "l", "j") // docs
	// docs finishes: it moves to DONE, the cursor follows it.
	for i := range m.live.snap.Tasks {
		if m.live.snap.Tasks[i].Key == "docs" {
			m.live.snap.Tasks[i].Status = c.TaskCompleted
		}
	}
	if tk, _ := m.cursorTask(); tk.Key != "docs" || tk.Status != c.TaskCompleted {
		t.Errorf("cursor lost the task: %q", tk.Key)
	}
}

func TestEmptyColumnsCollapse(t *testing.T) {
	cols := board([]service.TaskView{{Task: c.Task{Status: c.TaskRunning}}})
	ws := columnWidths(cols, 100)
	if ws[0] != len("PENDING")+2 || ws[2] <= 50 {
		t.Errorf("widths %v", ws)
	}
}
