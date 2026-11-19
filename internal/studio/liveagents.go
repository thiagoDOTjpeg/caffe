package studio

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	c "caffe/internal/contracts"
	"caffe/internal/service"
	"caffe/internal/studio/ui"
)

// Live agents (CC-15): the focused agent's terminal output, once a second,
// and the runtime status of the agents on screen, every two seconds. Both
// are read only while their view is visible (architecture §7), and runtime
// status only decorates: what is completed or failed comes from the
// database alone.

// liveEvery is the tick of both; status is read every other tick.
var liveEvery = time.Second

// outputLines is how much of an agent's terminal is read.
const outputLines = 200

type (
	liveTickMsg struct{ n int }
	outputMsg   struct {
		agentID string
		out     *service.AgentOutput
		err     error
	}
	rtStatusMsg struct {
		obs map[string]service.RuntimeObservation
	}
)

// agentOutput is the output pane of the Agent Room.
type agentOutput struct {
	on      bool
	agentID string // what out/err are about
	out     *service.AgentOutput
	err     error
}

// rtObs is the last runtime status of each agent, with when it was read.
type rtObs struct {
	at  time.Time
	obs map[string]service.RuntimeObservation
}

func liveTick(n int) tea.Cmd {
	return tea.Tick(liveEvery, func(time.Time) tea.Msg { return liveTickMsg{n} })
}

// roomVisible: the Agent Room is what the user is looking at.
func (m model) roomVisible() bool {
	return m.screen == screenAgents && m.inspect == nil && m.report == nil && m.overlay == overlayNone && !m.noProject
}

// agentsOnScreen are the agents whose cards or rows are visible now.
func (m model) agentsOnScreen() []service.AgentView {
	if m.live.snap == nil || m.inspect != nil || m.report != nil || m.overlay != overlayNone {
		return nil
	}
	if m.screen != screenAgents && m.screen != screenOverview {
		return nil
	}
	var out []service.AgentView
	for _, a := range m.live.snap.Agents {
		if agentRank(a.Visual) != 5 { // finished agents need no runtime status
			out = append(out, a)
		}
	}
	return out
}

func (m model) updateLiveAgents(msg tea.Msg) (model, tea.Cmd) {
	switch msg := msg.(type) {
	case liveTickMsg:
		cmds := []tea.Cmd{liveTick(msg.n + 1)}
		if m.output.on && m.roomVisible() {
			if a, ok := m.focusedAgent(); ok {
				src, id := m.src, a.ID
				cmds = append(cmds, func() tea.Msg {
					out, err := src.AgentOutput(context.Background(), id, outputLines)
					return outputMsg{id, out, err}
				})
			}
		}
		if agents := m.agentsOnScreen(); msg.n%2 == 0 && len(agents) > 0 {
			src := m.src
			cmds = append(cmds, func() tea.Msg {
				return rtStatusMsg{src.AgentRuntimeStatus(context.Background(), agents)}
			})
		}
		return m, tea.Batch(cmds...)
	case outputMsg:
		if m.output.on {
			m.output.agentID, m.output.out, m.output.err = msg.agentID, msg.out, msg.err
		}
	case rtStatusMsg:
		m.rt = rtObs{at: m.now(), obs: msg.obs} // the model's clock, as freshness is judged by it
	}
	return m, nil
}

// withRuntime returns agents with their visual state derived again using
// the last runtime status, when it is recent. Stored state wins for every
// finished execution (DeriveVisualState checks it first).
func (m model) withRuntime(agents []service.AgentView) []service.AgentView {
	if m.rt.obs == nil || m.now().Sub(m.rt.at) > 6*liveEvery { // stale: three missed reads
		return agents
	}
	out := make([]service.AgentView, len(agents))
	for i, a := range agents {
		if obs, ok := m.rt.obs[a.ID]; ok {
			a.Visual = service.DeriveVisualState(a.Status, latestExec(a), obs)
			a.Observation = obs
		}
		out[i] = a
	}
	return out
}

// focusedAgent is the agent under the Agent Room cursor.
func (m model) focusedAgent() (service.AgentView, bool) {
	if m.live.snap == nil || len(m.live.snap.Agents) == 0 {
		return service.AgentView{}, false
	}
	agents := sortedAgents(m.withRuntime(m.live.snap.Agents))
	return agents[m.agents.index(agents)], true
}

// outputPane renders the lower half of the Agent Room: the source is always
// named, live output says how fresh it is.
func (m model) outputPane(width, height int) []string {
	a, ok := m.focusedAgent()
	if !ok || height < 2 {
		return nil
	}
	o := m.output
	label := "loading…"
	var body []string
	switch {
	case o.agentID != a.ID:
	case o.err != nil:
		label = "live output unavailable"
		body = []string{" " + m.theme.Render(ui.ToneError, oneLine(o.err.Error()))}
	case o.out.Source == service.OutputLive:
		label = "live, refreshed " + compact(m.now().Sub(o.out.ObservedAt)) + " ago"
	case o.out.Source == service.OutputCaptured:
		label = "captured at end"
	default:
		label = "no output saved"
	}
	title := fmt.Sprintf(" ─ output · %s (%s) ", shortN(a.ID, 9), label)
	lines := []string{title + strings.Repeat("─", max(width-len([]rune(title))-1, 0))}
	if o.agentID == a.ID && o.err == nil && o.out != nil {
		text := strings.Split(strings.TrimRight(o.out.Text, "\n"), "\n")
		for _, l := range text {
			body = append(body, " "+l)
		}
	}
	room := height - 1
	if len(body) > room {
		body = body[len(body)-room:] // the newest lines
	}
	for _, l := range body {
		lines = append(lines, clip(l, width-1))
	}
	return lines
}

// latestExec is an agent's latest execution, nil for planning agents.
func latestExec(a service.AgentView) *c.Execution {
	if a.Execution == nil {
		return nil
	}
	return &a.Execution.Execution
}
