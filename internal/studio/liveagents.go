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

var liveEvery = time.Second

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

type agentOutput struct {
	on      bool
	agentID string
	out     *service.AgentOutput
	err     error
}

type rtObs struct {
	at  time.Time
	obs map[string]service.RuntimeObservation
}

func liveTick(n int) tea.Cmd {
	return tea.Tick(liveEvery, func(time.Time) tea.Msg { return liveTickMsg{n} })
}

func (m model) roomVisible() bool {
	return m.screen == screenAgents && m.inspect == nil && m.report == nil && m.overlay == overlayNone && !m.noProject
}

func (m model) agentsOnScreen() []service.AgentView {
	if m.live.snap == nil || m.inspect != nil || m.report != nil || m.overlay != overlayNone {
		return nil
	}
	if m.screen != screenAgents && m.screen != screenOverview {
		return nil
	}
	var out []service.AgentView
	for _, a := range m.live.snap.Agents {
		if agentRank(a.Visual) != 5 {
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
		m.rt = rtObs{at: m.now(), obs: msg.obs}
	}
	return m, nil
}

func (m model) withRuntime(agents []service.AgentView) []service.AgentView {
	if m.rt.obs == nil || m.now().Sub(m.rt.at) > 6*liveEvery {
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

func (m model) focusedAgent() (service.AgentView, bool) {
	if m.live.snap == nil || len(m.live.snap.Agents) == 0 {
		return service.AgentView{}, false
	}
	agents := sortedAgents(m.withRuntime(m.live.snap.Agents))
	return agents[m.agents.index(agents)], true
}

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
		body = body[len(body)-room:]
	}
	for _, l := range body {
		lines = append(lines, clip(l, width-1))
	}
	return lines
}

func latestExec(a service.AgentView) *c.Execution {
	if a.Execution == nil {
		return nil
	}
	return &a.Execution.Execution
}
