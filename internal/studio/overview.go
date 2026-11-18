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

// healthEvery is how often the runtime is asked whether it is reachable
// (architecture §7).
var healthEvery = 10 * time.Second

// health is the last runtime Capabilities result.
type health struct {
	info    *service.RuntimeInfo
	err     error
	checked bool
}

type (
	healthMsg struct {
		info *service.RuntimeInfo
		err  error
	}
	healthTickMsg struct{}
)

func checkRuntime(src source) tea.Cmd {
	return func() tea.Msg {
		info, err := src.Capabilities(context.Background())
		return healthMsg{info, err}
	}
}

func (m model) updateHealth(msg tea.Msg) (model, tea.Cmd) {
	switch msg := msg.(type) {
	case healthMsg:
		m.health = health{info: msg.info, err: msg.err, checked: true}
		return m, tea.Tick(healthEvery, func(time.Time) tea.Msg { return healthTickMsg{} })
	case healthTickMsg:
		return m, checkRuntime(m.src)
	}
	return m, nil
}

// runtimeBadge is the runtime indicator of the header and the health block.
func (h health) badge() badge {
	switch {
	case !h.checked:
		return badge{"◌", "runtime checking…", ui.ToneMuted}
	case h.err != nil:
		return badge{"✗", "runtime unreachable", ui.ToneError}
	case h.info.Version != "":
		return badge{"●", "runtime " + h.info.Version, ui.ToneOK}
	default:
		return badge{"●", "runtime reachable", ui.ToneOK}
	}
}

// padded renders b padded to w cells before colouring, so columns line up.
func padded(b badge, th ui.Theme, w int) string {
	return th.Render(b.Tone, fmt.Sprintf("%-*s", w, b.Symbol+" "+b.Label))
}

// headerText is the header line; it names the selected run when there is one.
func (m model) headerText() string {
	h := "caffè ─ " + m.project.Name
	v := m.live.snap
	if m.src == nil || v == nil {
		return h
	}
	done := 0
	for _, t := range v.Tasks {
		if t.Status == c.TaskCompleted {
			done++
		}
	}
	b := m.health.badge()
	return fmt.Sprintf("%s ─ run %s · %s ── %d/%d tasks ── %s %s", h, short(v.Run.ID), v.Run.Status, done, len(v.Tasks), b.Symbol, b.Label)
}

// overviewScreen is ux-specification §2.1 for the selected run.
func (m model) overviewScreen(height int) string {
	l := m.live
	if !l.loaded {
		return " Overview\n\n loading…"
	}
	if l.snap == nil {
		return " Overview\n\n no runs yet · start one with caffe run \"…\""
	}
	v, now, th := l.snap, m.now(), m.theme
	r := v.Run
	var lines []string
	add := func(format string, a ...any) { lines = append(lines, fmt.Sprintf(format, a...)) }

	add(" Overview")
	add(" Request  %s", clip(r.Request, 200))
	started := "not started"
	if r.StartedAt != nil {
		started = fmt.Sprintf("%s (%s)", r.StartedAt.Local().Format("15:04:05"), duration(r, now))
	}
	add(" Started  %-26s Phase  %-13s Base  %s", started, r.Status, shortN(r.BaseCommit, 7))
	if r.Error != "" {
		add(" %s", th.Render(ui.ToneError, "Error    "+clip(r.Error, 200)))
	}
	add("")

	counts := map[c.TaskStatus]int{}
	bar := ""
	for _, t := range v.Tasks {
		counts[t.Status]++
		if t.Status == c.TaskCompleted {
			bar += "■"
		} else {
			bar += "□"
		}
	}
	var parts []string
	for _, s := range []c.TaskStatus{c.TaskCompleted, c.TaskRunning, c.TaskReviewing, c.TaskAssigned, c.TaskReady,
		c.TaskPending, c.TaskBlocked, c.TaskFailed, c.TaskCancelled} {
		if n := counts[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, s))
		}
	}
	if len(v.Tasks) == 0 {
		add(" TASKS  none yet (planning)")
	} else {
		add(" TASKS  %s  %s", bar, strings.Join(parts, " · "))
	}
	for _, t := range v.Tasks {
		add("  %s %-12s %-11s %-8s %s", padded(taskBadge(t.Status), th, 12), clip(t.Key, 12), t.Role, taskElapsed(t.Task, now), taskNote(t))
	}
	add("")

	add(" AGENTS")
	if len(v.Agents) == 0 {
		add("  none yet")
	}
	for _, a := range v.Agents {
		add("  %s %-10s %-10s %s", padded(agentBadge(a.Visual), th, 14), shortN(a.ID, 9), a.Role, a.Kind)
	}
	add("")

	add(" HEALTH")
	add("  %s", padded(heartbeat(r, now), th, 0))
	add("  %s", padded(m.health.badge(), th, 0))
	warn := badge{"○", "warnings 0", ui.ToneMuted}
	if n := l.count(r.ID, c.EventWarning); n > 0 {
		warn = badge{"⚠", fmt.Sprintf("warnings %d", n), ui.ToneWarn}
	}
	add("  %s", padded(warn, th, 0))
	add("")

	add(" RECENT EVENTS")
	if room := height - len(lines); room > 0 {
		ev := l.feedLines(room)
		if len(ev) == 0 {
			ev = []string{"  none yet"}
		}
		lines = append(lines, ev...)
	}
	return strings.Join(lines, "\n")
}

func taskElapsed(t c.Task, now time.Time) string {
	if t.StartedAt == nil {
		return ""
	}
	end := now
	if t.CompletedAt != nil {
		end = *t.CompletedAt
	}
	return compact(end.Sub(*t.StartedAt))
}

// taskNote is the most useful extra fact about a task.
func taskNote(t service.TaskView) string {
	switch {
	case t.Error != "":
		return clip(t.Error, 60)
	case (t.Status == c.TaskRunning || t.Status == c.TaskAssigned || t.Status == c.TaskReviewing) && t.AssignedAgentID != "":
		return "agent " + shortN(t.AssignedAgentID, 9)
	case t.Reason != "":
		return clip(t.Reason, 60)
	default:
		return t.BranchName
	}
}

// heartbeat says how recently the run's owner process reported in.
func heartbeat(r service.RunSummary, now time.Time) badge {
	switch {
	case r.Status.Terminal():
		return badge{"○", "run finished", ui.ToneMuted}
	case r.HeartbeatAt == nil:
		return badge{"○", "no heartbeat yet", ui.ToneMuted}
	case r.OwnerAlive:
		return badge{"●", "run heartbeat " + compact(now.Sub(*r.HeartbeatAt)) + " ago", ui.ToneOK}
	default:
		return badge{"⚠", "no heartbeat for " + compact(now.Sub(*r.HeartbeatAt)), ui.ToneWarn}
	}
}

// count is the number of events of type typ for run in the feed (the newest
// feedCap events, so a lower bound on a long run).
func (l live) count(runID, typ string) int {
	n := 0
	for _, e := range l.feed {
		if e.RunID == runID && e.Type == typ {
			n++
		}
	}
	return n
}

func shortN(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
