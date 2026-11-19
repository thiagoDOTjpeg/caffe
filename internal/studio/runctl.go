package studio

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	c "caffe/internal/contracts"
	"caffe/internal/service"
	"caffe/internal/studio/ui"
)

// Run controls (CC-14): new run, cancel, resume, clean. Each goes through
// the service; while one is in flight its target shows it and the same
// action is refused, and its result triggers an immediate refresh.

// cleanTarget is the in-flight key of a clean, which has no run.
const cleanTarget = "*clean"

type actionDoneMsg struct {
	target string // run id or cleanTarget
	label  string // "cancel run 0112dd6a"
	ok     string // toast text on success
	runID  string // a run to select (new run)
	err    error
}

// busy reports the in-flight action on target, if any.
func (m model) busy(target string) (string, bool) {
	a, ok := m.inflight[target]
	return a, ok
}

// perform runs fn as a command with target marked in flight.
func (m model) perform(target, doing, label string, fn func(ctx context.Context) actionDoneMsg) (model, tea.Cmd) {
	if a, ok := m.busy(target); ok {
		return m.failed(label, fmt.Errorf("already %s", a)), nil
	}
	if m.src == nil {
		return m, nil
	}
	in := map[string]string{target: doing}
	for k, v := range m.inflight {
		in[k] = v
	}
	m.inflight = in
	return m, func() tea.Msg {
		msg := fn(context.Background())
		msg.target, msg.label = target, label
		return msg
	}
}

func (m model) actionDone(msg actionDoneMsg) (model, tea.Cmd) {
	in := map[string]string{}
	for k, v := range m.inflight {
		if k != msg.target {
			in[k] = v
		}
	}
	m.inflight = in
	var cmds []tea.Cmd
	switch {
	case errors.Is(msg.err, service.ErrConflict):
		m = m.failed(msg.label, fmt.Errorf("%w; showing the current state", msg.err))
	case msg.err != nil:
		m = m.failed(msg.label, msg.err)
	default:
		var cmd tea.Cmd
		m, cmd = m.notify(msg.ok)
		cmds = append(cmds, cmd)
	}
	if msg.runID != "" {
		m.live.runID, m.live.snap = msg.runID, nil
		m.events = eventsPanel{focused: m.events.focused}
	}
	m, cmd := m.refreshNow()
	return m, tea.Batch(append(cmds, cmd)...)
}

// refreshNow reloads without waiting for the watcher: Studio's own actions
// show their effect at once (architecture §5.3).
func (m model) refreshNow() (model, tea.Cmd) {
	if m.src == nil {
		return m, nil
	}
	if m.live.pending {
		m.live.dirty = true
		return m, nil
	}
	m.live.pending = true
	return m, m.live.load(m.src)
}

// targetRun is the run an action applies to: the Runs cursor on the Runs
// screen, the selected run elsewhere.
func (m model) targetRun() (service.RunSummary, bool) {
	id := m.live.runID
	if m.screen == screenRuns && m.inspect == nil && m.report == nil {
		if runs := m.runs.visibleRuns(m.live.runs); len(runs) > 0 {
			id = runs[m.runs.cursorIndex(runs)].ID
		}
	}
	for _, r := range m.live.runs {
		if r.ID == id {
			return r, true
		}
	}
	return service.RunSummary{}, false
}

func (m model) cancelRun() (model, tea.Cmd) {
	r, ok := m.targetRun()
	if !ok {
		return m, nil
	}
	label := "cancel run " + short(r.ID)
	if r.Status.Terminal() {
		return m.failed(label, fmt.Errorf("it is already %s", r.Status)), nil
	}
	body := []string{"No agent is running."}
	if m.live.snap != nil && m.live.snap.Run.ID == r.ID {
		var names []string
		for _, a := range m.live.snap.Agents {
			if agentRank(a.Visual) != 5 && a.Name != "" {
				names = append(names, a.Name)
			}
		}
		switch len(names) {
		case 0:
		case 1:
			body[0] = fmt.Sprintf("1 agent will be stopped (%s).", names[0])
		default:
			body[0] = fmt.Sprintf("%d agents will be stopped (%s).", len(names), strings.Join(names, ", "))
		}
	} else {
		body[0] = "Its running agents will be stopped."
	}
	body = append(body, "Completed work and worktrees are kept.")
	src, id := m.src, r.ID
	return m.ask("Cancel run "+short(r.ID)+"?", "Stop the run", body, func(m model) (model, tea.Cmd) {
		return m.perform(id, "cancelling", label, func(ctx context.Context) actionDoneMsg {
			_, err := src.CancelRun(ctx, id)
			return actionDoneMsg{ok: "run " + short(id) + " cancelled", err: err}
		})
	}), nil
}

