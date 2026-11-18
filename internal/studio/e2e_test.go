package studio

// End-to-end: a real orchestrator drives a run on a real Git repository and
// SQLite database, with only the agent runtime faked; Studio reads it through
// the real service and watcher. Only test code imports the engine here; the
// import-boundary test covers Studio's own code.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rs/zerolog"

	"caffe/internal/agents"
	"caffe/internal/config"
	c "caffe/internal/contracts"
	"caffe/internal/core"
	"caffe/internal/git"
	"caffe/internal/persistence"
	"caffe/internal/planner"
	"caffe/internal/runtime"
	"caffe/internal/service"
)

const e2ePlan = `{"goal":"Add the feature","tasks":[
 {"id":"impl","title":"Implement","description":"Create feature.txt","role":"developer","dependencies":[],"acceptance_criteria":["feature.txt exists"]},
 {"id":"review","title":"Review","description":"Review the change","role":"reviewer","dependencies":["impl"],"acceptance_criteria":["Reviewed"]}]}`

type e2eEnv struct {
	orch    *core.Orchestrator
	svc     *service.Service
	release chan struct{} // closed: the developer agent finishes
}

func git1(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func newE2E(t *testing.T) *e2eEnv {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git1(t, repo, "init", "-q", "-b", "main")
	git1(t, repo, "config", "user.email", "t@example.com")
	git1(t, repo, "config", "user.name", "t")
	os.WriteFile(filepath.Join(repo, "README.md"), []byte("# demo\n"), 0o644)
	git1(t, repo, "add", "-A")
	git1(t, repo, "commit", "-qm", "init")

	if err := config.Write(repo, config.Default("demo")); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	store, err := persistence.Open(ctx, cfg.Persistence.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ro, err := persistence.OpenReadOnly(ctx, cfg.Persistence.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ro.Close() })
	gm, err := git.New(ctx, repo, cfg.Git.WorktreeDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if err := gm.ExcludeLocally(ctx, ".caffe/", ".caffe-report-*.json"); err != nil {
		t.Fatal(err)
	}

	e := &e2eEnv{release: make(chan struct{})}
	rt := runtime.NewFake()
	// The fake agents: the prompt names the prompt file (4th word) and the
	// report path (last word). The developer waits for the test.
	rt.OnPrompt = func(agent, _, cwd, text string) {
		if strings.Contains(text, "stopped without delivering") {
			return
		}
		f := strings.Fields(text)
		report := f[len(f)-1]
		switch {
		case strings.HasSuffix(agent, "-developer"):
			<-e.release
			os.WriteFile(filepath.Join(cwd, "feature.txt"), []byte("v1\n"), 0o644)
			os.WriteFile(report, []byte(`{"status":"completed","summary":"created feature.txt","modified_files":["feature.txt"],"tests":[{"command":"go test ./...","exit_code":0}]}`), 0o644)
		case strings.HasSuffix(agent, "-reviewer"):
			os.WriteFile(report, []byte(`{"status":"completed","summary":"looks good","approved":true,"findings":[]}`), 0o644)
		}
	}
	am := &agents.Manager{RT: rt, Poll: 5 * time.Millisecond, Log: zerolog.Nop(), IdleNudgeAfter: time.Minute, IdleGiveUpAfter: time.Minute}
	e.orch = &core.Orchestrator{Cfg: cfg, Store: store, RT: rt, Git: gm, Agents: am, Log: zerolog.Nop(), HeartbeatEvery: 10 * time.Millisecond}
	e.orch.Planner = &planner.AgentProvider{Agents: am, Kind: cfg.Agent(c.RolePlanner).Kind, OnSession: e.orch.PlannerSession, OnBlocked: e.orch.PlannerBlocked}
	e.svc = service.New(cfg, ro, rt, gm)
	return e
}

// pump runs a model like Bubble Tea does: commands run in goroutines and
// their messages come back to Update, one at a time.
type pump struct {
	t    *testing.T
	m    model
	msgs chan tea.Msg
	wg   sync.WaitGroup
}

func (p *pump) exec(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				p.exec(c)
			}
			return
		}
		if msg != nil {
			p.msgs <- msg
		}
	}()
}

func (p *pump) send(msg tea.Msg) {
	res, cmd := p.m.Update(msg)
	p.m = res.(model)
	p.exec(cmd)
}

func (p *pump) keys(keys ...string) {
	for _, k := range keys {
		p.send(keyPress(k))
	}
}

