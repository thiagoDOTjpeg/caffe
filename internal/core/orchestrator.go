package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"caffe/internal/agents"
	"caffe/internal/config"
	c "caffe/internal/contracts"
	"caffe/internal/git"
	"caffe/internal/persistence"
	"caffe/internal/planner"
	"caffe/internal/runtime"
)

// StaleAfter is how long a run may go without a heartbeat before its owning
// process is considered dead.
const StaleAfter = 15 * time.Second

var errCancelledByUser = errors.New("cancelled by user")

// Orchestrator wires planning, scheduling, execution and recovery together.
// Everything external is injected.
type Orchestrator struct {
	Cfg     *config.Config
	Store   *persistence.Store
	RT      runtime.Runtime
	Git     *git.Manager
	Agents  *agents.Manager
	Planner planner.LLMProvider
	Log     zerolog.Logger
	// Progress receives one human-readable line per notable change.
	Progress func(line string)
	// HeartbeatEvery overrides the liveness interval (tests).
	HeartbeatEvery time.Duration

	mu      sync.Mutex
	wtLocks map[string]*sync.RWMutex
	sayMu   sync.Mutex // Progress is called from task goroutines; serialise it
}

// session is the state of the one run this process is driving.
type session struct {
	*Orchestrator
	run   *c.Run
	graph *Graph
	sched *Scheduler

	mu        sync.Mutex
	resumable map[string]*c.Execution // task id -> execution interrupted by a previous process
}

func (o *Orchestrator) say(format string, a ...any) {
	if o.Progress != nil {
		o.sayMu.Lock()
		defer o.sayMu.Unlock()
		o.Progress(fmt.Sprintf(format, a...))
	}
}

// event persists an event. Losing one must not abort the run, but it is logged
// loudly because events are what recovery and inspection rely on.
func (o *Orchestrator) event(e c.Event) {
	if err := o.Store.AddEvent(context.Background(), &e); err != nil {
		o.Log.Error().Err(err).Str("run_id", e.RunID).Str("event_type", e.Type).Msg("could not persist event")
	}
	o.Log.Info().Str("run_id", e.RunID).Str("task_id", e.TaskID).Str("agent_id", e.AgentID).Str("event_type", e.Type).
		Interface("payload", e.Payload).Msg("event")
}

func (o *Orchestrator) warn(runID, taskID, msg string) {
	o.event(c.Event{RunID: runID, TaskID: taskID, Type: c.EventWarning, Payload: map[string]any{"message": msg}})
	o.say("warning: %s", msg)
}

// RunDir is where a run's artifacts are stored.
func (o *Orchestrator) RunDir(runID string) string { return filepath.Join(o.Cfg.ArtifactDir(), runID) }

// wtLock returns the lock of a working directory. Developers take it
// exclusively, readers and validators share it, so nobody reads a tree that is
// being rewritten and two developers never write the same one.
func (o *Orchestrator) wtLock(path string) *sync.RWMutex {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.wtLocks == nil {
		o.wtLocks = map[string]*sync.RWMutex{}
	}
	if o.wtLocks[path] == nil {
		o.wtLocks[path] = &sync.RWMutex{}
	}
	return o.wtLocks[path]
}

// Run plans and executes a request. With a preset plan the planner is skipped.
// The returned error is about the orchestration itself; task failures are
// reflected in the run's status.
func (o *Orchestrator) Run(ctx context.Context, request string, preset *c.ExecutionPlan) (*c.Run, error) {
	return o.RunWithID(ctx, "", request, preset)
}

