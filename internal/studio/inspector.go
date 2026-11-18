package studio

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	c "caffe/internal/contracts"
	"caffe/internal/service"
	"caffe/internal/studio/ui"
)

// inspectorMax bounds what the inspector reads of a prompt or output file.
const inspectorMax = 256 << 10

var inspectorTabs = []string{"Report", "Prompt", "Output", "Artifacts", "Events"}

// inspector is the Execution Inspector (ux-specification §2.5), open over
// the current screen until closed.
type inspector struct {
	id     string
	d      *service.ExecutionDetail
	prompt *service.ArtifactContent
	output *service.ArtifactContent
	err    error
	tab    int
	scroll int
}

type inspectedMsg struct {
	id     string
	d      *service.ExecutionDetail
	prompt *service.ArtifactContent
	output *service.ArtifactContent
	err    error
}

// loadExecution reads everything the inspector shows. A missing prompt or
// output is not an error: the tab says so.
func loadExecution(src source, id string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		msg := inspectedMsg{id: id}
		if msg.d, msg.err = src.Execution(ctx, id); msg.err != nil {
			return msg
		}
		msg.prompt, _ = src.ReadPrompt(ctx, id, inspectorMax)
		if a := outputArtifact(msg.d); a != nil {
			msg.output, _ = src.ReadArtifact(ctx, a.ID, inspectorMax)
		}
		return msg
	}
}

// outputArtifact is the execution's full output: the terminal log of an
// agent (beside its prompt), or the log of a validation command.
func outputArtifact(d *service.ExecutionDetail) *c.Artifact {
	var found *c.Artifact
	for _, a := range d.Artifacts {
		switch {
		case a.Type == c.ArtifactLog && d.AgentID != "" && dirOf(a.Path) == dirOf(d.Prompt):
			return a
		case a.Type == c.ArtifactTestResult && d.AgentID == "":
			found = a // the latest
		}
	}
	return found
}

// dirOf is the directory part of a slash or backslash path.
func dirOf(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[:i]
	}
	return ""
}

// openInspector opens the inspector on an execution.
func (m model) openInspector(id string) (model, tea.Cmd) {
	if id == "" || m.src == nil {
		return m, nil
	}
	m.inspect, m.report = &inspector{id: id}, nil
	return m, loadExecution(m.src, id)
}

// latestExecution is the id of a task's latest execution, or "".
func latestExecution(v *service.RunView, taskID string) string {
	id := ""
	if v != nil {
		for _, e := range v.Executions {
			if e.TaskID == taskID {
				id = e.ID // oldest first
			}
		}
	}
	return id
}

func (m model) updateInspector(msg inspectedMsg) (model, tea.Cmd) {
	if m.inspect == nil || m.inspect.id != msg.id {
		return m, nil // closed or moved on
	}
	in := *m.inspect
	in.d, in.prompt, in.output, in.err = msg.d, msg.prompt, msg.output, msg.err
	m.inspect = &in
	return m, nil
}

// refreshInspector reloads a running execution after a data change.
func (m model) refreshInspector() tea.Cmd {
	if m.inspect == nil || m.inspect.d == nil || m.inspect.d.CompletedAt != nil || m.src == nil {
		return nil
	}
	return loadExecution(m.src, m.inspect.id)
}

func (m model) inspectorTab(delta int) model {
	in := *m.inspect
	in.tab = (in.tab + delta + len(inspectorTabs)) % len(inspectorTabs)
	in.scroll = 0
	m.inspect = &in
	return m
}

func (m model) inspectorScroll(delta int) model {
	in := *m.inspect
	in.scroll = max(in.scroll+delta, 0)
	m.inspect = &in
	return m
}

func (m model) inspectorScreen(width, height int) string {
	in := m.inspect
	if in.err != nil {
		return fmt.Sprintf(" Execution %s\n\n %s", short(in.id), m.theme.Render(ui.ToneError, in.err.Error()))
	}
	if in.d == nil {
		return loadingText("Execution " + short(in.id))
	}
	d := in.d
	head := executionHeader(d, m)
	var tabs []string
	for i, t := range inspectorTabs {
		if i == in.tab {
			t = "[" + t + "]"
		}
		tabs = append(tabs, t)
	}
	head = append(head, " "+strings.Join(tabs, "  ")+"   (h/l tab · j/k scroll · esc close)", "")
	var body []string
	switch inspectorTabs[in.tab] {
	case "Report":
		body = reportLines(d, m.theme)
	case "Prompt":
		body = contentLines(in.prompt, "no prompt recorded")
	case "Output":
		body = contentLines(in.output, "")
		if in.output == nil {
			body = outputTail(d)
		}
	case "Artifacts":
		for _, a := range d.Artifacts {
			body = append(body, fmt.Sprintf(" %-12s %s", a.Type, a.Path), "   "+a.Description)
		}
		if len(body) == 0 {
			body = []string{" no artifacts yet"}
		}
	case "Events":
		body = m.executionEvents(d)
	}
	room := max(height-len(head), 1)
	scroll := min(in.scroll, max(len(body)-room, 0))
	end := min(scroll+room, len(body))
	for i, l := range body[scroll:end] {
		body[scroll+i] = clip(l, width-1)
	}
	return strings.Join(append(head, body[scroll:end]...), "\n")
}

