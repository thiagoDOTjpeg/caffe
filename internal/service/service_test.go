package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"caffe/internal/config"
	c "caffe/internal/contracts"
	"caffe/internal/core"
	"caffe/internal/persistence"
	"caffe/internal/runtime"
)

func TestDeriveVisualState(t *testing.T) {
	ex := func(s c.ExecutionStatus) *c.Execution { return &c.Execution{Status: s} }
	seen := func(st runtime.AgentStatus) RuntimeObservation {
		return RuntimeObservation{Known: true, Found: true, Status: st}
	}
	gone := RuntimeObservation{Known: true, Found: false}
	cases := []struct {
		name   string
		agent  string
		exec   *c.Execution
		rt     RuntimeObservation
		expect VisualState
	}{
		{"cancelled wins over runtime", "stopped", ex(c.ExecCancelled), seen(runtime.StatusWorking), VisualCancelled},
		{"failed", "stopped", ex(c.ExecFailed), RuntimeObservation{}, VisualFailed},
		{"unknown outcome is failed", "stopped", ex(c.ExecUnknown), RuntimeObservation{}, VisualFailed},
		{"completed", "stopped", ex(c.ExecCompleted), RuntimeObservation{}, VisualCompleted},
		{"running but gone is lost", "running", ex(c.ExecRunning), gone, VisualLost},
		{"interrupted and gone is lost", "running", ex(c.ExecInterrupted), gone, VisualLost},
		{"blocked", "running", ex(c.ExecRunning), seen(runtime.StatusBlocked), VisualBlocked},
		{"working", "running", ex(c.ExecRunning), seen(runtime.StatusWorking), VisualWorking},
		{"idle without report is waiting, not done", "running", ex(c.ExecRunning), seen(runtime.StatusIdle), VisualWaiting},
		{"runtime 'done' is still waiting", "running", ex(c.ExecRunning), seen(runtime.StatusDone), VisualWaiting},
		{"interrupted, no runtime info", "running", ex(c.ExecInterrupted), RuntimeObservation{}, VisualInterrupted},
		{"interrupted but alive and working", "running", ex(c.ExecInterrupted), seen(runtime.StatusWorking), VisualWorking},
		{"running, not asked the runtime", "running", ex(c.ExecRunning), RuntimeObservation{}, VisualStarting},
		{"starting row", "starting", nil, RuntimeObservation{}, VisualStarting},
		{"runtime unknown status while running", "running", ex(c.ExecRunning), seen(runtime.StatusUnknown), VisualStarting},
		{"planning agent finished", "stopped", nil, RuntimeObservation{}, VisualStopped},
		{"live agent, no execution", "running", nil, seen(runtime.StatusIdle), VisualIdle},
	}
	for _, tc := range cases {
		if got := DeriveVisualState(tc.agent, tc.exec, tc.rt); got != tc.expect {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.expect)
		}
	}
}

func TestTaskReason(t *testing.T) {
	mk := func(key string, st c.TaskStatus, deps ...string) *c.Task {
		return &c.Task{ID: key, Key: key, Status: st, Dependencies: deps}
	}
	a := mk("a", c.TaskCompleted)
	b := mk("b", c.TaskRunning, "a")
	d := mk("d", c.TaskPending, "a", "b")
	r := mk("r", c.TaskReady, "a")
	bl := mk("bl", c.TaskBlocked)
	f := mk("f", c.TaskFailed)
	f.Error = "validation failed"
	g, err := core.NewGraph([]*c.Task{a, b, d, r, bl, f})
	if err != nil {
		t.Fatal(err)
	}
	for task, want := range map[*c.Task]string{
		a:  "",
		d:  "waiting on b (running)", // a is completed, so only b is listed
		r:  "queued: at most 3 agents run at once",
		bl: "agent is waiting for human input",
		f:  "validation failed",
	} {
		if got := TaskReason(task, g, 3, c.RunExecuting); got != want {
			t.Errorf("%s: got %q, want %q", task.Key, got, want)
		}
	}
	if got := TaskReason(d, g, 3, c.RunInterrupted); got != "run interrupted; waiting on b (running)" {
		t.Errorf("interrupted pending: %q", got)
	}
	if got := TaskReason(b, g, 3, c.RunInterrupted); got != "run interrupted" {
		t.Errorf("interrupted running: %q", got)
	}
	if got := TaskReason(a, g, 3, c.RunInterrupted); got != "" {
		t.Errorf("finished tasks are not affected by the interruption: %q", got)
	}
}