func (m model) resumeRun() (model, tea.Cmd) {
	r, ok := m.targetRun()
	if !ok {
		return m, nil
	}
	label := "resume run " + short(r.ID)
	if r.Status != c.RunInterrupted {
		return m.failed(label, fmt.Errorf("it is %s; only interrupted runs can be resumed", r.Status)), nil
	}
	src, id := m.src, r.ID
	return m.perform(id, "resuming", label, func(ctx context.Context) actionDoneMsg {
		err := src.ResumeRun(ctx, id)
		return actionDoneMsg{ok: "run " + short(id) + " resumed in the background", err: err}
	})
}

func (m model) cleanWorktrees() (model, tea.Cmd) {
	src := m.src
	return m.ask("Clean worktrees of finished runs?", "Clean", []string{
		"Worktrees of completed runs are removed; their branches are kept.",
		"Worktrees with uncommitted changes, and those of failed or cancelled runs, are kept.",
	}, func(m model) (model, tea.Cmd) {
		return m.perform(cleanTarget, "cleaning", "clean", func(ctx context.Context) actionDoneMsg {
			lines, err := src.Clean(ctx, false)
			removed, kept := 0, 0
			for _, l := range lines {
				if strings.HasPrefix(l, "removed") {
					removed++
				} else {
					kept++
				}
			}
			ok := "nothing to clean"
			if len(lines) > 0 {
				ok = fmt.Sprintf("clean: %d worktrees removed, %d kept (caffe clean lists them)", removed, kept)
			}
			return actionDoneMsg{ok: ok, err: err}
		})
	}), nil
}

// newRunForm is the "new run" overlay: a request and an optional plan file.
type newRunForm struct {
	request, plan string
	onPlan        bool // the plan field has focus
}

func (m model) openNewRun() (model, tea.Cmd) {
	if m.src == nil {
		return m, nil
	}
	m.form, m.overlay = &newRunForm{}, overlayNewRun
	return m, nil
}

func (m model) newRunKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := *m.form
	field := &f.request
	if f.onPlan {
		field = &f.plan
	}
	switch msg.String() {
	case "esc":
		m.overlay, m.form = overlayNone, nil
		return m, nil
	case "ctrl+q", "ctrl+c":
		return m.run("app.quit")
	case "tab", "shift+tab", "up", "down":
		f.onPlan = !f.onPlan
	case "backspace":
		if r := []rune(*field); len(r) > 0 {
			*field = string(r[:len(r)-1])
		}
	case "enter":
		req := strings.TrimSpace(f.request)
		if req == "" {
			f.onPlan = false
			m.form = &f
			return m, nil // nothing to start yet
		}
		m.overlay, m.form = overlayNone, nil
		src, plan := m.src, strings.TrimSpace(f.plan)
		return m.perform("*new", "starting", "new run", func(ctx context.Context) actionDoneMsg {
			id, err := src.StartRun(ctx, service.StartRunRequest{Request: req, PlanFile: plan})
			return actionDoneMsg{ok: "run " + short(id) + " started in the background; it keeps running if Studio exits", runID: id, err: err}
		})
	default:
		*field += msg.Text
	}
	m.form = &f
	return m, nil
}

func (m model) newRunView(width int) string {
	f := m.form
	cursor := func(on bool) string {
		if on {
			return m.theme.Render(ui.ToneActive, "▏")
		}
		return ""
	}
	w := max(width-16, 10)
	return strings.Join([]string{
		" New run",
		"",
		" Request    " + clip(f.request, w) + cursor(!f.onPlan),
		" Plan file  " + clip(f.plan, w) + cursor(f.onPlan) + m.theme.Render(ui.ToneMuted, "  (optional: a plan JSON instead of the planner)"),
		"",
		" enter starts · tab switches field · esc cancels",
		" The run is its own process: quitting Studio does not stop it.",
	}, "\n")
}
