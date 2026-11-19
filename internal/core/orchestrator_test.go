package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"caffe/internal/agents"
	"caffe/internal/config"
	c "caffe/internal/contracts"
	"caffe/internal/git"
	"caffe/internal/persistence"
	"caffe/internal/planner"
	"caffe/internal/runtime"
)

type env struct {
	t     *testing.T
	o     *Orchestrator
	rt    *runtime.Fake
	repo  string
	store *persistence.Store
	sc    *script
}

type script struct {
	mu      sync.Mutex
	prompts map[c.Role][]string
	kinds   map[c.Role]string
	handle  func(role c.Role, cwd, prompt string) string
}

func (s *script) onPrompt(agent, kind, cwd, text string) {
	if strings.Contains(text, "stopped without delivering") {
		return
	}
	f := strings.Fields(text)
	promptPath, reportPath := f[3], f[len(f)-1]
	body, _ := os.ReadFile(promptPath)
	role := c.Role(agent[strings.Index(agent, "-")+1:])
	s.mu.Lock()
	s.prompts[role] = append(s.prompts[role], string(body))
	s.kinds[role] = kind
	s.mu.Unlock()
	if rep := s.handle(role, cwd, string(body)); rep != "" {
		os.WriteFile(reportPath, []byte(rep), 0o644)
	}
}