// executionHeader is the inspector's first lines, with the observed exit
// code of a validation command labelled as such.
func executionHeader(d *service.ExecutionDetail, m model) []string {
	first := fmt.Sprintf(" Execution %s · %s · attempt %d · %s", short(d.ID), d.TaskKey, d.Attempt, d.Role)
	if d.Agent != nil && d.Agent.Kind != "" {
		first += " · " + d.Agent.Kind
	}
	when := d.StartedAt.Local().Format("15:04:05")
	if d.CompletedAt != nil {
		when += fmt.Sprintf(" → %s (%s)", d.CompletedAt.Local().Format("15:04:05"), compact(d.CompletedAt.Sub(d.StartedAt)))
	} else {
		when += fmt.Sprintf(" (%s so far)", compact(m.now().Sub(d.StartedAt)))
	}
	second := fmt.Sprintf(" status %s · %s", d.Status, when)
	if d.Agent != nil {
		second += " · agent " + d.Agent.Name
	}
	if d.ExitCode != nil && d.ExitCodeObserved {
		second += fmt.Sprintf(" · exit %d · observed (%s)", *d.ExitCode, c.LabelObserved)
	}
	out := []string{first, second}
	if a := d.Agent; a != nil && (a.WorkspaceID != "" || a.PaneID != "" || a.RuntimeSessionID != "") {
		out = append(out, fmt.Sprintf(" runtime  workspace %s · pane %s · terminal %s", a.WorkspaceID, a.PaneID, shortN(a.RuntimeSessionID, 12)))
	}
	if d.Error != "" {
		out = append(out, " "+m.theme.Render(ui.ToneError, "error  "+d.Error))
	}
	return out
}

// reportLines renders the execution's report. Test results in it are claims
// unless the orchestrator ran the commands (contracts.LabelClaimed).
func reportLines(d *service.ExecutionDetail, th ui.Theme) []string {
	r := d.Report
	if r == nil {
		return []string{" no report delivered"}
	}
	var out []string
	if r.Approved != nil {
		if *r.Approved {
			out = append(out, " "+th.Render(ui.ToneOK, "✓ approved (reviewer recommendation)"))
		} else {
			out = append(out, " "+th.Render(ui.ToneError, "✗ rejected (reviewer recommendation)"))
		}
	}
	if r.Status != "" {
		out = append(out, " Status   "+r.Status)
	}
	out = append(out, " Summary  "+oneLine(r.Summary))
	list := func(title string, items []string) {
		if len(items) > 0 {
			out = append(out, " "+title)
			for _, it := range items {
				out = append(out, "  · "+oneLine(it))
			}
		}
	}
	if r.Recommendation != "" {
		out = append(out, " Recommendation  "+oneLine(r.Recommendation))
	}
	list("Relevant files", r.RelevantFiles)
	list("Risks", r.Risks)
	list("Modified files", r.ModifiedFiles)
	if d.ClaimsUnverified && len(r.Tests) > 0 {
		out = append(out, " Tests")
		for _, t := range r.Tests {
			out = append(out, fmt.Sprintf("  %s → exit %d · %s", t.Command, t.ExitCode, c.LabelClaimed))
		}
	}
	if len(r.Findings) > 0 {
		out = append(out, " Findings")
		for _, f := range r.Findings {
			loc := f.File
			if f.Line > 0 {
				loc = fmt.Sprintf("%s:%d", f.File, f.Line)
			}
			out = append(out, fmt.Sprintf("  %-7s %s  %s", f.Severity, loc, oneLine(f.Description)))
		}
	}
	list("Limitations", r.Limitations)
	return out
}

func contentLines(a *service.ArtifactContent, empty string) []string {
	if a == nil || a.Text == "" {
		if empty == "" {
			return nil
		}
		return []string{" " + empty}
	}
	lines := strings.Split(strings.TrimRight(a.Text, "\n"), "\n")
	if a.Truncated {
		lines = append(lines, fmt.Sprintf(" … showing the first %d of %d bytes", len(a.Text), a.Size))
	}
	return lines
}

// outputTail is the stored output tail, when the full log is not available.
func outputTail(d *service.ExecutionDetail) []string {
	if d.Output == "" {
		if d.CompletedAt == nil {
			return []string{" no output captured yet (live output comes with the Agent Room)"}
		}
		return []string{" no output captured"}
	}
	return append([]string{" (stored tail)"}, strings.Split(strings.TrimRight(d.Output, "\n"), "\n")...)
}

// executionEvents are the feed events of the execution's task or agent.
func (m model) executionEvents(d *service.ExecutionDetail) []string {
	var out []string
	for _, e := range m.live.feed {
		if e.RunID != d.RunID || (e.TaskID != d.TaskID && (d.AgentID == "" || e.AgentID != d.AgentID)) {
			continue
		}
		line := fmt.Sprintf(" %s  %-18s", e.CreatedAt.Local().Format("15:04:05"), e.Type)
		for _, k := range []string{"from", "to", "reason", "error", "message", "command", "exit_code"} {
			if v, ok := e.Payload[k]; ok && fmt.Sprint(v) != "" {
				line += fmt.Sprintf(" %s=%v", k, oneLine(fmt.Sprint(v)))
			}
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		return []string{" no events in memory for this execution"}
	}
	return out
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
