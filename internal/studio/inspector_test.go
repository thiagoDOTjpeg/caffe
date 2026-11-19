package studio

import (
	"strings"
	"testing"
	"time"

	c "caffe/internal/contracts"
	"caffe/internal/service"
)

func inspectorModel(t *testing.T) (model, *fakeSource) {
	m := boardModel(t)
	f := m.src.(*fakeSource)
	yes, zero, one := true, 0, 1
	dir := "/p/.caffe/artifacts/run/review/exreview"
	f.set(func(f *fakeSource) {
		f.execs = map[string]*service.ExecutionDetail{
			"exreview-1": {RunID: m.live.runID, ExecutionView: service.ExecutionView{TaskKey: "review", Role: c.RoleReviewer,
				Execution: c.Execution{ID: "exreview-1", TaskID: "t3", AgentID: "c8eccebbd-77", Attempt: 1, Status: c.ExecCompleted,
					Prompt: dir + "/prompt.md", StartedAt: testNow.Add(-27 * time.Second), CompletedAt: at(0)}},
				Agent: &c.Agent{Name: "c8eccebbd-reviewer", Kind: "claude", WorkspaceID: "w3", PaneID: "w3:p1", RuntimeSessionID: "term_66aa77bb88cc"},
				Artifacts: []*c.Artifact{
					{ID: "a-log", Type: c.ArtifactLog, Path: dir + "/output.txt", Description: "terminal output"},
					{ID: "a-rep", Type: c.ArtifactReport, Path: dir + "/report.json", Description: "reviewer report"},
				},
				Report: &c.Report{Summary: "Read changes.diff and checked it against the worktree", Approved: &yes,
					Tests:    []c.CommandRun{{Command: "go test ./...", ExitCode: 0}},
					Findings: []c.Finding{{Severity: "info", File: "hello.txt", Line: 1, Description: "left as is"}}},
				ClaimsUnverified: true},
			"exvalidate-1": {RunID: m.live.runID, ExecutionView: service.ExecutionView{TaskKey: "validate", Role: c.RoleValidator, ExitCodeObserved: true,
				Execution: c.Execution{ID: "exvalidate-1", TaskID: "t4", Attempt: 1, Status: c.ExecFailed, Prompt: "go vet ./...",
					StartedAt: testNow.Add(-3 * time.Second), CompletedAt: at(0), ExitCode: &one, Error: "exit status 1"}},
				Artifacts: []*c.Artifact{{ID: "a-test", Type: c.ArtifactTestResult, Path: "/p/x/vet.log"}},
				Report:    &c.Report{Summary: "vet failed", Tests: []c.CommandRun{{Command: "go vet ./...", ExitCode: zero}}}},
		}
		f.prompts = map[string]string{"exreview-1": "You are the reviewer.\nReview the change.", "exvalidate-1": "go vet ./..."}
		f.artifacts = map[string]string{"a-log": "● Read changes.diff\n● Checked the worktree", "a-test": "vet: x.go:1: bad"}
	})
	m.live.snap.Executions = []service.ExecutionView{
		{Execution: c.Execution{ID: "exreview-0", TaskID: "t3"}},
		{Execution: c.Execution{ID: "exreview-1", TaskID: "t3"}},
		{Execution: c.Execution{ID: "exvalidate-1", TaskID: "t4"}},
	}
	return m, f
}

func openOn(t *testing.T, m model, keys ...string) model {
	t.Helper()
	m, cmd := press(m, keys...)
	if m.inspect == nil {
		t.Fatal("inspector not opened")
	}
	return drive(t, m, cmd)
}