// until processes messages until cond holds for the model, or fails.
func (p *pump) until(what string, cond func(m model) bool) {
	p.t.Helper()
	deadline := time.After(15 * time.Second)
	for !cond(p.m) {
		select {
		case msg := <-p.msgs:
			p.send(msg)
		case <-deadline:
			p.t.Fatalf("timed out waiting for %s; screen:\n%s", what, plain(p.m.View().Content))
		}
	}
}

func task(m model, key string) (service.TaskView, bool) {
	if m.live.snap == nil {
		return service.TaskView{}, false
	}
	for _, t := range m.live.snap.Tasks {
		if t.Key == key {
			return t, true
		}
	}
	return service.TaskView{}, false
}

func TestStudioFollowsARealRunEndToEnd(t *testing.T) {
	e := newE2E(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w, err := e.svc.Watch(ctx, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(e.svc, Project{Name: "demo", Root: "/repo"}, &crashGuard{log: zerolog.Nop()})
	m.changes = w.Changes()
	p := &pump{t: t, m: m, msgs: make(chan tea.Msg, 64)}
	p.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	p.exec(p.m.Init())
	p.until("the empty state", func(m model) bool { return m.live.loaded })
	if v := plain(p.m.View().Content); !strings.Contains(v, "No runs yet") {
		t.Fatalf("not the empty state:\n%s", v)
	}

	plan, err := planner.ParsePlan([]byte(e2ePlan), 10)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan *c.Run, 1)
	go func() {
		run, err := e.orch.Run(context.Background(), "Add the feature", plan)
		if err != nil {
			t.Errorf("run: %v", err)
		}
		done <- run
	}()

	// While the developer works: the run is selected by itself, impl runs,
	// review waits on it, and the developer's card shows it.
	p.until("impl running", func(m model) bool {
		t, ok := task(m, "impl")
		return ok && t.Status == c.TaskRunning
	})
	v := plain(p.m.View().Content)
	for _, want := range []string{"Add the feature", "● running    impl", "waiting on impl", "AGENTS"} {
		if !strings.Contains(v, want) {
			t.Errorf("overview while running lacks %q:\n%s", want, v)
		}
	}
	p.keys("3")
	p.until("the developer's card", func(m model) bool {
		return strings.Contains(plain(m.View().Content), "developer · claude")
	})
	p.keys("2")
	if v := plain(p.m.View().Content); !strings.Contains(v, "⤷ impl") {
		t.Errorf("board lacks the pending review card:\n%s", v)
	}

	close(e.release)
	run := <-done
	if run == nil || run.Status != c.RunCompleted {
		t.Fatalf("run did not complete: %+v", run)
	}
	p.until("the completed run", func(m model) bool {
		return m.live.snap != nil && m.live.snap.Run.Status == c.RunCompleted
	})

	// Task Board: everything DONE.
	v = plain(p.m.View().Content)
	if !strings.Contains(v, "✓ impl") || !strings.Contains(v, "✓ review") {
		t.Errorf("board after the run:\n%s", v)
	}
	// Inspector on impl: the agent's tests are labelled as claims.
	p.keys("l", "l", "l", "l", "enter") // DONE column, first card: impl
	p.until("the impl execution", func(m model) bool { return m.inspect != nil && m.inspect.d != nil })
	v = plain(p.m.View().Content)
	for _, want := range []string{"Execution", "impl", "created feature.txt", "go test ./... → exit 0 · " + c.LabelClaimed} {
		if !strings.Contains(v, want) {
			t.Errorf("inspector lacks %q:\n%s", want, v)
		}
	}
	p.keys("esc", "4")
	if v := plain(p.m.View().Content); !strings.Contains(v, short(run.ID)) || !strings.Contains(v, "✓ completed") {
		t.Errorf("runs screen:\n%s", v)
	}
	p.keys("1", "r")
	p.until("the report", func(m model) bool { return m.report != nil && m.report.loaded })
	if v := plain(p.m.View().Content); !strings.Contains(v, "TASKS") || !strings.Contains(v, "impl") {
		t.Errorf("report:\n%s", v)
	}
	p.keys("esc")
	// The feed saw the run's events, in order and without duplicates.
	var last int64
	for _, ev := range p.m.live.feed {
		if ev.Seq <= last {
			t.Fatalf("feed out of order at seq %d", ev.Seq)
		}
		last = ev.Seq
	}
	if p.m.live.count(run.ID, c.EventDiffCollected) == 0 {
		t.Error("feed lacks the diff.collected event")
	}
}
