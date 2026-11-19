package studio

import (
	"fmt"
	"sort"
	"strings"

	c "caffe/internal/contracts"
	"caffe/internal/service"
)

// agentRank orders Agent Room cards (ux-specification §2.3): what needs a
// human first, then live work, then what is starting, then finished agents.
func agentRank(v service.VisualState) int {
	switch v {
	case service.VisualBlocked, service.VisualLost:
		return 0
	case service.VisualWorking, service.VisualWaiting:
		return 1
	case service.VisualInterrupted:
		return 2
	case service.VisualStarting:
		return 3
	case service.VisualIdle:
		return 4
	default: // completed, failed, cancelled, stopped
		return 5
	}
}

// sortedAgents is the card order; ties keep the snapshot order.
func sortedAgents(agents []service.AgentView) []service.AgentView {
	out := append([]service.AgentView(nil), agents...)
	sort.SliceStable(out, func(i, j int) bool { return agentRank(out[i].Visual) < agentRank(out[j].Visual) })
	return out
}

// agentsView is the Agent Room state; the cursor is an agent id (§3.6).
type agentsView struct{ cursor string }

const cardW = 31 // outer width of an agent card

func (v agentsView) index(agents []service.AgentView) int {
	for i, a := range agents {
		if a.ID == v.cursor {
			return i
		}
	}
	return 0
}

// perRow is how many cards fit side by side in width cells.
func perRow(width int) int { return max((width-1)/(cardW+1), 1) }

// agentsMove moves the cursor by delta cards (±1 across, ±perRow down).
func (m model) agentsMove(delta func(perRow int) int) model {
	if m.live.snap == nil || len(m.live.snap.Agents) == 0 {
		return m
	}
	agents := sortedAgents(m.withRuntime(m.live.snap.Agents))
	i := m.agents.index(agents) + delta(perRow(m.layout().Main.W))
	m.agents.cursor = agents[min(max(i, 0), len(agents)-1)].ID
	return m
}

// lastEvents is the newest feed event per agent id.
func (l live) lastEvents() map[string]c.Event {
	out := map[string]c.Event{}
	for _, e := range l.feed {
		if e.AgentID != "" {
			out[e.AgentID] = e
		}
	}
	return out
}

// agentCard is the text inside one card, cardW-2 cells wide.
func agentCard(a service.AgentView, tasks map[string]service.TaskView, last map[string]c.Event) []string {
	w := cardW - 4
	b := agentBadge(a.Visual)
	state := strings.ToUpper(b.Label)
	if a.Visual == service.VisualFailed && a.Execution != nil && a.Execution.Status == c.ExecUnknown {
		state = "OUTCOME UNKNOWN" // failed: no report, agent gone
	}
	elapsed := ""
	if a.Execution != nil {
		elapsed = compact(a.Elapsed)
	}
	role := string(a.Role)
	if a.Kind != "" {
		role += " · " + a.Kind
	}
	lines := []string{
		shortN(a.ID, 9),
		role,
		fmt.Sprintf("%s %s  %s", b.Symbol, state, elapsed),
	}
	field := func(label, v string) {
		if v == "" {
			v = "—"
		}
		lines = append(lines, label+"  "+v)
	}
	t := tasks[a.TaskKey]
	field("task", a.TaskKey)
	field("ws  ", t.BranchName)
	if e, ok := last[a.ID]; ok {
		field("last", e.Type+" "+e.CreatedAt.Local().Format("15:04:05"))
	} else {
		field("last", "")
	}
	for i := range lines {
		lines[i] = fmt.Sprintf("%-*s", w, clip(lines[i], w))
	}
	return lines
}

// box draws lines in a single (or, when focused, double) border (§3.6).
func box(lines []string, focused bool) []string {
	h, v, tl, tr, bl, br := "─", "│", "┌", "┐", "└", "┘"
	if focused {
		h, v, tl, tr, bl, br = "═", "║", "╔", "╗", "╚", "╝"
	}
	inner := cardW - 2
	out := []string{tl + strings.Repeat(h, inner) + tr}
	for _, l := range lines {
		out = append(out, v+" "+l+" "+v)
	}
	return append(out, bl+strings.Repeat(h, inner)+br)
}

func (m model) agentsScreen(width, height int) string {
	l := m.live
	if !l.loaded {
		return loadingText("Agents")
	}
	if l.snap == nil {
		return noRunsText("Agents")
	}
	agents := sortedAgents(m.withRuntime(l.snap.Agents))
	live, done := 0, 0
	for _, a := range agents {
		if agentRank(a.Visual) == 5 {
			done++
		} else {
			live++
		}
	}
	title := " Agents · run " + short(l.snap.Run.ID)
	counts := fmt.Sprintf("%d live · %d done", live, done)
	out := []string{fmt.Sprintf("%-*s%s", max(width-len(counts)-1, len(title)+2), title, counts)}
	if len(agents) == 0 {
		return strings.Join(append(out, "", " no agents yet"), "\n")
	}
	tasks := map[string]service.TaskView{}
	for _, t := range l.snap.Tasks {
		tasks[t.Key] = t
	}
	last := l.lastEvents()
	n, cur := perRow(width), m.agents.index(agents)
	cardH := len(box(agentCard(agents[0], tasks, last), false))
	var pane []string
	if m.output.on {
		pane = m.outputPane(width, height/2)
		height -= len(pane)
	}
	rowsFit := max((height-len(out))/cardH, 1)
	// Keep the cursor's row on screen.
	first := max(cur/n-rowsFit+1, 0)
	for r := first; r < first+rowsFit && r*n < len(agents); r++ {
		var cards [][]string
		for i := r * n; i < min((r+1)*n, len(agents)); i++ {
			cards = append(cards, m.styledCard(agents[i], tasks, last, i == cur))
		}
		for line := range cardH {
			var b strings.Builder
			for i, cd := range cards {
				if i > 0 {
					b.WriteString(" ")
				}
				b.WriteString(cd[line])
			}
			out = append(out, " "+b.String())
		}
	}
	if hidden := len(agents) - min((first+rowsFit)*n, len(agents)); hidden > 0 {
		out = append(out, fmt.Sprintf(" … %d more agents", hidden))
	}
	if !m.output.on {
		out = append(out, " [o] output  [i] inspect")
	}
	return strings.Join(append(out, pane...), "\n")
}

// styledCard colours the state line of a card by its tone, after layout.
func (m model) styledCard(a service.AgentView, tasks map[string]service.TaskView, last map[string]c.Event, focused bool) []string {
	lines := agentCard(a, tasks, last)
	lines[2] = m.theme.Render(agentBadge(a.Visual).Tone, lines[2])
	return box(lines, focused)
}
