package studio

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	c "caffe/internal/contracts"
	"caffe/internal/service"
)

// runsView is the Runs screen state (ux-specification §2.4). The cursor is
// kept by run id, so refreshes never move it (§3.6).
type runsView struct {
	cursor     string
	filter     string
	filtering  bool // keys go to the filter text
	activeOnly bool
}

// visibleRuns applies the filter: a case-insensitive match on id, status or
// request, and the active toggle.
func (v runsView) visibleRuns(runs []service.RunSummary) []service.RunSummary {
	q := strings.ToLower(v.filter)
	var out []service.RunSummary
	for _, r := range runs {
		if v.activeOnly && !active(r) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(r.ID+" "+string(r.Status)+" "+r.Request), q) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// cursorIndex is the cursor's row, or 0 when its run is not visible.
func (v runsView) cursorIndex(runs []service.RunSummary) int {
	for i, r := range runs {
		if r.ID == v.cursor {
			return i
		}
	}
	return 0
}

// move shifts the cursor by delta rows, clamped; delta beyond the list
// jumps to an end (g/G).
func (m model) runsMove(delta int) model {
	runs := m.runs.visibleRuns(m.live.runs)
	if len(runs) == 0 {
		return m
	}
	i := min(max(m.runs.cursorIndex(runs)+delta, 0), len(runs)-1)
	m.runs.cursor = runs[i].ID
	return m
}

// runsOpen makes the run under the cursor the selected run and shows it.
func (m model) runsOpen() (model, tea.Cmd) {
	runs := m.runs.visibleRuns(m.live.runs)
	if len(runs) == 0 {
		return m, nil
	}
	id := runs[m.runs.cursorIndex(runs)].ID
	m.screen = screenOverview
	if id == m.live.runID || m.src == nil {
		return m, nil
	}
	m.live.runID, m.live.snap = id, nil
	m.events = eventsPanel{focused: m.events.focused} // filters and pages belong to a run
	if m.live.pending {
		m.live.dirty = true
		return m, nil
	}
	m.live.pending = true
	return m, m.live.load(m.src)
}

// runsFilterKey edits the filter while it has focus: enter keeps it, esc
// clears it.
func (m model) runsFilterKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.runs.filtering = false
	case "esc":
		m.runs.filtering, m.runs.filter = false, ""
	case "ctrl+q", "ctrl+c":
		return m.run("app.quit")
	case "backspace":
		if r := []rune(m.runs.filter); len(r) > 0 {
			m.runs.filter = string(r[:len(r)-1])
		}
	default:
		m.runs.filter += msg.Text
	}
	return m, nil
}

func (m model) runsScreen(width int) string {
	var b strings.Builder
	head := " Runs"
	switch {
	case m.runs.filtering:
		head += fmt.Sprintf("   / %s▏", m.runs.filter)
	case m.runs.filter != "":
		head += fmt.Sprintf("   / %s", m.runs.filter)
	}
	if m.runs.activeOnly {
		head += "   active only"
	}
	b.WriteString(head + "\n")
	runs := m.runs.visibleRuns(m.live.runs)
	if len(runs) == 0 {
		if len(m.live.runs) == 0 {
			b.WriteString("\n no runs yet · start one with caffe run")
		} else {
			b.WriteString("\n no run matches")
		}
		return b.String()
	}
	reqW := max(width-60, 10)
	fmt.Fprintf(&b, "   %-9s %-13s %-17s %-9s %s\n", "RUN", "STATUS", "CREATED", "DURATION", "REQUEST")
	cur, now := m.runs.cursorIndex(runs), m.now()
	for i, r := range runs {
		marker := " "
		if i == cur {
			marker = "▸"
		}
		st := runBadge(r.Status)
		status := m.theme.Render(st.Tone, fmt.Sprintf("%-13s", st.Symbol+" "+st.Label))
		fmt.Fprintf(&b, " %s %-9s %s %-17s %-9s %s\n", marker, short(r.ID), status,
			when(r.CreatedAt, now), duration(r, now), clip(r.Request, reqW))
	}
	b.WriteString("\n [enter] open  [/] filter  [a] active only")
	return b.String()
}

// active: the run can still make progress (interrupted runs need a resume).
func active(r service.RunSummary) bool {
	return !r.Status.Terminal() && r.Status != c.RunInterrupted
}

// when is a short local timestamp relative to now.
func when(t, now time.Time) string {
	t, now = t.Local(), now.Local()
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	switch yd := now.AddDate(0, 0, -1); {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return "today " + t.Format("15:04")
	case t.Year() == yd.Year() && t.YearDay() == yd.YearDay():
		return "yesterday " + t.Format("15:04")
	default:
		return t.Format("2006-01-02 15:04")
	}
}

// duration of a run: started to completed, or to now while it runs; — when
// it never started or was interrupted without finishing.
func duration(r service.RunSummary, now time.Time) string {
	if r.StartedAt == nil {
		return "—"
	}
	end := now
	switch {
	case r.CompletedAt != nil:
		end = *r.CompletedAt
	case !r.OwnerAlive:
		return "—"
	}
	return compact(end.Sub(*r.StartedAt))
}

func compact(d time.Duration) string {
	d = d.Round(time.Second)
	h, mnt, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %02dm", h, mnt)
	case mnt > 0:
		return fmt.Sprintf("%dm %02ds", mnt, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

func clip(s string, n int) string {
	if n < 1 {
		return ""
	}
	s = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(s) // one line, indentation kept
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