// RunWithID is Run with a caller-chosen run id, so that a process launching
// `caffe run` knows the id before the run exists. id must be a canonical
// (lowercase, hyphenated) UUID that no run has; "" generates one.
func (o *Orchestrator) RunWithID(ctx context.Context, id, request string, preset *c.ExecutionPlan) (*c.Run, error) {
	if id == "" {
		id = uuid.NewString()
	} else if u, err := uuid.Parse(id); err != nil || u.String() != id {
		return nil, fmt.Errorf("run id %q is not a canonical UUID (like %s)", id, uuid.NewString())
	} else if _, err := o.Store.GetRun(ctx, id); err == nil {
		return nil, fmt.Errorf("a run with id %s already exists", id)
	} else if !errors.Is(err, persistence.ErrNotFound) {
		return nil, err
	}
	if strings.TrimSpace(request) == "" {
		return nil, errors.New("empty request")
	}
	if preset != nil {
		if err := preset.Validate(); err != nil {
			return nil, err
		}
	}
	head, err := o.Git.Head(ctx)
	if err != nil {
		return nil, err
	}
	now := persistence.Now()
	run := &c.Run{ID: id, ProjectPath: o.Cfg.Project.Path, UserRequest: request, Status: c.RunPending,
		CreatedAt: now, HeartbeatAt: &now, Metadata: map[string]string{"base_commit": head}}
	if err := o.Store.CreateRun(ctx, run); err != nil {
		return nil, err
	}
	if _, err := o.Store.Backup(ctx, filepath.Join(filepath.Dir(o.Cfg.Persistence.Path), "backups"), 5); err != nil {
		o.warn(run.ID, "", "database backup failed: "+err.Error())
	}
	o.say("run %s created", run.ID)
	return o.drive(ctx, run, preset, nil, nil)
}

// drive owns a run until it reaches a terminal status.
func (o *Orchestrator) drive(ctx context.Context, run *c.Run, preset *c.ExecutionPlan, tasks []*c.Task, resumable map[string]*c.Execution) (*c.Run, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stopHeartbeat := o.heartbeat(run.ID, cancel)

	s := &session{Orchestrator: o, run: run, resumable: resumable}
	err := s.execute(ctx, preset, tasks)
	stopHeartbeat()
	return run, errors.Join(err, s.finalize(ctx, err))
}

// heartbeat keeps the run marked as owned and watches for a cancellation
// requested by another process (`caffe cancel`).
func (o *Orchestrator) heartbeat(runID string, cancel context.CancelCauseFunc) (stop func()) {
	every := o.HeartbeatEvery
	if every == 0 {
		every = 2 * time.Second
	}
	quit, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			select {
			case <-quit:
				return
			case <-tick.C:
				st, err := o.Store.Heartbeat(context.Background(), runID)
				if err != nil {
					o.Log.Warn().Err(err).Str("run_id", runID).Msg("heartbeat failed")
					continue
				}
				if st == c.RunCancelled {
					cancel(errCancelledByUser)
					return
				}
			}
		}
	}()
	return func() { close(quit); <-done }
}

func (s *session) setRunStatus(to c.RunStatus) error {
	if s.run.Status == to {
		return nil
	}
	from := s.run.Status
	now := persistence.Now()
	s.run.Status = to
	s.run.HeartbeatAt = &now
	if s.run.StartedAt == nil {
		s.run.StartedAt = &now
	}
	if err := s.Store.UpdateRun(context.Background(), s.run, from); err != nil {
		s.run.Status = from
		return err
	}
	s.say("run is %s", to)
	return nil
}

func (s *session) execute(ctx context.Context, preset *c.ExecutionPlan, tasks []*c.Task) error {
	if len(tasks) == 0 {
		plan := preset
		if plan == nil {
			if err := s.setRunStatus(c.RunPlanning); err != nil {
				return err
			}
			if err := s.preflight(ctx, []c.Role{c.RolePlanner}); err != nil {
				return err
			}
			var err error
			plan, err = s.plan(ctx)
			// Planning agents' workspaces are already closed by the agent
			// manager; record that, or their rows stay "running" forever.
			if serr := s.stopAgents(context.WithoutCancel(ctx), s.run.ID); serr != nil {
				s.Log.Warn().Err(serr).Str("run_id", s.run.ID).Msg("could not record planning agents as stopped")
			}
			if err != nil {
				return err
			}
		}
		if err := s.savePlan(plan); err != nil {
			return err
		}
		tasks = tasksFromPlan(s.run.ID, plan, s.Cfg.Orchestration.MaxTaskRetries+1)
		if err := s.Store.CreateTasks(ctx, tasks); err != nil {
			return err
		}
		s.say("plan: %d tasks — %s", len(tasks), plan.Goal)
	}
	var roles []c.Role
	seen := map[c.Role]bool{}
	for _, t := range tasks {
		if !seen[t.Role] && !Finished(t.Status) {
			seen[t.Role] = true
			roles = append(roles, t.Role)
		}
	}
	if err := s.preflight(ctx, roles); err != nil {
		return err
	}
	graph, err := NewGraph(tasks)
	if err != nil {
		return err
	}
	if err := s.setRunStatus(c.RunExecuting); err != nil {
		return err
	}
	s.graph = graph
	s.sched = NewScheduler(s.Store, graph, s.Cfg.Orchestration.MaxConcurrentAgents, s.Cfg.Orchestration.DefaultTimeout, s.execTask,
		s.Log.With().Str("run_id", s.run.ID).Logger())
	s.sched.OnChange = func(t c.Task, from c.TaskStatus, reason string) {
		s.Log.Info().Str("run_id", t.RunID).Str("task_id", t.ID).Str("task", t.Key).Str("event_type", c.TaskEvent(t.Status)).
			Str("from", string(from)).Int("attempts", t.Attempts).Str("reason", reason).Msg("task transition")
		if from == t.Status {
			return
		}
		line := fmt.Sprintf("%-16s %s → %s", t.Key, from, t.Status)
		if reason != "" {
			line += "  (" + reason + ")"
		}
		s.say("%s", line)
	}
	// The run status follows the most advanced phase currently in flight.
	s.sched.OnActive = func(active map[c.Role]int) {
		to := c.RunExecuting
		switch {
		case active[c.RoleDeveloper]+active[c.RoleResearcher] > 0:
		case active[c.RoleReviewer] > 0:
			to = c.RunReviewing
		case active[c.RoleValidator] > 0:
			to = c.RunValidating
		default:
			return
		}
		if err := s.setRunStatus(to); err != nil && !errors.Is(err, persistence.ErrConflict) {
			s.Log.Warn().Err(err).Msg("could not update run phase")
		}
	}
	return s.sched.Run(ctx)
}

