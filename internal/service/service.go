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

var ErrNotFound = errors.New("not found")

type Service struct {
	cfg   *config.Config
	store *persistence.Store
	rt    runtime.Runtime
	git   *git.Manager
	now   func() time.Time
	exe   string

	engine Engine
	w      *persistence.Store
}

func New(cfg *config.Config, store *persistence.Store, rt runtime.Runtime, gm *git.Manager) *Service {
	return &Service{cfg: cfg, store: store, rt: rt, git: gm, now: time.Now}
}

func notFound(err error) error {
	if errors.Is(err, persistence.ErrNotFound) {
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	}
	return err
}

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

	OwnerAlive bool
}

type TaskView struct {
	c.Task
	DependsOn []string
	Reason    string
}

type ExecutionView struct {
	c.Execution
	TaskKey string
	Role    c.Role

	ExitCodeObserved bool
}

type AgentView struct {
	c.Agent
	TaskKey     string
	Execution   *ExecutionView
	Visual      VisualState
	Elapsed     time.Duration
	Observation RuntimeObservation
}

type RunView struct {
	Run        RunSummary
	Tasks      []TaskView
	Agents     []AgentView
	Executions []ExecutionView
	Artifacts  []*c.Artifact

	Seq int64
}

type ExecutionDetail struct {
	ExecutionView
	RunID     string
	Agent     *c.Agent
	Artifacts []*c.Artifact

	Report *c.Report

	ClaimsUnverified bool
}

type ArtifactContent struct {
	Artifact  *c.Artifact
	Text      string
	Size      int64
	Truncated bool
}

func (s *Service) summary(r *c.Run) RunSummary {
	sum := RunSummary{ID: r.ID, Request: r.UserRequest, Status: r.Status, Error: r.Error, BaseCommit: r.Metadata["base_commit"],
		CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, CompletedAt: r.CompletedAt, HeartbeatAt: r.HeartbeatAt}
	sum.OwnerAlive = !r.Status.Terminal() && r.Status != c.RunInterrupted && r.HeartbeatAt != nil &&
		s.now().Sub(*r.HeartbeatAt) < core.StaleAfter
	return sum
}

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
	latest := map[string]*ExecutionView{}
	for _, e := range snap.Executions {
		ev := ExecutionView{Execution: *e, ExitCodeObserved: e.ExitCode != nil}
		if t := byID[e.TaskID]; t != nil {
			ev.TaskKey, ev.Role = t.Key, t.Role
		}
		v.Executions = append(v.Executions, ev)
		if e.AgentID != "" {
			cp := ev
			latest[e.AgentID] = &cp
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

func (s *Service) EventsSince(ctx context.Context, after int64, runID string, limit int) ([]c.Event, error) {
	return s.store.EventsSince(ctx, after, runID, limit)
}

func (s *Service) EventsBefore(ctx context.Context, before int64, runID string, limit int) ([]c.Event, error) {
	return s.store.EventsBefore(ctx, before, runID, limit)
}

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
	var own, latest string
	for _, a := range snap.Artifacts {
		if a.TaskID != ex.TaskID {
			continue
		}
		d.Artifacts = append(d.Artifacts, a)
		if a.Type == c.ArtifactReport {
			latest = a.Path
			if ex.AgentID != "" && filepath.Dir(a.Path) == filepath.Dir(ex.Prompt) {
				own = a.Path
			}
		}
	}
	if own == "" {
		own = latest
	}
	if own != "" && within(own, s.cfg.ArtifactDir()) {
		d.Report = core.LoadReport(own)
	}
	execs := make([]*c.Execution, len(snap.Executions))
	for i := range snap.Executions {
		execs[i] = &snap.Executions[i].Execution
	}
	d.ClaimsUnverified = core.ClaimsUnverified(d.Role, execs, ex.TaskID)
	return d, nil
}

func (s *Service) ReadPrompt(ctx context.Context, executionID string, max int64) (*ArtifactContent, error) {
	ex, _, err := s.store.GetExecution(ctx, executionID)
	if err != nil {
		return nil, notFound(err)
	}
	if (ex.AgentID == "" && ex.ExitCode != nil) || !filepath.IsAbs(ex.Prompt) {
		return &ArtifactContent{Text: runtime.Redact(ex.Prompt), Size: int64(len(ex.Prompt))}, nil
	}
	if !within(ex.Prompt, s.cfg.ArtifactDir()) {
		return nil, fmt.Errorf("execution %s: prompt points outside the artifact directory: %s", executionID, ex.Prompt)
	}
	return readBounded(ex.Prompt, max)
}

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
	out, err := readBounded(a.Path, max)
	if err != nil {
		return nil, fmt.Errorf("artifact %s: %w", id, err)
	}
	out.Artifact = a
	return out, nil
}

func readBounded(path string, max int64) (*ArtifactContent, error) {
	if max <= 0 {
		return nil, fmt.Errorf("max must be positive, got %d", max)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	buf, err := io.ReadAll(io.LimitReader(f, max))
	if err != nil {
		return nil, err
	}
	return &ArtifactContent{Size: fi.Size(), Truncated: fi.Size() > max,
		Text: runtime.Redact(strings.ToValidUTF8(string(buf), "�"))}, nil
}

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

func (s *Service) Report(ctx context.Context, runID string) (string, error) {
	r, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return "", notFound(err)
	}
	return core.BuildReport(ctx, s.store, r.ID)
}

type RuntimeInfo = runtime.Capabilities

func (s *Service) Capabilities(ctx context.Context) (*RuntimeInfo, error) {
	return s.rt.Capabilities(ctx)
}