func TestInspectorFromTaskBoardShowsReportWithClaimLabel(t *testing.T) {
	m, _ := inspectorModel(t)
	m = openOn(t, m, "l", "enter")
	if m.inspect.id != "exreview-1" {
		t.Fatalf("opened %q", m.inspect.id)
	}
	golden(t, "inspector-report-120x40", m.View().Content)
	v := m.View().Content
	for _, want := range []string{"Execution exreview · review · attempt 1 · reviewer · claude",
		"status completed · 14:11:03 → 14:11:30 (27s) · agent c8eccebbd-reviewer",
		"runtime  workspace w3 · pane w3:p1 · terminal term_66aa77b",
		"[Report]", "✓ approved (reviewer recommendation)",
		"go test ./... → exit 0 · " + c.LabelClaimed, "info    hello.txt:1  left as is"} {
		if !strings.Contains(v, want) {
			t.Errorf("report tab lacks %q", want)
		}
	}
}

func TestInspectorTabs(t *testing.T) {
	m, _ := inspectorModel(t)
	m = openOn(t, m, "l", "enter")
	for _, tc := range []struct {
		key, want string
	}{
		{"l", "Review the change."},
		{"l", "● Checked the worktree"},
		{"l", "reviewer report"},
		{"l", "task.running"},
		{"l", "[Report]"},
		{"h", "[Events]"},
	} {
		m, _ = press(m, tc.key)
		if v := m.View().Content; !strings.Contains(v, tc.want) {
			t.Errorf("after %s: lacks %q", tc.key, tc.want)
		}
	}
	m, _ = press(m, "esc")
	if m.inspect != nil || m.screen != screenTasks {
		t.Error("esc does not return to the board")
	}
}

func TestInspectorValidationCommandIsObserved(t *testing.T) {
	m, _ := inspectorModel(t)
	m = openOn(t, m, "enter")
	v := m.View().Content
	if !strings.Contains(v, "exit 1 · observed ("+c.LabelObserved+")") {
		t.Error("observed exit code not labelled")
	}
	if strings.Contains(v, c.LabelClaimed) {
		t.Error("a validator's orchestrator-run tests shown as claims")
	}
	m, _ = press(m, "l", "l")
	if v := m.View().Content; !strings.Contains(v, "vet: x.go:1: bad") {
		t.Error("validation log not shown as output")
	}
}

func TestInspectorFromAgentRoomAndErrors(t *testing.T) {
	m, f := inspectorModel(t)
	m.live.snap.Agents[0].Execution = &service.ExecutionView{Execution: c.Execution{ID: "exreview-1"}}
	m, _ = press(m, "3")
	m = openOn(t, m, "i")
	if m.inspect.d == nil || m.inspect.d.ID != "exreview-1" {
		t.Fatal("agent's execution not loaded")
	}
	m, _ = press(m, "esc")
	f.set(func(f *fakeSource) { delete(f.execs, "exreview-1") })
	m = openOn(t, m, "i")
	if v := m.View().Content; !strings.Contains(v, "not found") {
		t.Error("missing execution not reported")
	}
}

func TestInspectorReloadsRunningExecutionOnly(t *testing.T) {
	m, _ := inspectorModel(t)
	m = openOn(t, m, "l", "enter")
	if m.refreshInspector() != nil {
		t.Error("a finished execution is reloaded")
	}
	d := *m.inspect.d
	d.CompletedAt = nil
	m.inspect.d = &d
	if m.refreshInspector() == nil {
		t.Error("a running execution is not reloaded")
	}

	m, _ = m.updateInspector(inspectedMsg{id: "other"})
	if m.inspect.d == nil {
		t.Error("stale result applied")
	}
}

func TestOutputArtifactPicksTheExecutionsOwnLog(t *testing.T) {
	d := &service.ExecutionDetail{ExecutionView: service.ExecutionView{Execution: c.Execution{AgentID: "a", Prompt: "/r/t/ex2/prompt.md"}},
		Artifacts: []*c.Artifact{{ID: "old", Type: c.ArtifactLog, Path: "/r/t/ex1/output.txt"}, {ID: "own", Type: c.ArtifactLog, Path: "/r/t/ex2/output.txt"}}}
	if a := outputArtifact(d); a == nil || a.ID != "own" {
		t.Errorf("picked %+v", a)
	}
}