// preflight checks, against what the runtime really supports, that every
// agent kind the run needs can be started.
func (s *session) preflight(ctx context.Context, roles []c.Role) error {
	caps, err := s.RT.Capabilities(ctx)
	if err != nil {
		return fmt.Errorf("runtime is not usable: %w", err)
	}
	for _, role := range roles {
		if role == c.RoleValidator && len(s.Cfg.Validation.Commands) > 0 {
			continue // the orchestrator runs the commands itself
		}
		if kind := s.Cfg.Agent(role).Kind; !caps.Supports(kind) {
			return fmt.Errorf("agents.%s.kind=%q cannot be started by this runtime (version %s; available kinds: %s)",
				role, kind, caps.Version, strings.Join(caps.Kinds, ", "))
		}
	}
	return nil
}

// plan asks the planner, retrying when it returns something invalid.
func (s *session) plan(ctx context.Context) (*c.ExecutionPlan, error) {
	overview, err := planner.CollectContext(s.Cfg.Project.Path)
	if err != nil {
		return nil, fmt.Errorf("collect project context: %w", err)
	}
	attempts := s.Cfg.Orchestration.MaxTaskRetries + 1
	for i := 1; ; i++ {
		plan, err := s.Planner.GeneratePlan(ctx, planner.PlanningInput{
			RunID: s.run.ID, UserRequest: s.run.UserRequest, ProjectPath: s.Cfg.Project.Path, Context: overview,
			MaxTasks: 12, Dir: filepath.Join(s.RunDir(s.run.ID), "planner", fmt.Sprintf("attempt-%d", i)),
		})
		if err == nil {
			return plan, nil
		}
		if ctx.Err() != nil || i >= attempts {
			return nil, fmt.Errorf("planning failed: %w", err)
		}
		s.warn(s.run.ID, "", fmt.Sprintf("planning attempt %d of %d failed: %v", i, attempts, err))
	}
}

func (s *session) savePlan(plan *c.ExecutionPlan) error {
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.RunDir(s.run.ID), "plan.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	return s.Store.AddArtifact(context.Background(), &c.Artifact{RunID: s.run.ID, Type: c.ArtifactReport, Path: path, Description: "execution plan"})
}

func tasksFromPlan(runID string, plan *c.ExecutionPlan, maxAttempts int) []*c.Task {
	ids := map[string]string{}
	for _, p := range plan.Tasks {
		ids[p.ID] = uuid.NewString()
	}
	now := persistence.Now()
	tasks := make([]*c.Task, 0, len(plan.Tasks))
	for _, p := range plan.Tasks {
		t := &c.Task{ID: ids[p.ID], RunID: runID, Key: p.ID, Title: p.Title, Description: p.Description, Role: p.Role,
			Status: c.TaskPending, AcceptanceCriteria: p.AcceptanceCriteria, MaxAttempts: maxAttempts, CreatedAt: now}
		for _, d := range p.Dependencies {
			t.Dependencies = append(t.Dependencies, ids[d])
		}
		tasks = append(tasks, t)
	}
	return tasks
}

