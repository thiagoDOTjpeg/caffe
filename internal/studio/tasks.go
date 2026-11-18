package studio

import (
	"fmt"
	"strings"
	"time"

	c "caffe/internal/contracts"
	"caffe/internal/service"
	"caffe/internal/studio/ui"
)

// boardColumns maps task statuses to Task Board columns (ux-specification §2.2).
var boardColumns = []struct {
	title    string
	statuses []c.TaskStatus
}{
	{"PENDING", []c.TaskStatus{c.TaskPending}},
	{"READY", []c.TaskStatus{c.TaskReady, c.TaskAssigned}},
	{"RUNNING", []c.TaskStatus{c.TaskRunning, c.TaskBlocked}},
	{"REVIEW", []c.TaskStatus{c.TaskReviewing}},
	{"DONE", []c.TaskStatus{c.TaskCompleted}},
	{"FAILED", []c.TaskStatus{c.TaskFailed, c.TaskCancelled}},
}

// columnOf is the board column of a status; unknown statuses go to PENDING
// rather than disappearing.
func columnOf(s c.TaskStatus) int {
	for i, col := range boardColumns {
		for _, st := range col.statuses {
			if st == s {
				return i
			}
		}
	}
	return 0
}

// board groups tasks into columns, plan order within each.
func board(tasks []service.TaskView) [][]service.TaskView {
	cols := make([][]service.TaskView, len(boardColumns))
	for _, t := range tasks {
		i := columnOf(t.Status)
		cols[i] = append(cols[i], t)
	}
	return cols
}

// tasksView is the Task Board state; the cursor is a task id (§3.6).
type tasksView struct{ cursor string }

// cursorPos is the column and row of the cursor; the first card when the
// cursor's task is gone.
func (v tasksView) cursorPos(cols [][]service.TaskView) (col, row int) {
	for i, cards := range cols {
		for j, t := range cards {
			if t.ID == v.cursor {
				return i, j
			}
		}
	}
	for i, cards := range cols {
		if len(cards) > 0 {
			return i, 0
		}
	}
	return 0, 0
}

// tasksMove moves the cursor dc columns (skipping empty ones) or dr rows.
func (m model) tasksMove(dc, dr int) model {
	if m.live.snap == nil {
		return m
	}
	cols := board(m.live.snap.Tasks)
	col, row := m.tasks.cursorPos(cols)
	if len(cols[col]) == 0 {
		return m
	}
	if dr != 0 {
		row = min(max(row+dr, 0), len(cols[col])-1)
	}
	for next := col + dc; dc != 0 && next >= 0 && next < len(cols); next += dc {
		if len(cols[next]) > 0 {
			col, row = next, min(row, len(cols[next])-1)
			break
		}
	}
	m.tasks.cursor = cols[col][row].ID
	return m
}

// columnWidths gives empty columns the width of their title and shares the
// rest among the others.
func columnWidths(cols [][]service.TaskView, width int) []int {
	ws := make([]int, len(cols))
	rest, full := width, 0
	for i, cards := range cols {
		if len(cards) == 0 {
			ws[i] = len(boardColumns[i].title) + 2
			rest -= ws[i]
		} else {
			full++
		}
	}
	for i, cards := range cols {
		if len(cards) > 0 {
			ws[i] = max(rest/full, 8)
		}
	}
	return ws
}

// card is the text of one task on the board: its title line and detail lines.
func card(t service.TaskView, keys map[string]service.TaskView, now time.Time) []string {
	b := taskBadge(t.Status)
	lines := []string{b.Symbol + " " + t.Key}
	switch t.Status {
	case c.TaskPending, c.TaskReady:
		var open []string
		for _, d := range t.DependsOn {
			if dep, ok := keys[d]; !ok || dep.Status != c.TaskCompleted {
				open = append(open, d)
			}
		}
		if len(open) > 0 {
			lines = append(lines, "  ⤷ "+strings.Join(open, ", "))
		}
	case c.TaskBlocked:
		lines = append(lines, "  ⚠ blocked")
	case c.TaskRunning, c.TaskAssigned, c.TaskReviewing:
		lines = append(lines, "  "+string(t.Role))
		if el := taskElapsed(t.Task, now); el != "" {
			lines = append(lines, fmt.Sprintf("  %s · %d/%d", el, t.Attempts, t.MaxAttempts))
		}
	case c.TaskFailed, c.TaskCancelled:
		if t.Error != "" {
			lines = append(lines, "  "+t.Error)
		}
	}
	return lines
}