// fixture builds a project directory with a database written through the
// normal (writing) store, then gives back a Service on a read-only store, as
// a consumer process would have.
type fixture struct {
	svc      *Service
	w        *persistence.Store
	cfg      *config.Config
	artifact string // path of a real artifact file
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default("demo")
	cfg.Project.Path = root
	cfg.Git.WorktreeDirectory = filepath.Join(root, ".caffe", "worktrees")
	cfg.Persistence.Path = filepath.Join(root, ".caffe", "caffe.db")

	w, err := persistence.Open(ctx, cfg.Persistence.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	now := persistence.Now()
	hb := now
	run := &c.Run{ID: "11111111-0000-0000-0000-000000000000", ProjectPath: root, UserRequest: "add the feature", Status: c.RunExecuting,
		CreatedAt: now, StartedAt: &now, HeartbeatAt: &hb, Metadata: map[string]string{"base_commit": "abc123"}}
	must(t, w.CreateRun(ctx, run))
	tasks := []*c.Task{
		{ID: "t-research", RunID: run.ID, Key: "research", Title: "R", Description: "d", Role: c.RoleResearcher, Status: c.TaskCompleted, AcceptanceCriteria: []string{"x"}, MaxAttempts: 3, CreatedAt: now},
		{ID: "t-impl", RunID: run.ID, Key: "impl", Title: "I", Description: "d", Role: c.RoleDeveloper, Status: c.TaskRunning, Dependencies: []string{"t-research"}, AcceptanceCriteria: []string{"x"}, MaxAttempts: 3, CreatedAt: now},
		{ID: "t-validate", RunID: run.ID, Key: "validate", Title: "V", Description: "d", Role: c.RoleValidator, Status: c.TaskPending, Dependencies: []string{"t-impl"}, AcceptanceCriteria: []string{"x"}, MaxAttempts: 3, CreatedAt: now},
	}
	must(t, w.CreateTasks(ctx, tasks))

	must(t, w.SaveAgent(ctx, &c.Agent{ID: "ag-plan", RunID: run.ID, Name: "c1-planner", Kind: "claude", Role: c.RolePlanner, Status: "running"}))
	must(t, w.SaveAgent(ctx, &c.Agent{ID: "ag-plan", RunID: run.ID, Name: "c1-planner", Kind: "claude", Role: c.RolePlanner, Status: "stopped"}))
	must(t, w.SaveAgent(ctx, &c.Agent{ID: "ag-res", RunID: run.ID, Name: "c2-researcher", Kind: "agy", Role: c.RoleResearcher, Status: "stopped"}))
	must(t, w.SaveAgent(ctx, &c.Agent{ID: "ag-dev", RunID: run.ID, Name: "c3-developer", Kind: "claude", Role: c.RoleDeveloper, Status: "running", CurrentTaskID: "t-impl"}))

	done := now.Add(-time.Minute)
	started := done.Add(-30 * time.Second)
	must(t, w.SaveExecution(ctx, &c.Execution{ID: "ex-research", TaskID: "t-research", AgentID: "ag-res", Attempt: 1, Status: c.ExecCompleted, StartedAt: started, CompletedAt: &done}))
	must(t, w.SaveExecution(ctx, &c.Execution{ID: "ex-developer", TaskID: "t-impl", AgentID: "ag-dev", Attempt: 1, Status: c.ExecRunning, StartedAt: now.Add(-10 * time.Second)}))
	zero := 0
	must(t, w.SaveExecution(ctx, &c.Execution{ID: "ex-command", TaskID: "t-research", Attempt: 1, Status: c.ExecCompleted, Prompt: "go test ./...", StartedAt: started, CompletedAt: &done, ExitCode: &zero}))

	artDir := filepath.Join(cfg.ArtifactDir(), run.ID, "research")
	must(t, os.MkdirAll(artDir, 0o755))
	art := filepath.Join(artDir, "report.json")
	must(t, os.WriteFile(art, []byte(`{"summary":"found it","token":"api_key=supersecret123"}`), 0o644))
	must(t, w.AddArtifact(ctx, &c.Artifact{ID: "art-1", RunID: run.ID, TaskID: "t-research", Type: c.ArtifactReport, Path: art}))

	r, err := persistence.OpenReadOnly(ctx, cfg.Persistence.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return &fixture{svc: New(cfg, r, runtime.NewFake(), nil), w: w, cfg: cfg, artifact: art}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunSnapshotView(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	runs, err := f.svc.Runs(ctx, true)
	if err != nil || len(runs) != 1 || !runs[0].OwnerAlive || runs[0].BaseCommit != "abc123" {
		t.Fatalf("runs = %+v, %v", runs, err)
	}

	v, err := f.svc.RunSnapshot(ctx, "1111") // prefix
	if err != nil {
		t.Fatal(err)
	}
	tasks := map[string]TaskView{}
	for _, tv := range v.Tasks {
		tasks[tv.Key] = tv
	}
	if got := tasks["validate"]; got.Reason != "waiting on impl (running)" || len(got.DependsOn) != 1 || got.DependsOn[0] != "impl" {
		t.Errorf("validate = reason %q deps %v", got.Reason, got.DependsOn)
	}
	if v.Tasks[0].Key != "research" {
		t.Error("tasks not in plan order")
	}

	agents := map[string]AgentView{}
	for _, a := range v.Agents {
		agents[string(a.Role)] = a
	}
	if a := agents["planner"]; a.Visual != VisualStopped || a.Execution != nil {
		t.Errorf("planner = %s", a.Visual)
	}
	if a := agents["researcher"]; a.Visual != VisualCompleted || a.TaskKey != "research" || a.Elapsed != 30*time.Second {
		t.Errorf("researcher = %s task=%s elapsed=%s", a.Visual, a.TaskKey, a.Elapsed)
	}
	// Stored state only: a running execution cannot be told apart from a
	// starting one without asking the runtime.
	if a := agents["developer"]; a.Visual != VisualStarting || a.Elapsed < 10*time.Second {
		t.Errorf("developer = %s elapsed=%s", a.Visual, a.Elapsed)
	}

	observed := map[string]bool{}
	for _, e := range v.Executions {
		observed[e.ID] = e.ExitCodeObserved
	}
	if !observed["ex-command"] || observed["ex-research"] {
		t.Errorf("observed = %v", observed)
	}

	// The cursor continues exactly where the snapshot stops.
	if evs, _ := f.svc.EventsSince(ctx, v.Seq, "", 100); len(evs) != 0 {
		t.Errorf("%d events after a fresh snapshot", len(evs))
	}
	must(t, f.w.AddEvent(ctx, &c.Event{RunID: v.Run.ID, Type: c.EventWarning, Payload: map[string]any{"message": "m"}}))
	if evs, _ := f.svc.EventsSince(ctx, v.Seq, "", 100); len(evs) != 1 || evs[0].Type != c.EventWarning {
		t.Errorf("events after snapshot = %+v", evs)
	}

	if _, err := f.svc.RunSnapshot(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing run: %v", err)
	}
}

func TestExecutionDetailAndReport(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	d, err := f.svc.Execution(ctx, "ex-research")
	if err != nil {
		t.Fatal(err)
	}
	if d.TaskKey != "research" || d.Role != c.RoleResearcher || d.Agent == nil || d.Agent.Name != "c2-researcher" || len(d.Artifacts) != 1 {
		t.Fatalf("detail = %+v", d)
	}
	if d, _ := f.svc.Execution(ctx, "ex-command"); d.Agent != nil || !d.ExitCodeObserved {
		t.Errorf("validation command detail = %+v", d)
	}
	if _, err := f.svc.Execution(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing execution: %v", err)
	}
	rep, err := f.svc.Report(ctx, "1111")
	if err != nil || !strings.Contains(rep, "add the feature") || !strings.Contains(rep, "| impl | developer | running |") {
		t.Errorf("report: %v\n%s", err, rep)
	}
	if caps, err := f.svc.Capabilities(ctx); err != nil || !caps.Supports("claude") {
		t.Errorf("capabilities: %+v, %v", caps, err)
	}
}

func TestReadArtifactIsBoundedRedactedAndConfined(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	got, err := f.svc.ReadArtifact(ctx, "art-1", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text, "found it") || strings.Contains(got.Text, "supersecret123") || got.Truncated {
		t.Errorf("text = %q truncated=%v", got.Text, got.Truncated)
	}
	if short, _ := f.svc.ReadArtifact(ctx, "art-1", 5); !short.Truncated || len(short.Text) > 5 {
		t.Errorf("bound not applied: %+v", short)
	}

	// A database row pointing elsewhere must not become a way to read the host.
	outside := filepath.Join(t.TempDir(), "secret.txt")
	must(t, os.WriteFile(outside, []byte("private"), 0o600))
	must(t, f.w.AddArtifact(ctx, &c.Artifact{ID: "art-out", RunID: "11111111-0000-0000-0000-000000000000", Type: c.ArtifactLog, Path: outside}))
	if _, err := f.svc.ReadArtifact(ctx, "art-out", 100); err == nil {
		t.Error("artifact outside the project was read")
	}
	link := filepath.Join(filepath.Dir(f.artifact), "link.txt")
	must(t, os.Symlink(outside, link))
	must(t, f.w.AddArtifact(ctx, &c.Artifact{ID: "art-link", RunID: "11111111-0000-0000-0000-000000000000", Type: c.ArtifactLog, Path: link}))
	if _, err := f.svc.ReadArtifact(ctx, "art-link", 100); err == nil {
		t.Error("symlink out of the artifact directory was followed")
	}
	if _, err := f.svc.ReadArtifact(ctx, "nope", 100); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing artifact: %v", err)
	}
}

func TestExecutionReportAndPrompt(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	d, err := f.svc.Execution(ctx, "ex-research")
	if err != nil {
		t.Fatal(err)
	}
	if d.Report == nil || d.Report.Summary != "found it" || !d.ClaimsUnverified {
		t.Errorf("report %+v, claims unverified %v", d.Report, d.ClaimsUnverified)
	}

	// An agent execution's own report (beside its prompt.md) wins over the
	// task's latest one.
	dir := filepath.Join(f.cfg.ArtifactDir(), "11111111-0000-0000-0000-000000000000", "impl", "exdevelo")
	must(t, os.MkdirAll(dir, 0o755))
	prompt := filepath.Join(dir, "prompt.md")
	must(t, os.WriteFile(prompt, []byte("Implement it. password=hunter2hunter2"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "report.json"), []byte(`{"summary":"own"}`), 0o644))
	other := filepath.Join(f.cfg.ArtifactDir(), "later.json")
	must(t, os.WriteFile(other, []byte(`{"summary":"later attempt"}`), 0o644))
	ex := &c.Execution{ID: "ex-dev-2", TaskID: "t-impl", AgentID: "ag-dev", Attempt: 1, Status: c.ExecRunning, Prompt: prompt, StartedAt: persistence.Now()}
	must(t, f.w.SaveExecution(ctx, ex))
	must(t, f.w.AddArtifact(ctx, &c.Artifact{ID: "art-own", RunID: "11111111-0000-0000-0000-000000000000", TaskID: "t-impl", Type: c.ArtifactReport, Path: filepath.Join(dir, "report.json")}))
	must(t, f.w.AddArtifact(ctx, &c.Artifact{ID: "art-later", RunID: "11111111-0000-0000-0000-000000000000", TaskID: "t-impl", Type: c.ArtifactReport, Path: other}))
	if d, _ := f.svc.Execution(ctx, "ex-dev-2"); d.Report == nil || d.Report.Summary != "own" {
		t.Errorf("own report not preferred: %+v", d.Report)
	}

	p, err := f.svc.ReadPrompt(ctx, "ex-dev-2", 1<<20)
	if err != nil || !strings.HasPrefix(p.Text, "Implement it.") || strings.Contains(p.Text, "hunter2hunter2") {
		t.Errorf("prompt %+v, %v", p, err)
	}
	if p, err := f.svc.ReadPrompt(ctx, "ex-command", 100); err != nil || p.Text != "go test ./..." {
		t.Errorf("command line %+v, %v", p, err)
	}
	ex.ID, ex.Prompt = "ex-dev-3", filepath.Join(t.TempDir(), "prompt.md") // prompts are immutable: a new row
	must(t, os.WriteFile(ex.Prompt, []byte("private"), 0o600))
	must(t, f.w.SaveExecution(ctx, ex))
	if _, err := f.svc.ReadPrompt(ctx, "ex-dev-3", 100); err == nil {
		t.Error("prompt outside the artifact directory was read")
	}
}