// finalize records the run's outcome and writes the consolidated report.
func (s *session) finalize(ctx context.Context, runErr error) error {
	db := context.Background()
	stored, err := s.Store.GetRun(db, s.run.ID)
	if err != nil {
		return err
	}
	outcome, msg := c.RunCompleted, ""
	switch {
	case stored.Status == c.RunCancelled || errors.Is(context.Cause(ctx), errCancelledByUser):
		outcome, msg = c.RunCancelled, "cancelled by user"
	case ctx.Err() != nil:
		outcome, msg = c.RunCancelled, "interrupted by signal"
	case runErr != nil:
		outcome, msg = c.RunFailed, runErr.Error()
	default:
		var bad []string
		for _, t := range s.graph.Tasks() {
			if st := s.sched.Snapshot(t); st.Status != c.TaskCompleted {
				bad = append(bad, fmt.Sprintf("%s %s", st.Key, st.Status))
			}
		}
		if len(bad) > 0 {
			sort.Strings(bad)
			outcome, msg = c.RunFailed, "tasks not completed: "+strings.Join(bad, ", ")
		}
	}
	if stored.Status != outcome {
		from := stored.Status
		now := persistence.Now()
		s.run.Status, s.run.Error, s.run.CompletedAt, s.run.HeartbeatAt = outcome, msg, &now, &now
		if err := s.Store.UpdateRun(db, s.run, from); err != nil {
			return err
		}
	} else {
		*s.run = *stored
	}
	report, err := BuildReport(db, s.Store, s.run.ID)
	if err != nil {
		return err
	}
	path := filepath.Join(s.RunDir(s.run.ID), "report.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
		return err
	}
	s.say("run %s: %s — report: %s", s.run.Status, s.run.ID, path)
	return s.Store.AddArtifact(db, &c.Artifact{RunID: s.run.ID, Type: c.ArtifactReport, Path: path, Description: "consolidated run report"})
}

// PlannerSession persists the planning agent so it shows up in `caffe agents`
// and can be cleaned up after a crash.
func (o *Orchestrator) PlannerSession(runID string, sess agents.Session) {
	now := persistence.Now()
	a := &c.Agent{ID: "planner-" + sess.Name, RunID: runID, Name: sess.Name, Kind: sess.Kind, Role: c.RolePlanner, Status: "running",
		WorkspaceID: sess.WorkspaceID, PaneID: sess.PaneID, RuntimeSessionID: sess.TerminalID, LastHeartbeat: &now}
	if err := o.Store.SaveAgent(context.Background(), a); err != nil {
		o.Log.Error().Err(err).Str("run_id", runID).Msg("could not persist planner agent")
	}
}

// PlannerBlocked surfaces a planning agent that waits for a human.
func (o *Orchestrator) PlannerBlocked(runID string, blocked bool) {
	if blocked {
		o.warn(runID, "", "the planning agent is waiting for human input in its Herdr pane")
	}
}

// Cancel stops a run. If its owning process is alive, that process notices
// within one heartbeat and stops its agents itself. Otherwise the leftovers
// are cleaned up here.
func (o *Orchestrator) Cancel(ctx context.Context, idOrPrefix string) (*c.Run, error) {
	run, err := o.Store.GetRun(ctx, idOrPrefix)
	if err != nil {
		return nil, err
	}
	if run.Status.Terminal() {
		return nil, fmt.Errorf("run %s is already %s", run.ID, run.Status)
	}
	ownerAlive := run.Status != c.RunInterrupted && run.HeartbeatAt != nil && time.Since(*run.HeartbeatAt) < StaleAfter
	from := run.Status
	now := persistence.Now()
	run.Status, run.Error, run.CompletedAt = c.RunCancelled, "cancelled by user", &now
	if err := o.Store.UpdateRun(ctx, run, from); err != nil {
		return nil, err
	}
	if ownerAlive {
		return run, nil
	}
	tasks, err := o.Store.ListTasks(ctx, run.ID)
	if err != nil {
		return run, err
	}
	var errs []error
	for _, t := range tasks {
		if !Finished(t.Status) {
			if _, err := transition(ctx, o.Store, *t, c.TaskCancelled, "run cancelled", nil); err != nil {
				errs = append(errs, err)
			}
		}
	}
	execs, err := o.Store.ListExecutions(ctx, run.ID)
	if err != nil {
		return run, errors.Join(append(errs, err)...)
	}
	for _, e := range execs {
		if e.Status == c.ExecRunning || e.Status == c.ExecInterrupted {
			e.Status, e.CompletedAt, e.Error = c.ExecCancelled, &now, "run cancelled"
			errs = append(errs, o.Store.SaveExecution(ctx, e))
		}
	}
	errs = append(errs, o.stopAgents(ctx, run.ID))
	return run, errors.Join(errs...)
}