func (m model) tasksScreen(width, height int) string {
	l := m.live
	if !l.loaded {
		return " Tasks\n\n loading…"
	}
	if l.snap == nil {
		return " Tasks\n\n no runs yet · start one with caffe run \"…\""
	}
	v, th, now := l.snap, m.theme, m.now()
	var out []string
	out = append(out, " Tasks · run "+short(v.Run.ID))
	if len(v.Tasks) == 0 {
		return strings.Join(append(out, "", " no tasks yet (the run is planning)"), "\n")
	}
	keys := map[string]service.TaskView{}
	for _, t := range v.Tasks {
		keys[t.Key] = t
	}
	cols := board(v.Tasks)
	ws := columnWidths(cols, width-1)
	ccol, crow := m.tasks.cursorPos(cols)

	// Each column is a list of cells (marker + text, plain) with a tone.
	type cell struct {
		text string
		tone ui.Tone
	}
	grid := make([][]cell, len(cols))
	rows := 0
	for i, cards := range cols {
		for j, t := range cards {
			for k, line := range card(t, keys, now) {
				marker := " "
				if k == 0 && i == ccol && j == crow {
					marker = "▸"
				}
				tone := ui.ToneMuted
				if k == 0 {
					tone = taskBadge(t.Status).Tone
				}
				grid[i] = append(grid[i], cell{marker + line, tone})
			}
		}
		rows = max(rows, len(grid[i]))
	}
	var head strings.Builder
	head.WriteString("  ") // titles line up with the card symbols, after the cursor marker
	for i, col := range boardColumns {
		head.WriteString(fmt.Sprintf("%-*s", ws[i], clip(col.title, ws[i]-1)))
	}
	out = append(out, head.String())
	boardRows := max(min(rows, height-len(out)-8), 1) // keep room for the details
	for r := range boardRows {
		var b strings.Builder
		b.WriteString(" ")
		for i := range cols {
			text, tone := "", ui.ToneNormal
			if r < len(grid[i]) {
				text, tone = grid[i][r].text, grid[i][r].tone
			}
			b.WriteString(th.Render(tone, fmt.Sprintf("%-*s", ws[i], clip(text, ws[i]-1))))
		}
		out = append(out, b.String())
	}
	if rows > boardRows {
		out = append(out, fmt.Sprintf(" … %d more lines", rows-boardRows))
	}
	out = append(out, "")
	if t, ok := m.cursorTask(); ok {
		out = append(out, taskDetails(t, keys, width)...)
	}
	return strings.Join(out, "\n")
}

// cursorTask is the task under the Task Board cursor.
func (m model) cursorTask() (service.TaskView, bool) {
	if m.live.snap == nil || len(m.live.snap.Tasks) == 0 {
		return service.TaskView{}, false
	}
	cols := board(m.live.snap.Tasks)
	col, row := m.tasks.cursorPos(cols)
	if len(cols[col]) == 0 {
		return service.TaskView{}, false
	}
	return cols[col][row], true
}

// taskDetails is the details block below the board.
func taskDetails(t service.TaskView, keys map[string]service.TaskView, width int) []string {
	rule := " ─ " + t.Key + " "
	out := []string{rule + strings.Repeat("─", max(width-len([]rune(rule))-1, 0))}
	if t.Title != "" {
		out = append(out, " "+clip(t.Title, width-2))
	}
	if t.Description != "" && t.Description != t.Title {
		out = append(out, " "+clip(t.Description, width-2))
	}
	facts := []string{"role " + string(t.Role), fmt.Sprintf("attempt %d/%d", t.Attempts, t.MaxAttempts)}
	if len(t.DependsOn) > 0 {
		var deps []string
		for _, d := range t.DependsOn {
			deps = append(deps, fmt.Sprintf("%s (%s)", d, keys[d].Status))
		}
		facts = append(facts, "depends on "+strings.Join(deps, ", "))
	}
	out = append(out, " "+clip(strings.Join(facts, " · "), width-2))
	if t.Reason != "" {
		out = append(out, " waiting  "+clip(t.Reason, width-12))
	}
	if t.Error != "" {
		out = append(out, " error    "+clip(t.Error, width-12))
	}
	if t.BranchName != "" {
		out = append(out, " branch   "+t.BranchName)
	}
	for i, crit := range t.AcceptanceCriteria {
		label := "         "
		if i == 0 {
			label = "criteria "
		}
		out = append(out, " "+label+" ☐ "+clip(crit, width-14))
	}
	return out
}
