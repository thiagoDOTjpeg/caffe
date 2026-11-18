// Package service is Caffè's application API: the only way consumers (the
// CLI, Caffè Studio) read engine state or ask the engine to act. Consumers
// never touch persistence, the runtime or managed worktrees directly.
//
// This file holds the queries. They never modify state.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"caffe/internal/config"
	c "caffe/internal/contracts"
	"caffe/internal/core"
	"caffe/internal/git"
	"caffe/internal/persistence"
	"caffe/internal/runtime"
)

// ErrNotFound is returned when a run, execution or artifact does not exist.
var ErrNotFound = errors.New("not found")

// Service answers queries over a read-only store.
type Service struct {
	cfg   *config.Config
	store *persistence.Store // from persistence.OpenReadOnly
	rt    runtime.Runtime
	git   *git.Manager // nil: queries that need Git return an error
	now   func() time.Time
	exe   string // caffe binary launched by StartRun/ResumeRun; "" = this executable
}

// New builds a Service. store should come from persistence.OpenReadOnly so
// snapshots never take the write lock.
func New(cfg *config.Config, store *persistence.Store, rt runtime.Runtime, gm *git.Manager) *Service {
	return &Service{cfg: cfg, store: store, rt: rt, git: gm, now: time.Now}
}

func notFound(err error) error {
	if errors.Is(err, persistence.ErrNotFound) {
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	}
	return err
}

// ---------- views ----------

type RunSummary struct {
	ID          string
	Request     string
	Status      c.RunStatus
	Error       string
	BaseCommit  string
	CreatedAt   time.Time
	StartedAt   *time.Time
	CompletedAt *time.Time
	HeartbeatAt *time.Time
	// OwnerAlive: a process is driving the run (fresh heartbeat). False for
	// finished and interrupted runs, and for runs whose owner just died but
	// were not yet flagged interrupted.
	OwnerAlive bool
}

type TaskView struct {
	c.Task
	DependsOn []string // keys, plan order
	Reason    string   // see TaskReason
}

type ExecutionView struct {
	c.Execution
	TaskKey string
	Role    c.Role
	// ExitCodeObserved: the exit code was observed by the orchestrator
	// (validation commands). Agent-reported results are claims and live in
	// the report, never here.
	ExitCodeObserved bool
}

type AgentView struct {
	c.Agent
	TaskKey     string         // of the agent's latest execution
	Execution   *ExecutionView // latest, nil for planning agents
	Visual      VisualState
	Elapsed     time.Duration // of the latest execution, to now if still running
	Observation RuntimeObservation
}

type RunView struct {
	Run        RunSummary
	Tasks      []TaskView
	Agents     []AgentView
	Executions []ExecutionView
	Artifacts  []*c.Artifact
	// Seq: every event with Seq <= this is reflected here. Continue with
	// EventsSince(Seq).
	Seq int64
}

type ExecutionDetail struct {
	ExecutionView
	RunID     string
	Agent     *c.Agent      // nil for validation commands
	Artifacts []*c.Artifact // of the execution's task
}

type ArtifactContent struct {
	Artifact  *c.Artifact
	Text      string // redacted
	Size      int64
	Truncated bool
}

// ---------- queries ----------

func (s *Service) summary(r *c.Run) RunSummary {
	sum := RunSummary{ID: r.ID, Request: r.UserRequest, Status: r.Status, Error: r.Error, BaseCommit: r.Metadata["base_commit"],
		CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, CompletedAt: r.CompletedAt, HeartbeatAt: r.HeartbeatAt}
	sum.OwnerAlive = !r.Status.Terminal() && r.Status != c.RunInterrupted && r.HeartbeatAt != nil &&
		s.now().Sub(*r.HeartbeatAt) < core.StaleAfter
	return sum
}

// Runs lists runs, newest first.
func (s *Service) Runs(ctx context.Context, activeOnly bool) ([]RunSummary, error) {
	runs, err := s.store.ListRuns(ctx, activeOnly)
	if err != nil {
		return nil, err
	}
	out := make([]RunSummary, 0, len(runs))
	for _, r := range runs {
		out = append(out, s.summary(r))
	}
	return out, nil
}

// RunSnapshot returns a consistent view of one run (id or unambiguous
// prefix), with derived fields. It does not ask the runtime: agent visual
// states are derived from stored state only (see AgentRuntimeStatus, F-15).
func (s *Service) RunSnapshot(ctx context.Context, runID string) (*RunView, error) {
	snap, err := s.store.RunSnapshot(ctx, runID)
	if err != nil {
		return nil, notFound(err)
	}
	v := &RunView{Run: s.summary(snap.Run), Artifacts: snap.Artifacts, Seq: snap.Seq}
	byID := map[string]*c.Task{}
	for _, t := range snap.Tasks {
		byID[t.ID] = t
	}
	if len(snap.Tasks) > 0 {
		g, err := core.NewGraph(snap.Tasks)
		if err != nil {
			return nil, fmt.Errorf("run %s: %w", snap.Run.ID, err)
		}
		for _, t := range snap.Tasks {
			tv := TaskView{Task: *t, Reason: TaskReason(t, g, s.cfg.Orchestration.MaxConcurrentAgents, snap.Run.Status)}
			for _, d := range g.Deps(t) {
				tv.DependsOn = append(tv.DependsOn, d.Key)
			}
			v.Tasks = append(v.Tasks, tv)
		}
	}
	latest := map[string]*ExecutionView{} // agent id → latest execution
	for _, e := range snap.Executions {
		ev := ExecutionView{Execution: *e, ExitCodeObserved: e.ExitCode != nil}
		if t := byID[e.TaskID]; t != nil {
			ev.TaskKey, ev.Role = t.Key, t.Role
		}
		v.Executions = append(v.Executions, ev)
		if e.AgentID != "" {
			cp := ev
			latest[e.AgentID] = &cp // executions are ordered oldest first
		}
	}
	for _, a := range snap.Agents {
		av := AgentView{Agent: *a, Execution: latest[a.ID]}
		var ex *c.Execution
		if av.Execution != nil {
			ex = &av.Execution.Execution
			av.TaskKey = av.Execution.TaskKey
			end := s.now()
			if ex.CompletedAt != nil {
				end = *ex.CompletedAt
			}
			av.Elapsed = end.Sub(ex.StartedAt)
		}
		av.Visual = DeriveVisualState(a.Status, ex, RuntimeObservation{})
		v.Agents = append(v.Agents, av)
	}
	return v, nil
}