// stopAgents closes the runtime workspaces of a run's agents that are not
// known to be stopped.
func (o *Orchestrator) stopAgents(ctx context.Context, runID string) error {
	list, err := o.Store.ListAgents(ctx, runID)
	if err != nil {
		return err
	}
	var errs []error
	for _, a := range list {
		if a.Status == "stopped" || a.WorkspaceID == "" {
			continue
		}
		// Only close the workspace if it still hosts OUR agent: workspace ids
		// are reused by the runtime after a restart.
		if _, err := o.RT.GetAgentStatus(ctx, a.Name); err == nil {
			if err := o.RT.StopAgent(ctx, a.WorkspaceID); err != nil {
				errs = append(errs, fmt.Errorf("stop agent %s: %w", a.Name, err))
				continue
			}
		} else if !errors.Is(err, runtime.ErrNotFound) {
			errs = append(errs, fmt.Errorf("agent %s: %w", a.Name, err))
			continue
		}
		a.Status, a.CurrentTaskID = "stopped", ""
		errs = append(errs, o.Store.SaveAgent(ctx, a))
	}
	return errors.Join(errs...)
}

// Clean removes the worktrees of finished runs. Worktrees of runs or tasks
// that did not complete are kept unless includeFailed is set or the
// configuration says not to preserve them, and leased worktrees are always
// kept. Branches are never deleted, and Git refuses to remove a worktree with
// uncommitted changes.
func (o *Orchestrator) Clean(ctx context.Context, includeFailed bool) ([]string, error) {
	includeFailed = includeFailed || !o.Cfg.Security.PreserveFailedWorktrees
	runs, err := o.Store.ListRuns(ctx, false)
	if err != nil {
		return nil, err
	}
	var actions []string
	var errs []error
	for _, run := range runs {
		if !run.Status.Terminal() {
			continue
		}
		errs = append(errs, o.stopAgents(ctx, run.ID))
		tasks, err := o.Store.ListTasks(ctx, run.ID)
		if err != nil {
			return actions, err
		}
		for _, t := range tasks {
			if t.WorktreePath == "" {
				continue
			}
			if _, err := os.Stat(t.WorktreePath); errors.Is(err, os.ErrNotExist) {
				continue
			}
			// A leased worktree is in use: by a human editing it in Studio, or
			// an agent (WS-16).
			if l, err := o.Store.GetLease(ctx, t.WorktreePath); err == nil {
				actions = append(actions, fmt.Sprintf("kept    %s (leased by %s %s since %s)", t.WorktreePath, l.HolderKind, l.HolderID, l.AcquiredAt.Local().Format("2006-01-02 15:04")))
				continue
			} else if !errors.Is(err, persistence.ErrNotFound) {
				return actions, err
			}
			// A worktree is only disposable when both its task and its run
			// succeeded; anything else is evidence someone may want to look at.
			if (t.Status != c.TaskCompleted || run.Status != c.RunCompleted) && !includeFailed {
				actions = append(actions, fmt.Sprintf("kept    %s (run %s, task %s %s; use --failed to remove)", t.WorktreePath, run.Status, t.Key, t.Status))
				continue
			}
			if err := o.Git.RemoveWorktree(ctx, t.WorktreePath); err != nil {
				actions = append(actions, fmt.Sprintf("kept    %s (%v)", t.WorktreePath, err))
				continue
			}
			actions = append(actions, fmt.Sprintf("removed %s (branch %s kept)", t.WorktreePath, t.BranchName))
			o.event(c.Event{RunID: run.ID, TaskID: t.ID, Type: c.EventWorktreeRemoved, Payload: map[string]any{"path": t.WorktreePath, "branch": t.BranchName}})
		}
	}
	return actions, errors.Join(errs...)
}
