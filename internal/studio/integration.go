package studio

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"caffe/internal/service"
	"caffe/internal/studio/ui"
)

type integration struct {
	runID    string
	items    []service.IntegrationItem
	err      error
	loaded   bool
	cursor   int
	note     *string
	decision string
}

type (
	integrationMsg struct {
		runID string
		items []service.IntegrationItem
		err   error
	}
	previewMsg struct {
		p   *service.MergePreview
		err error
	}
	integratedMsg struct {
		ok  string
		err error
	}
)

func (m model) loadIntegration() (model, tea.Cmd) {
	if m.src == nil || m.live.runID == "" {
		return m, nil
	}
	if m.integ.runID != m.live.runID {
		m.integ = integration{runID: m.live.runID}
	}
	src, id := m.src, m.live.runID
	return m, func() tea.Msg {
		items, err := src.Integration(context.Background(), id)
		return integrationMsg{id, items, err}
	}
}

func (m model) updateIntegration(msg tea.Msg) (model, tea.Cmd) {
	switch msg := msg.(type) {
	case integrationMsg:
		if msg.runID != m.integ.runID {
			return m, nil
		}
		m.integ.items, m.integ.err, m.integ.loaded = msg.items, msg.err, true
		m.integ.cursor = max(0, min(m.integ.cursor, len(msg.items)-1))
	case previewMsg:
		if msg.err != nil {
			return m.failed("merge preview", msg.err), nil
		}
		p := msg.p
		body := []string{"Into " + p.Target + " in the main checkout, without pushing:", "  " + p.Command}
		if !p.Ready() {
			why := strings.Join(p.Blockers, "; ")
			if len(p.Conflicts) > 0 {
				why += " (" + strings.Join(p.Conflicts, ", ") + ")"
			}
			return m.failed("merge "+p.TaskKey, fmt.Errorf("refused: %s", why)), nil
		}
		src, id := m.src, p.TaskID
		return m.ask("Merge "+p.Branch+"?", "Merge", append(body, "No conflicts. The branch and its worktree are kept."),
			func(m model) (model, tea.Cmd) {
				return m, func() tea.Msg {
					p, err := src.Merge(context.Background(), id)
					if err != nil {
						return integratedMsg{err: err}
					}
					return integratedMsg{ok: fmt.Sprintf("merged %s into %s (not pushed)", p.Branch, p.Target)}
				}
			}), nil
	case integratedMsg:
		if msg.err != nil {
			m = m.failed("integration", msg.err)
		} else {
			var cmd tea.Cmd
			m, cmd = m.notify(msg.ok)
			m, load := m.loadIntegration()
			return m, tea.Batch(cmd, load)
		}
		return m.loadIntegration()
	}
	return m, nil
}

func (m model) integItem() (service.IntegrationItem, bool) {
	if m.integ.cursor < len(m.integ.items) {
		return m.integ.items[m.integ.cursor], true
	}
	return service.IntegrationItem{}, false
}

func (m model) integMove(d int) model {
	m.integ.cursor = max(0, min(m.integ.cursor+d, len(m.integ.items)-1))
	return m
}

func (m model) integDecide(decision string) (model, tea.Cmd) {
	if _, ok := m.integItem(); !ok {
		return m, nil
	}
	s := ""
	m.integ.note, m.integ.decision = &s, decision
	return m, nil
}

func (m model) integNoteKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := *m.integ.note
	done, cancel := lineInput(&s, msg)
	switch {
	case cancel:
		m.integ.note = nil
	case done:
		m.integ.note = nil
		it, ok := m.integItem()
		if !ok {
			return m, nil
		}
		src, d := m.src, m.integ.decision
		return m, func() tea.Msg {
			err := src.Decide(context.Background(), it.Task.ID, d, s)
			return integratedMsg{ok: fmt.Sprintf("%s: %s recorded", it.Task.Key, d), err: err}
		}
	default:
		m.integ.note = &s
	}
	return m, nil
}

func (m model) integMerge() (model, tea.Cmd) {
	it, ok := m.integItem()
	if !ok || m.src == nil {
		return m, nil
	}
	src := m.src
	return m, func() tea.Msg {
		p, err := src.PreviewMerge(context.Background(), it.Task.ID)
		return previewMsg{p, err}
	}
}

func (m model) integDiff() (model, tea.Cmd) {
	it, ok := m.integItem()
	if !ok {
		return m, nil
	}
	return m.openDiff(service.TaskView{Task: it.Task})
}

func (m model) integrationScreen(width int) string {
	var b strings.Builder
	b.WriteString(" Integration")
	if m.live.runID != "" {
		b.WriteString(" · run " + short(m.live.runID))
	}
	b.WriteString("\n")
	i := m.integ
	switch {
	case m.live.runID == "":
		b.WriteString("\n  No run selected.")
		return b.String()
	case i.err != nil:
		b.WriteString("\n  " + m.theme.Render(ui.ToneError, "✗ "+i.err.Error()))
		return b.String()
	case !i.loaded:
		b.WriteString("\n  loading…")
		return b.String()
	case len(i.items) == 0:
		b.WriteString("\n  This run has no developer branches.")
		return b.String()
	}
	target := i.items[0].Target
	b.WriteString(m.theme.Render(ui.ToneMuted, "  branches are merged into "+target+" in the main checkout, only when approved; nothing is pushed") + "\n\n")
	fmt.Fprintf(&b, "  %-16s %-30s %-10s %-10s %-6s %s\n", "TASK", "CHANGES", "REVIEW", "CHECKS", "MANUAL", "DECISION")
	for n, it := range i.items {
		marker := " "
		if n == i.cursor {
			marker = "▸"
		}
		dec, tone := "—", ui.ToneMuted
		if it.Decision != nil {
			dec = it.Decision.Decision
			switch dec {
			case service.DecisionApprove:
				tone = ui.ToneOK
			case service.DecisionReject:
				tone = ui.ToneError
			case service.DecisionMerged:
				tone = ui.ToneActive
			}
		}
		if it.Merged && dec != service.DecisionMerged {
			dec += " · in " + target
		}
		stat := strings.TrimSpace(it.Stat)
		if stat == "" {
			stat = "no changes"
		}
		fmt.Fprintf(&b, " %s %-16s %-30s %-10s %-10s %-6d %s\n", marker, clip(it.Task.Key, 16), clip(stat, 30), it.Review, it.Validation, it.Manual, m.theme.Render(tone, dec))
	}
	it, _ := m.integItem()
	b.WriteString("\n  " + m.theme.Render(ui.ToneMuted, it.Task.BranchName) + "\n")
	for _, d := range it.History {
		note := ""
		if d.Note != "" {
			note = " — " + d.Note
		}
		fmt.Fprintf(&b, "  %s %s by %s%s\n", d.At.Local().Format("01-02 15:04"), d.Decision, d.Author, clip(note, max(width-40, 10)))
	}
	if i.note != nil {
		fmt.Fprintf(&b, "\n  %s note: %s%s  %s\n", i.decision, *i.note, m.theme.Render(ui.ToneActive, "▏"), m.theme.Render(ui.ToneMuted, "enter records · esc cancels"))
	}
	b.WriteString("\n" + m.theme.Render(ui.ToneMuted, "  a approve · r reject · m merge (preview first) · d diff"))
	return b.String()
}