// EventsSince returns up to limit events after seq, for one run or all
// ("" runID), oldest first.
func (s *Service) EventsSince(ctx context.Context, after int64, runID string, limit int) ([]c.Event, error) {
	return s.store.EventsSince(ctx, after, runID, limit)
}

// Events returns every event of a run in log order, paging internally.
func (s *Service) Events(ctx context.Context, runID string) ([]c.Event, error) {
	var all []c.Event
	var after int64
	for {
		page, err := s.store.EventsSince(ctx, after, runID, 1000)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < 1000 {
			return all, nil
		}
		after = page[len(page)-1].Seq
	}
}

// AgentRuntimeStatus asks the runtime about each agent, keyed by agent id.
// It never fails as a whole: a per-agent problem is in that agent's Err, and
// an agent the runtime does not know is Known && !Found.
func (s *Service) AgentRuntimeStatus(ctx context.Context, agents []AgentView) map[string]RuntimeObservation {
	out := make(map[string]RuntimeObservation, len(agents))
	for _, a := range agents {
		st, err := s.rt.GetAgentStatus(ctx, a.Name)
		switch {
		case err == nil:
			out[a.ID] = RuntimeObservation{Known: true, Found: true, Status: st}
		case errors.Is(err, runtime.ErrNotFound):
			out[a.ID] = RuntimeObservation{Known: true}
		default:
			out[a.ID] = RuntimeObservation{Err: err}
		}
	}
	return out
}

// Execution returns one execution with its task, agent and artifacts.
func (s *Service) Execution(ctx context.Context, id string) (*ExecutionDetail, error) {
	ex, runID, err := s.store.GetExecution(ctx, id)
	if err != nil {
		return nil, notFound(err)
	}
	snap, err := s.RunSnapshot(ctx, runID)
	if err != nil {
		return nil, err
	}
	d := &ExecutionDetail{RunID: runID, ExecutionView: ExecutionView{Execution: *ex, ExitCodeObserved: ex.ExitCode != nil}}
	for _, t := range snap.Tasks {
		if t.ID == ex.TaskID {
			d.TaskKey, d.Role = t.Key, t.Role
		}
	}
	for i := range snap.Agents {
		if snap.Agents[i].ID == ex.AgentID {
			a := snap.Agents[i].Agent
			d.Agent = &a
		}
	}
	for _, a := range snap.Artifacts {
		if a.TaskID == ex.TaskID {
			d.Artifacts = append(d.Artifacts, a)
		}
	}
	return d, nil
}

// ReadArtifact returns up to max bytes of an artifact's file, redacted. Only
// files inside the project's artifact directory or managed worktrees are
// read: the path comes from the database, and the database is not trusted
// to point anywhere on the host.
func (s *Service) ReadArtifact(ctx context.Context, id string, max int64) (*ArtifactContent, error) {
	if max <= 0 {
		return nil, fmt.Errorf("ReadArtifact: max must be positive, got %d", max)
	}
	a, err := s.store.GetArtifact(ctx, id)
	if err != nil {
		return nil, notFound(err)
	}
	if !within(a.Path, s.cfg.ArtifactDir()) && !within(a.Path, s.cfg.Git.WorktreeDirectory) {
		return nil, fmt.Errorf("artifact %s points outside the project's artifact and worktree directories: %s", id, a.Path)
	}
	f, err := os.Open(a.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("artifact %s is not a regular file", id)
	}
	buf, err := io.ReadAll(io.LimitReader(f, max))
	if err != nil {
		return nil, err
	}
	return &ArtifactContent{Artifact: a, Size: fi.Size(), Truncated: fi.Size() > max,
		Text: runtime.Redact(strings.ToValidUTF8(string(buf), "�"))}, nil
}

// within reports whether path resolves to a location strictly inside dir,
// following symlinks so a link cannot point the reader elsewhere.
func within(path, dir string) bool {
	rp, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	rd, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rd, rp)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

// Report renders the consolidated Markdown report of a run.
func (s *Service) Report(ctx context.Context, runID string) (string, error) {
	r, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return "", notFound(err)
	}
	return core.BuildReport(ctx, s.store, r.ID)
}

// Capabilities asks the runtime what it can do.
func (s *Service) Capabilities(ctx context.Context) (*runtime.Capabilities, error) {
	return s.rt.Capabilities(ctx)
}