func (s *script) seen(role c.Role) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.prompts[role]...)
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newEnv(t *testing.T, tune func(*config.Config)) *env {
	t.Helper()
	ctx := context.Background()
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "init", "-q", "-b", "main")
	gitRun(t, repo, "config", "user.email", "t@example.com")
	gitRun(t, repo, "config", "user.name", "t")
	os.WriteFile(filepath.Join(repo, "README.md"), []byte("# demo\n"), 0o644)
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-qm", "init")

	cfg := config.Default("demo")
	cfg.Agents["researcher"] = config.AgentSpec{Kind: "codex"}
	cfg.Validation.Commands = []string{helperCommand(t, "check-file", "feature.txt")}
	if tune != nil {
		tune(cfg)
	}
	if err := config.Write(repo, cfg); err != nil {
		t.Fatal(err)
	}
	if cfg, err = config.Load(repo); err != nil {
		t.Fatal(err)
	}
	store, err := persistence.Open(ctx, cfg.Persistence.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	gm, err := git.New(ctx, repo, cfg.Git.WorktreeDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if err := gm.ExcludeLocally(ctx, ".caffe/", ".caffe-report-*.json"); err != nil {
		t.Fatal(err)
	}
	rt := runtime.NewFake()
	sc := &script{prompts: map[c.Role][]string{}, kinds: map[c.Role]string{}}
	rt.OnPrompt = sc.onPrompt
	am := &agents.Manager{RT: rt, Poll: 5 * time.Millisecond, Log: zerolog.Nop(), IdleNudgeAfter: 15 * time.Millisecond, IdleGiveUpAfter: 80 * time.Millisecond}
	o := &Orchestrator{Cfg: cfg, Store: store, RT: rt, Git: gm, Agents: am, Log: zerolog.Nop(), HeartbeatEvery: 10 * time.Millisecond}
	o.Planner = &planner.AgentProvider{Agents: am, Kind: cfg.Agent(c.RolePlanner).Kind, OnSession: o.PlannerSession, OnBlocked: o.PlannerBlocked}
	return &env{t: t, o: o, rt: rt, repo: repo, store: store, sc: sc}
}

func (e *env) tasks(runID string) map[string]*c.Task {
	e.t.Helper()
	list, err := e.store.ListTasks(context.Background(), runID)
	if err != nil {
		e.t.Fatal(err)
	}
	out := map[string]*c.Task{}
	for _, t := range list {
		out[t.Key] = t
	}
	return out
}

const fullPlan = `{"goal":"Add the feature","tasks":[
 {"id":"research","title":"Research","description":"Look around","role":"researcher","dependencies":[],"acceptance_criteria":["Files found"]},
 {"id":"impl","title":"Implement","description":"Create feature.txt","role":"developer","dependencies":["research"],"acceptance_criteria":["feature.txt exists"]},
 {"id":"review","title":"Review","description":"Review the change","role":"reviewer","dependencies":["impl"],"acceptance_criteria":["Reviewed"]},
 {"id":"validate","title":"Validate","description":"Run checks","role":"validator","dependencies":["review"],"acceptance_criteria":["Checks pass"]}]}`

const (
	okReport      = `{"status":"completed","summary":"did it"}`
	approveReport = `{"status":"completed","summary":"looks good","approved":true,"findings":[]}`
	rejectReport  = `{"status":"completed","summary":"not yet","approved":false,"findings":[{"severity":"high","file":"feature.txt","line":1,"description":"content-must-say-fixed"}]}`
)

func goodAgents(role c.Role, cwd, prompt string) string {
	switch role {
	case c.RolePlanner:
		return fullPlan
	case c.RoleResearcher:
		return `{"status":"completed","summary":"research-summary-xyz","relevant_files":["README.md"],"recommendation":"add feature.txt"}`
	case c.RoleDeveloper:
		os.WriteFile(filepath.Join(cwd, "feature.txt"), []byte("v1\n"), 0o644)
		return okReport
	case c.RoleReviewer:
		return approveReport
	}
	return okReport
}

func TestFullRunPlansDelegatesReviewsAndValidates(t *testing.T) {
	e := newEnv(t, nil)
	e.sc.handle = goodAgents
	var progress []string
	e.o.Progress = func(l string) { progress = append(progress, l) }

	run, err := e.o.Run(context.Background(), "Add the feature", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != c.RunCompleted {
		t.Fatalf("run = %s (%s)\n%s", run.Status, run.Error, strings.Join(progress, "\n"))
	}
	tasks := e.tasks(run.ID)
	for k, task := range tasks {
		if task.Status != c.TaskCompleted {
			t.Errorf("task %s = %s", k, task.Status)
		}
	}
	impl := tasks["impl"]

	if !strings.HasPrefix(impl.BranchName, "caffe/") || !strings.HasSuffix(impl.BranchName, "/impl") {
		t.Errorf("branch = %q", impl.BranchName)
	}
	if _, err := os.Stat(filepath.Join(impl.WorktreePath, "feature.txt")); err != nil {
		t.Error("feature.txt missing from worktree")
	}
	if _, err := os.Stat(filepath.Join(e.repo, "feature.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Error("feature.txt leaked into the main checkout")
	}
	if st := gitRun(t, e.repo, "status", "--porcelain"); st != "" {
		t.Errorf("main checkout dirty: %q", st)
	}
	if got := gitRun(t, e.repo, "show", impl.BranchName+":feature.txt"); got != "v1" {
		t.Errorf("branch content = %q", got)
	}
	if head := gitRun(t, e.repo, "rev-parse", "HEAD"); head != run.Metadata["base_commit"] {
		t.Error("main branch moved: something was merged")
	}

	if e.sc.kinds[c.RoleResearcher] != "codex" || e.sc.kinds[c.RoleDeveloper] != "claude" {
		t.Errorf("kinds = %v", e.sc.kinds)
	}

	if dev := e.sc.seen(c.RoleDeveloper); len(dev) != 1 || !strings.Contains(dev[0], "research-summary-xyz") {
		t.Errorf("developer prompt lacks upstream research: %d prompts", len(dev))
	}
	if rev := e.sc.seen(c.RoleReviewer); len(rev) != 1 || !strings.Contains(rev[0], "changes.diff") {
		t.Error("reviewer prompt lacks the diff path")
	}

	execs, _ := e.store.ListExecutions(context.Background(), run.ID)
	var validated bool
	for _, ex := range execs {
		if ex.TaskID == tasks["validate"].ID {
			validated = ex.ExitCode != nil && *ex.ExitCode == 0 && ex.Prompt == helperCommand(t, "check-file", "feature.txt")
		}
	}
	if !validated {
		t.Error("validation command exit code not recorded")
	}
	arts, _ := e.store.ListArtifacts(context.Background(), run.ID)
	types := map[c.ArtifactType]int{}
	for _, a := range arts {
		types[a.Type]++
		if _, err := os.Stat(a.Path); err != nil {
			t.Errorf("artifact %s missing on disk", a.Path)
		}
	}
	if types[c.ArtifactDiff] == 0 || types[c.ArtifactReport] < 5 || types[c.ArtifactTestResult] != 1 || types[c.ArtifactLog] == 0 {
		t.Errorf("artifacts = %v", types)
	}
	report, _ := os.ReadFile(filepath.Join(e.o.RunDir(run.ID), "report.md"))
	for _, want := range []string{"completed", impl.BranchName, "exit 0", "research-summary-xyz", "executed by the orchestrator"} {
		if !strings.Contains(string(report), want) {
			t.Errorf("report lacks %q", want)
		}
	}
	if live := e.rt.Live(); len(live) != 0 {
		t.Errorf("agents left running: %v", live)
	}

	evs, err := e.store.EventsSince(context.Background(), 0, run.ID, 10_000)
	if err != nil {
		t.Fatal(err)
	}
	first := map[string]int64{}
	artifactEvents := 0
	for _, ev := range evs {
		id := ev.AgentID
		if x, ok := ev.Payload["execution_id"].(string); ok {
			id = x
		}
		if _, ok := first[ev.Type+"|"+id]; !ok {
			first[ev.Type+"|"+id] = ev.Seq
		}
		if ev.Type == c.EventArtifactAdded {
			artifactEvents++
		}
	}
	agentsList, _ := e.store.ListAgents(context.Background(), run.ID)
	if len(agentsList) != 4 {
		t.Errorf("agents = %d, want 4", len(agentsList))
	}
	for _, a := range agentsList {
		started, ok1 := first[c.EventAgentStarted+"|"+a.ID]
		stopped, ok2 := first[c.EventAgentStopped+"|"+a.ID]
		if !ok1 || !ok2 || started >= stopped || a.Status != "stopped" {
			t.Errorf("agent %s (%s): started=%v stopped=%v status=%s", a.Name, a.Role, ok1, ok2, a.Status)
		}
	}
	for _, ex := range execs {
		if ex.AgentID == "" {
			continue
		}
		st, ok1 := first[c.EventExecutionStarted+"|"+ex.ID]
		ag, ok2 := first[c.EventAgentStarted+"|"+ex.AgentID]
		done, ok3 := first["execution.completed|"+ex.ID]
		if !ok1 || !ok2 || !ok3 || !(st < ag && ag < done) {
			t.Errorf("execution %s: started=%d agent=%d completed=%d", ex.ID[:8], st, ag, done)
		}
	}
	if artifactEvents != len(arts) {
		t.Errorf("artifact.added events = %d, artifacts = %d", artifactEvents, len(arts))
	}

	if n := len(e.rt.Prompts); n != 4 {
		t.Errorf("prompts sent = %d, want 4", n)
	}
}

func TestRunWithExplicitID(t *testing.T) {
	e := newEnv(t, nil)
	e.sc.handle = func(c.Role, string, string) string { return okReport }
	plan := &c.ExecutionPlan{Goal: "g", Tasks: []c.PlanTask{{ID: "r", Title: "t", Description: "d", Role: c.RoleResearcher, AcceptanceCriteria: []string{"x"}}}}
	ctx := context.Background()
	const id = "0f8fad5b-d9cb-469f-a165-70867728950e"

	run, err := e.o.RunWithID(ctx, id, "g", plan)
	if err != nil || run.ID != id || run.Status != c.RunCompleted {
		t.Fatalf("run = %+v, %v", run, err)
	}

	prompts := len(e.rt.Prompts)
	if _, err := e.o.RunWithID(ctx, id, "g", plan); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate id: %v", err)
	}
	if len(e.rt.Prompts) != prompts {
		t.Error("a duplicate id still started agents")
	}
	for _, bad := range []string{"not-a-uuid", strings.ToUpper(id), "{" + id + "}", "0f8fad5bd9cb469fa16570867728950e", "../../etc"} {
		if _, err := e.o.RunWithID(ctx, bad, "g", plan); err == nil || !strings.Contains(err.Error(), "canonical UUID") {
			t.Errorf("id %q: %v", bad, err)
		}
	}
	runs, _ := e.store.ListRuns(ctx, false)
	if len(runs) != 1 {
		t.Errorf("rejected ids created runs: %d runs", len(runs))
	}
}

func TestReviewerRejectionTriggersCorrection(t *testing.T) {
	e := newEnv(t, nil)
	reviews := 0
	e.sc.handle = func(role c.Role, cwd, prompt string) string {
		switch role {
		case c.RoleDeveloper:
			if strings.Contains(prompt, "content-must-say-fixed") {
				os.WriteFile(filepath.Join(cwd, "feature.txt"), []byte("fixed\n"), 0o644)
				return okReport
			}
		case c.RoleReviewer:
			if reviews++; reviews == 1 {

				os.WriteFile(filepath.Join(cwd, "reviewer-was-here.txt"), []byte("x"), 0o644)
				return rejectReport
			}
		}
		return goodAgents(role, cwd, prompt)
	}
	run, err := e.o.Run(context.Background(), "Add the feature", nil)
	if err != nil || run.Status != c.RunCompleted {
		t.Fatalf("run = %s (%s), err = %v", run.Status, run.Error, err)
	}
	impl := e.tasks(run.ID)["impl"]
	if reviews != 2 || len(e.sc.seen(c.RoleDeveloper)) != 2 {
		t.Fatalf("reviews=%d developer runs=%d", reviews, len(e.sc.seen(c.RoleDeveloper)))
	}
	if got := gitRun(t, e.repo, "show", impl.BranchName+":feature.txt"); got != "fixed" {
		t.Errorf("correction not on the branch: %q", got)
	}
	if files := gitRun(t, e.repo, "ls-tree", "-r", "--name-only", impl.BranchName); strings.Contains(files, "reviewer-was-here") {
		t.Error("reviewer's stray edit was committed to the developer branch")
	}
	if impl.Status != c.TaskCompleted || impl.Attempts != 2 {
		t.Errorf("impl status=%s attempts=%d", impl.Status, impl.Attempts)
	}
	evs, _ := e.store.ListEvents(context.Background(), run.ID)
	var sawReviewing bool
	for _, ev := range evs {
		sawReviewing = sawReviewing || ev.Type == "task.reviewing"
	}
	if !sawReviewing {
		t.Error("developer task never went through reviewing")
	}
}

func TestReviewNeverApprovedFailsRunAndKeepsWorktree(t *testing.T) {
	e := newEnv(t, func(cfg *config.Config) { cfg.Orchestration.MaxReviewRounds = 1 })
	e.sc.handle = func(role c.Role, cwd, prompt string) string {
		if role == c.RoleReviewer {
			return rejectReport
		}
		if role == c.RoleDeveloper {
			f, _ := os.OpenFile(filepath.Join(cwd, "feature.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			f.WriteString("try\n")
			f.Close()
			return okReport
		}
		return goodAgents(role, cwd, prompt)
	}
	run, err := e.o.Run(context.Background(), "Add the feature", nil)
	if err != nil {
		t.Fatal(err)
	}
	tasks := e.tasks(run.ID)
	if run.Status != c.RunFailed || tasks["review"].Status != c.TaskFailed || tasks["validate"].Status != c.TaskCancelled {
		t.Fatalf("run=%s review=%s validate=%s", run.Status, tasks["review"].Status, tasks["validate"].Status)
	}
	if tasks["review"].Attempts != 1 {
		t.Errorf("a rejected review must not be retried: attempts=%d", tasks["review"].Attempts)
	}
	if n := len(e.sc.seen(c.RoleReviewer)); n != 2 {
		t.Errorf("reviews = %d, want 2", n)
	}

	actions, err := e.o.Clean(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tasks["impl"].WorktreePath); err != nil {
		t.Errorf("worktree of the run was removed: %v (%v)", err, actions)
	}
}

func TestValidationFailureRecordsRealExitCode(t *testing.T) {
	check, fail := helperCommand(t, "check-file", "feature.txt"), helperCommand(t, "fail")
	e := newEnv(t, func(cfg *config.Config) { cfg.Validation.Commands = []string{check, fail} })
	e.sc.handle = goodAgents
	run, err := e.o.Run(context.Background(), "Add the feature", nil)
	if err != nil {
		t.Fatal(err)
	}
	tasks := e.tasks(run.ID)
	if run.Status != c.RunFailed || tasks["validate"].Status != c.TaskFailed || tasks["validate"].Attempts != 1 {
		t.Fatalf("run=%s validate=%s attempts=%d", run.Status, tasks["validate"].Status, tasks["validate"].Attempts)
	}
	execs, _ := e.store.ListExecutions(context.Background(), run.ID)
	codes := map[string]int{}
	for _, ex := range execs {
		if ex.ExitCode != nil {
			codes[ex.Prompt] = *ex.ExitCode
			if ex.Prompt == fail && !strings.Contains(ex.Output, "boom") {
				t.Errorf("command output not captured: %q", ex.Output)
			}
		}
	}
	if codes[check] != 0 || codes[fail] != 3 || len(codes) != 2 {
		t.Fatalf("exit codes = %v", codes)
	}
}

func TestConcurrentDevelopersAreIsolatedAndBounded(t *testing.T) {
	e := newEnv(t, func(cfg *config.Config) { cfg.Orchestration.MaxConcurrentAgents = 2 })
	plan := &c.ExecutionPlan{Goal: "three things"}
	for _, k := range []string{"one", "two", "three"} {
		plan.Tasks = append(plan.Tasks, c.PlanTask{ID: k, Title: k, Description: "write shared.txt", Role: c.RoleDeveloper, AcceptanceCriteria: []string{"x"}})
	}
	e.sc.handle = func(role c.Role, cwd, prompt string) string {

		os.WriteFile(filepath.Join(cwd, "shared.txt"), []byte(filepath.Base(cwd)+"\n"), 0o644)
		time.Sleep(40 * time.Millisecond)
		if b, _ := os.ReadFile(filepath.Join(cwd, "shared.txt")); strings.TrimSpace(string(b)) != filepath.Base(cwd) {
			t.Errorf("another developer overwrote %s", cwd)
		}
		return okReport
	}
	run, err := e.o.Run(context.Background(), "three things", plan)
	if err != nil || run.Status != c.RunCompleted {
		t.Fatalf("run = %s (%s), err = %v", run.Status, run.Error, err)
	}
	paths := map[string]bool{}
	for k, task := range e.tasks(run.ID) {
		paths[task.WorktreePath] = true
		if got := gitRun(t, e.repo, "show", task.BranchName+":shared.txt"); got != k {
			t.Errorf("branch of %s holds %q", k, got)
		}
	}
	if len(paths) != 3 {
		t.Errorf("developers shared a worktree: %v", paths)
	}
	if e.rt.MaxConcurrent != 2 {
		t.Errorf("max concurrent agents = %d, want 2 (bounded, but actually concurrent)", e.rt.MaxConcurrent)
	}
	evs, _ := e.store.ListEvents(context.Background(), run.ID)
	conflicts := 0
	for _, ev := range evs {
		if msg, _ := ev.Payload["message"].(string); ev.Type == c.EventWarning && strings.Contains(msg, "conflict on shared.txt") {
			conflicts++
		}
	}
	if conflicts != 3 {
		t.Errorf("want one conflict warning per pair of developers, got %d", conflicts)
	}
}

func TestDependentDeveloperBuildsOnDependencyBranch(t *testing.T) {
	e := newEnv(t, nil)
	plan := &c.ExecutionPlan{Goal: "stack", Tasks: []c.PlanTask{
		{ID: "base", Title: "base", Description: "d", Role: c.RoleDeveloper, AcceptanceCriteria: []string{"x"}},
		{ID: "top", Title: "top", Description: "d", Role: c.RoleDeveloper, Dependencies: []string{"base"}, AcceptanceCriteria: []string{"x"}},
	}}
	e.sc.handle = func(role c.Role, cwd, prompt string) string {
		name := filepath.Base(cwd)
		if name == "top" {
			if _, err := os.Stat(filepath.Join(cwd, "base.txt")); err != nil {
				t.Error("top does not see the work of the task it depends on")
			}
		}
		os.WriteFile(filepath.Join(cwd, name+".txt"), []byte("x\n"), 0o644)
		return okReport
	}
	run, err := e.o.Run(context.Background(), "stack", plan)
	if err != nil || run.Status != c.RunCompleted {
		t.Fatalf("run = %s (%s), err = %v", run.Status, run.Error, err)
	}

	diff, _ := os.ReadFile(filepath.Join(e.o.RunDir(run.ID), "top", "changes.diff"))
	if !strings.Contains(string(diff), "top.txt") || strings.Contains(string(diff), "base.txt") {
		t.Errorf("top diff is not relative to its dependency:\n%s", diff)
	}
}

func TestDeveloperClaimWithoutChangesIsNotSuccess(t *testing.T) {
	e := newEnv(t, func(cfg *config.Config) { cfg.Orchestration.MaxTaskRetries = 1 })
	plan := &c.ExecutionPlan{Goal: "g", Tasks: []c.PlanTask{{ID: "impl", Title: "t", Description: "d", Role: c.RoleDeveloper, AcceptanceCriteria: []string{"x"}}}}
	e.sc.handle = func(c.Role, string, string) string { return okReport }
	run, err := e.o.Run(context.Background(), "g", plan)
	if err != nil {
		t.Fatal(err)
	}
	impl := e.tasks(run.ID)["impl"]
	if run.Status != c.RunFailed || impl.Status != c.TaskFailed || impl.Attempts != 2 || !strings.Contains(impl.Error, "no changes") {
		t.Fatalf("run=%s impl=%s attempts=%d err=%q", run.Status, impl.Status, impl.Attempts, impl.Error)
	}

	if p := e.sc.seen(c.RoleDeveloper); len(p) != 2 || strings.Contains(p[0], "previous attempt") || !strings.Contains(p[1], "previous attempt") {
		t.Errorf("retry prompt handling wrong (%d prompts)", len(p))
	}
}

func TestInvalidReportAndInvalidPlanAreRejected(t *testing.T) {
	e := newEnv(t, func(cfg *config.Config) { cfg.Orchestration.MaxTaskRetries = 1 })
	plans := 0
	e.sc.handle = func(role c.Role, cwd, prompt string) string {
		if role == c.RolePlanner {
			plans++
			return `{"goal":"g","tasks":[{"id":"a","title":"t","description":"d","role":"developer","dependencies":["a"],"acceptance_criteria":["x"]}]}`
		}
		return okReport
	}
	run, err := e.o.Run(context.Background(), "g", nil)
	if err == nil || !strings.Contains(err.Error(), "depends on itself") || run.Status != c.RunFailed {
		t.Fatalf("run=%s err=%v", run.Status, err)
	}
	if plans != 2 {
		t.Errorf("planner asked %d times, want 2", plans)
	}
	if tasks := e.tasks(run.ID); len(tasks) != 0 {
		t.Errorf("an invalid plan created %d tasks", len(tasks))
	}

	e2 := newEnv(t, func(cfg *config.Config) { cfg.Orchestration.MaxTaskRetries = 0 })
	e2.sc.handle = func(c.Role, string, string) string { return `{"status":"totally done","summary":"x"}` }
	plan := &c.ExecutionPlan{Goal: "g", Tasks: []c.PlanTask{{ID: "r", Title: "t", Description: "d", Role: c.RoleResearcher, AcceptanceCriteria: []string{"x"}}}}
	run2, err := e2.o.Run(context.Background(), "g", plan)
	if err != nil || run2.Status != c.RunFailed || !strings.Contains(e2.tasks(run2.ID)["r"].Error, "invalid report") {
		t.Fatalf("run=%s err=%v task=%q", run2.Status, err, e2.tasks(run2.ID)["r"].Error)
	}
}

func TestUnavailableAgentKindFailsBeforeAnythingStarts(t *testing.T) {
	e := newEnv(t, func(cfg *config.Config) { cfg.Agents["developer"] = config.AgentSpec{Kind: "gemini"} })
	e.sc.handle = goodAgents
	plan := &c.ExecutionPlan{Goal: "g", Tasks: []c.PlanTask{{ID: "impl", Title: "t", Description: "d", Role: c.RoleDeveloper, AcceptanceCriteria: []string{"x"}}}}
	run, err := e.o.Run(context.Background(), "g", plan)
	if err == nil || !strings.Contains(err.Error(), `agents.developer.kind="gemini"`) || !strings.Contains(err.Error(), "claude, codex") {
		t.Fatalf("err = %v", err)
	}
	if run.Status != c.RunFailed || len(e.rt.Prompts) != 0 {
		t.Fatalf("run=%s prompts=%d", run.Status, len(e.rt.Prompts))
	}
}

func TestCancelFromAnotherProcessStopsAgents(t *testing.T) {
	e := newEnv(t, nil)
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	e.sc.handle = func(role c.Role, cwd, prompt string) string {
		started <- struct{}{}
		<-release
		return ""
	}
	defer close(release)
	plan := &c.ExecutionPlan{Goal: "g", Tasks: []c.PlanTask{
		{ID: "a", Title: "t", Description: "d", Role: c.RoleResearcher, AcceptanceCriteria: []string{"x"}},
		{ID: "b", Title: "t", Description: "d", Role: c.RoleResearcher, Dependencies: []string{"a"}, AcceptanceCriteria: []string{"x"}},
	}}
	type outcome struct {
		run *c.Run
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		run, err := e.o.Run(context.Background(), "g", plan)
		done <- outcome{run, err}
	}()
	<-started
	runs, _ := e.store.ListRuns(context.Background(), true)

	other := &Orchestrator{Cfg: e.o.Cfg, Store: e.store, RT: e.rt, Git: e.o.Git, Log: zerolog.Nop()}
	if _, err := other.Cancel(context.Background(), runs[0].ID[:8]); err != nil {
		t.Fatal(err)
	}
	select {
	case out := <-done:
		if out.err != nil || out.run.Status != c.RunCancelled {
			t.Fatalf("run=%s err=%v", out.run.Status, out.err)
		}
		tasks := e.tasks(out.run.ID)
		if tasks["a"].Status != c.TaskCancelled || tasks["b"].Status != c.TaskCancelled {
			t.Errorf("a=%s b=%s", tasks["a"].Status, tasks["b"].Status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the owning process did not notice the cancellation")
	}
	if live := e.rt.Live(); len(live) != 0 {
		t.Errorf("agents left running after cancel: %v", live)
	}
	if _, err := other.Cancel(context.Background(), runs[0].ID); err == nil {
		t.Error("cancelling a finished run must be an error")
	}
}

func (e *env) crash(agentAlive bool) (run *c.Run, implExec *c.Execution, wt string) {
	t := e.t
	t.Helper()
	ctx := context.Background()
	head := gitRun(t, e.repo, "rev-parse", "HEAD")
	old := persistence.Now().Add(-time.Hour)
	run = &c.Run{ID: "11111111-2222-3333-4444-555555555555", ProjectPath: e.repo, UserRequest: "Add the feature", Status: c.RunPending,
		CreatedAt: old, HeartbeatAt: &old, Metadata: map[string]string{"base_commit": head}}
	if err := e.store.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	var plan c.ExecutionPlan
	p, err := planner.ParsePlan([]byte(fullPlan), 10)
	if err != nil {
		t.Fatal(err)
	}
	plan = *p
	tasks := tasksFromPlan(run.ID, &plan, 3)
	if err := e.store.CreateTasks(ctx, tasks); err != nil {
		t.Fatal(err)
	}
	run.Status = c.RunExecuting
	if err := e.store.UpdateRun(ctx, run, c.RunPending); err != nil {
		t.Fatal(err)
	}
	step := func(task *c.Task, to c.TaskStatus, mut func(*c.Task)) {
		next, err := transition(ctx, e.store, *task, to, "", mut)
		if err != nil {
			t.Fatal(err)
		}
		*task = next
	}
	research, impl := tasks[0], tasks[1]
	for _, st := range []c.TaskStatus{c.TaskReady, c.TaskAssigned, c.TaskRunning, c.TaskCompleted} {
		step(research, st, func(t *c.Task) { t.Attempts = 1 })
	}
	repPath := filepath.Join(e.o.RunDir(run.ID), "research", "report.json")
	os.MkdirAll(filepath.Dir(repPath), 0o755)
	os.WriteFile(repPath, []byte(`{"status":"completed","summary":"research-from-before-the-crash"}`), 0o644)
	e.store.AddArtifact(ctx, &c.Artifact{RunID: run.ID, TaskID: research.ID, Type: c.ArtifactReport, Path: repPath})

	wt, err = e.o.Git.CreateWorktree(ctx, filepath.Join(short(run.ID), "impl"), "caffe/"+short(run.ID)+"/impl", head)
	if err != nil {
		t.Fatal(err)
	}
	implExec = &c.Execution{ID: "aaaaaaaa-0000-0000-0000-000000000000", TaskID: impl.ID, AgentID: "agent-1", Attempt: 1, Status: c.ExecRunning, StartedAt: old}
	agent := &c.Agent{ID: "agent-1", RunID: run.ID, Name: agents.AgentName(implExec.ID, c.RoleDeveloper), Kind: "claude", Role: c.RoleDeveloper, Status: "running"}
	if agentAlive {
		ws, _ := e.rt.CreateWorkspace(ctx, wt, agent.Name)
		e.rt.StartAgent(ctx, "claude", ws.PaneID, agent.Name, nil)
		e.rt.SetStatus(agent.Name, runtime.StatusWorking)
		agent.WorkspaceID, agent.PaneID = ws.ID, ws.PaneID
	}
	if err := e.store.SaveAgent(ctx, agent); err != nil {
		t.Fatal(err)
	}
	if err := e.store.SaveExecution(ctx, implExec); err != nil {
		t.Fatal(err)
	}
	step(impl, c.TaskReady, nil)
	step(impl, c.TaskAssigned, func(t *c.Task) {
		t.Attempts, t.WorktreePath, t.BranchName = 1, wt, "caffe/"+short(run.ID)+"/impl"
	})
	step(impl, c.TaskRunning, func(t *c.Task) { t.AssignedAgentID = agent.ID })
	return run, implExec, wt
}

func TestRestartDoesNotLoseOrDuplicateRunningWork(t *testing.T) {
	e := newEnv(t, nil)
	e.sc.handle = goodAgents
	run, implExec, wt := e.crash(true)
	ctx := context.Background()

	ids, err := e.o.Recover(ctx)
	if err != nil || len(ids) != 1 {
		t.Fatalf("recover = %v, %v", ids, err)
	}
	got, _ := e.store.GetRun(ctx, run.ID)
	if got.Status != c.RunInterrupted || e.tasks(run.ID)["impl"].Status != c.TaskRunning {
		t.Fatalf("after recover: run=%s impl=%s", got.Status, e.tasks(run.ID)["impl"].Status)
	}
	if len(e.rt.Prompts) != 0 {
		t.Fatal("recovery prompted an agent")
	}
	if _, err := e.o.Run(ctx, "other", &c.ExecutionPlan{}); err == nil {
		t.Fatal("empty preset plan accepted")
	}

	agentName := agents.AgentName(implExec.ID, c.RoleDeveloper)
	time.AfterFunc(60*time.Millisecond, func() {
		os.WriteFile(filepath.Join(wt, "feature.txt"), []byte("from-surviving-agent\n"), 0o644)
		os.WriteFile(agents.ReportPath(wt, implExec.ID), []byte(okReport), 0o644)
		e.rt.SetStatus(agentName, runtime.StatusIdle)
	})
	resumed, err := e.o.Resume(ctx, run.ID)
	if err != nil || resumed.Status != c.RunCompleted {
		t.Fatalf("resume: run=%s (%s) err=%v", resumed.Status, resumed.Error, err)
	}

	if n := len(e.sc.seen(c.RoleDeveloper)); n != 0 {
		t.Errorf("developer was prompted again %d times", n)
	}
	if n := len(e.sc.seen(c.RoleResearcher)); n != 0 {
		t.Errorf("completed research was re-run %d times", n)
	}

	tasks := e.tasks(run.ID)
	if got := gitRun(t, e.repo, "show", tasks["impl"].BranchName+":feature.txt"); got != "from-surviving-agent" {
		t.Errorf("branch content = %q", got)
	}
	execs, _ := e.store.ListExecutions(ctx, run.ID)
	var implExecs []*c.Execution
	for _, ex := range execs {
		if ex.TaskID == tasks["impl"].ID {
			implExecs = append(implExecs, ex)
		}
	}
	if len(implExecs) != 1 || implExecs[0].ID != implExec.ID || implExecs[0].Status != c.ExecCompleted || tasks["impl"].Attempts != 1 {
		t.Errorf("impl executions = %d, status %s, attempts %d", len(implExecs), implExecs[0].Status, tasks["impl"].Attempts)
	}
	if len(e.sc.seen(c.RoleReviewer)) != 1 || tasks["validate"].Status != c.TaskCompleted {
		t.Error("the rest of the graph did not run after resume")
	}
	if _, err := e.o.Resume(ctx, run.ID); err == nil {
		t.Error("a completed run must not be resumable")
	}
}

func TestResumeWithVanishedAgentRecordsUnknownAndContinuesInWorktree(t *testing.T) {
	e := newEnv(t, nil)
	run, implExec, wt := e.crash(false)
	ctx := context.Background()

	os.WriteFile(filepath.Join(wt, "partial.txt"), []byte("half done\n"), 0o644)
	e.sc.handle = func(role c.Role, cwd, prompt string) string {
		if role == c.RoleDeveloper {
			if _, err := os.Stat(filepath.Join(cwd, "partial.txt")); err != nil {
				t.Error("partial work of the interrupted attempt was thrown away")
			}
		}
		return goodAgents(role, cwd, prompt)
	}
	if _, err := e.o.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	resumed, err := e.o.Resume(ctx, run.ID[:6])
	if err != nil || resumed.Status != c.RunCompleted {
		t.Fatalf("resume: run=%s (%s) err=%v", resumed.Status, resumed.Error, err)
	}
	dev := e.sc.seen(c.RoleDeveloper)
	if len(dev) != 1 || !strings.Contains(dev[0], "previous attempt") || !strings.Contains(dev[0], "research-from-before-the-crash") {
		t.Fatalf("new developer execution was not told about the interrupted one (%d prompts)", len(dev))
	}
	execs, _ := e.store.ListExecutions(ctx, run.ID)
	status := map[string]c.ExecutionStatus{}
	for _, ex := range execs {
		status[ex.ID] = ex.Status
	}
	if status[implExec.ID] != c.ExecUnknown {
		t.Errorf("interrupted execution = %s, want unknown (its outcome cannot be known)", status[implExec.ID])
	}
	if e.tasks(run.ID)["impl"].WorktreePath != wt {
		t.Error("a new worktree was created instead of continuing in the preserved one")
	}
}

func TestCancelInterruptedRunCleansUpLeftovers(t *testing.T) {
	e := newEnv(t, nil)
	run, implExec, _ := e.crash(true)
	ctx := context.Background()
	if _, err := e.o.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := e.o.Cancel(ctx, run.ID)
	if err != nil || got.Status != c.RunCancelled {
		t.Fatalf("cancel: %v, %v", got, err)
	}
	if live := e.rt.Live(); len(live) != 0 {
		t.Errorf("orphaned agent still running: %v", live)
	}
	tasks := e.tasks(run.ID)
	if tasks["impl"].Status != c.TaskCancelled || tasks["review"].Status != c.TaskCancelled || tasks["research"].Status != c.TaskCompleted {
		t.Errorf("impl=%s review=%s research=%s", tasks["impl"].Status, tasks["review"].Status, tasks["research"].Status)
	}
	execs, _ := e.store.ListExecutions(ctx, run.ID)
	for _, ex := range execs {
		if ex.ID == implExec.ID && ex.Status != c.ExecCancelled {
			t.Errorf("execution = %s", ex.Status)
		}
	}
	report, err := BuildReport(ctx, e.store, run.ID)
	if err != nil || !strings.Contains(report, "cancelled") {
		t.Errorf("report: %v\n%s", err, report)
	}
}

func TestCleanRemovesOnlyCompletedWorktreesAndKeepsBranches(t *testing.T) {
	e := newEnv(t, nil)
	e.sc.handle = goodAgents
	run, err := e.o.Run(context.Background(), "Add the feature", nil)
	if err != nil || run.Status != c.RunCompleted {
		t.Fatal(run.Status, err)
	}
	impl := e.tasks(run.ID)["impl"]

	human := &persistence.Lease{Path: impl.WorktreePath, HolderKind: persistence.LeaseHuman, HolderID: "studio-1"}
	if err := e.store.AcquireLease(context.Background(), human); err != nil {
		t.Fatal(err)
	}
	if actions, err := e.o.Clean(context.Background(), true); err != nil || len(actions) != 1 || !strings.Contains(actions[0], "leased by human studio-1") {
		t.Fatalf("clean of a leased worktree = %v, %v", actions, err)
	}
	e.store.ReleaseLease(context.Background(), human)
	actions, err := e.o.Clean(context.Background(), false)
	if err != nil || len(actions) != 1 || !strings.HasPrefix(actions[0], "removed") {
		t.Fatalf("clean = %v, %v", actions, err)
	}
	if _, err := os.Stat(impl.WorktreePath); !errors.Is(err, os.ErrNotExist) {
		t.Error("worktree still there")
	}
	if got := gitRun(t, e.repo, "show", impl.BranchName+":feature.txt"); got != "v1" {
		t.Error("branch was lost")
	}
	if again, _ := e.o.Clean(context.Background(), false); len(again) != 0 {
		t.Errorf("second clean did something: %v", again)
	}
}
