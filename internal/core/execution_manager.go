package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"caffe/internal/agents"
	c "caffe/internal/contracts"
	"caffe/internal/git"
	"caffe/internal/persistence"
	"caffe/internal/proctree"
	"caffe/internal/runtime"
)

// execTask is the scheduler's TaskFunc: it applies the policy of the task's role.
func (s *session) execTask(ctx context.Context, t *c.Task) error {
	switch t.Role {
	case c.RoleResearcher:
		return s.runResearcher(ctx, t)
	case c.RoleDeveloper:
		return s.runDeveloper(ctx, t)
	case c.RoleReviewer:
		return s.runReviewer(ctx, t)
	case c.RoleValidator:
		return s.runValidator(ctx, t)
	}
	return Permanent(fmt.Errorf("task %s has unknown role %q", t.Key, t.Role))
}

func short(id string) string { return strings.ReplaceAll(id, "-", "")[:8] }

// ---------- researcher ----------

func (s *session) runResearcher(ctx context.Context, t *c.Task) error {
	project := s.Cfg.Project.Path
	before, err := s.Git.Status(ctx, project)
	if err != nil {
		return err
	}
	if _, err := s.runAgent(ctx, &agentRun{owner: t, role: c.RoleResearcher, cwd: project, tracked: true}); err != nil {
		return err
	}
	// Read-only is a policy we can only verify after the fact. The user may be
	// editing the checkout too, so this is a warning, and nothing is reverted.
	if after, err := s.Git.Status(context.WithoutCancel(ctx), project); err == nil && after != before {
		s.warn(s.run.ID, t.ID, fmt.Sprintf("the project checkout changed while read-only task %s ran; check `git status`", t.Key))
	}
	return nil
}

// ---------- developer ----------

func (s *session) runDeveloper(ctx context.Context, t *c.Task) error {
	snap := s.sched.Snapshot(t)
	wt := snap.WorktreePath
	if wt == "" {
		base := s.run.Metadata["base_commit"]
		if dep := s.graph.Nearest(t, c.RoleDeveloper); dep != nil {
			// Build on the work this task depends on, not on the original base.
			if b := s.sched.Snapshot(dep).BranchName; b != "" {
				base = b
			}
		}
		name := filepath.Join(short(s.run.ID), t.Key)
		branch := s.Cfg.Git.BranchPrefix + "/" + short(s.run.ID) + "/" + t.Key
		var err error
		if wt, err = s.Git.CreateWorktree(ctx, name, branch, base); err != nil {
			return Permanent(fmt.Errorf("create worktree: %w", err))
		}
		if err := s.sched.Transition(ctx, t, snap.Status, "worktree created", func(t *c.Task) { t.WorktreePath, t.BranchName = wt, branch }); err != nil {
			return err
		}
		s.event(c.Event{RunID: s.run.ID, TaskID: t.ID, Type: c.EventWorktreeCreated, Payload: map[string]any{"path": wt, "branch": branch, "base": base}})
		snap.BranchName = branch
	}
	lock := s.wtLock(wt)
	lock.Lock()
	defer lock.Unlock()

	ar := &agentRun{owner: t, role: c.RoleDeveloper, cwd: wt, tracked: true}
	ar.data.Branch = snap.BranchName
	// Any attempt after the first inherits a worktree in an unknown state.
	ar.data.Interrupted, ar.data.PriorError = snap.Attempts > 1, snap.Error
	if _, err := s.runAgent(ctx, ar); err != nil {
		return err
	}
	return s.collectChanges(ctx, t, wt)
}

func (s *session) diffPath(dev *c.Task) string {
	return filepath.Join(s.RunDir(s.run.ID), dev.Key, "changes.diff")
}

// diffBase is the commit a developer task's work started from.
func (s *session) diffBase(ctx context.Context, dev *c.Task, wt string) (string, error) {
	return DiffBase(ctx, s.Git, wt, s.graph, dev, s.run.Metadata["base_commit"],
		func(t *c.Task) string { return s.sched.Snapshot(t).BranchName })
}

// DiffBase is the commit a developer task's changes are measured from: the
// merge-base of the worktree's HEAD with the branch of the nearest developer
// task it depends on (it started from that branch), or the run's base commit
// when there is none. branchOf reads a task's branch; the engine passes a
// locked read of its in-memory state, readers of persisted state pass
// t.BranchName.
func DiffBase(ctx context.Context, gm *git.Manager, worktree string, g *Graph, dev *c.Task, runBase string,
	branchOf func(*c.Task) string) (string, error) {
	if dep := g.Nearest(dev, c.RoleDeveloper); dep != nil {
		if b := branchOf(dep); b != "" {
			return gm.MergeBase(ctx, worktree, "HEAD", b)
		}
	}
	return runBase, nil
}

// collectChanges puts a developer's work on its branch and stores the diff.
// A developer that claims completion without changing anything has not
// completed anything.
func (s *session) collectChanges(ctx context.Context, dev *c.Task, wt string) error {
	if _, err := s.Git.CommitAll(ctx, wt, fmt.Sprintf("caffe: %s [%s]", dev.Title, dev.Key)); err != nil {
		return fmt.Errorf("commit worktree: %w", err)
	}
	base, err := s.diffBase(ctx, dev, wt)
	if err != nil {
		return err
	}
	diff, err := s.Git.Diff(ctx, wt, base)
	if err != nil {
		return err
	}
	if strings.TrimSpace(diff) == "" {
		return fmt.Errorf("developer reported completion but the worktree has no changes relative to %s", base)
	}
	path := s.diffPath(dev)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(diff+"\n"), 0o644); err != nil {
		return err
	}
	files, err := s.Git.ChangedFiles(ctx, wt, base)
	if err != nil {
		return err
	}
	s.event(c.Event{RunID: s.run.ID, TaskID: dev.ID, Type: c.EventDiffCollected, Payload: map[string]any{"path": path, "base": base, "files": files}})
	return s.Store.AddArtifact(ctx, &c.Artifact{RunID: s.run.ID, TaskID: dev.ID, Type: c.ArtifactDiff, Path: path,
		Description: fmt.Sprintf("changes of task %s relative to %s (%d files)", dev.Key, base, len(files))})
}

// ---------- reviewer ----------

// runReviewer reviews the developer task this task depends on. A rejection
// sends the findings back to a developer agent in the same worktree and
// reviews again, up to max_review_rounds times.
func (s *session) runReviewer(ctx context.Context, t *c.Task) error {
	dev := s.graph.Nearest(t, c.RoleDeveloper)
	if dev == nil {
		rep, err := s.runAgent(ctx, &agentRun{owner: t, role: c.RoleReviewer, cwd: s.Cfg.Project.Path, tracked: true})
		if err != nil {
			return err
		}
		if !*rep.Approved {
			return Permanent(fmt.Errorf("review rejected: %s", rep.Summary))
		}
		return nil
	}
	wt := s.sched.Snapshot(dev).WorktreePath
	lock := s.wtLock(wt)
	for round := 0; ; round++ {
		rep, err := s.reviewOnce(ctx, t, dev, wt)
		if err != nil {
			return err
		}
		if *rep.Approved {
			return nil
		}
		s.say("%-16s review rejected with %d findings (round %d)", t.Key, len(rep.Findings), round+1)
		if round >= s.Cfg.Orchestration.MaxReviewRounds {
			return Permanent(fmt.Errorf("changes of task %s still rejected after %d correction rounds: %s", dev.Key, round, rep.Summary))
		}
		if err := s.correct(ctx, dev, wt, rep.Findings, lock.Lock, lock.Unlock); err != nil {
			return Permanent(fmt.Errorf("correction of task %s failed: %w", dev.Key, err))
		}
	}
}

func (s *session) reviewOnce(ctx context.Context, t, dev *c.Task, wt string) (*c.Report, error) {
	lock := s.wtLock(wt)
	// Make sure the diff reflects the worktree as it is now (it may hold
	// changes from an interrupted correction).
	lock.Lock()
	err := s.collectChanges(ctx, dev, wt)
	lock.Unlock()
	if err != nil {
		return nil, err
	}
	lock.RLock()
	defer lock.RUnlock()
	base, err := s.diffBase(ctx, dev, wt)
	if err != nil {
		return nil, err
	}
	ar := &agentRun{owner: t, role: c.RoleReviewer, cwd: wt, tracked: true}
	ar.data.DiffPath, ar.data.BaseCommit = s.diffPath(dev), base
	rep, err := s.runAgent(ctx, ar)
	// A reviewer must not change the tree it reviews. The worktree is ours and
	// fully committed, so discarding stray edits is safe and keeps them out of
	// the developer's branch.
	if dirty, derr := s.Git.IsDirty(context.WithoutCancel(ctx), wt); derr == nil && dirty {
		s.warn(s.run.ID, t.ID, fmt.Sprintf("reviewer %s modified the worktree it was reviewing; the changes were discarded", t.Key))
		if rerr := s.Git.Restore(context.WithoutCancel(ctx), wt); rerr != nil {
			return nil, errors.Join(err, rerr)
		}
	}
	return rep, err
}

// correct runs a developer agent on a rejected change. The developer task
// goes completed -> reviewing -> completed (or failed).
func (s *session) correct(ctx context.Context, dev *c.Task, wt string, findings []c.Finding, lock, unlock func()) error {
	db := context.WithoutCancel(ctx)
	if err := s.sched.Transition(db, dev, c.TaskReviewing, "review rejected, correcting", func(t *c.Task) { t.Attempts++ }); err != nil {
		return err
	}
	lock()
	ar := &agentRun{owner: dev, role: c.RoleDeveloper, cwd: wt}
	ar.data.Branch, ar.data.Findings = s.sched.Snapshot(dev).BranchName, findings
	_, err := s.runAgent(ctx, ar)
	if err == nil {
		err = s.collectChanges(ctx, dev, wt)
	}
	unlock()
	switch {
	case err == nil:
		return s.sched.Transition(db, dev, c.TaskCompleted, "corrected", nil)
	case ctx.Err() != nil:
		return errors.Join(err, s.sched.Transition(db, dev, c.TaskCancelled, "run stopped during correction", nil))
	default:
		return errors.Join(err, s.sched.Transition(db, dev, c.TaskFailed, "correction failed: "+err.Error(), nil))
	}
}

// ---------- validator ----------

func (s *session) runValidator(ctx context.Context, t *c.Task) error {
	cwd := s.Cfg.Project.Path
	if dev := s.graph.Nearest(t, c.RoleDeveloper); dev != nil {
		if wt := s.sched.Snapshot(dev).WorktreePath; wt != "" {
			cwd = wt
		}
	}
	lock := s.wtLock(cwd)
	lock.RLock()
	defer lock.RUnlock()
	if len(s.Cfg.Validation.Commands) > 0 {
		return s.runCommands(ctx, t, cwd)
	}
	// No commands configured: an agent finds and runs the checks. Its exit
	// codes are claims, and are labelled as such in the report.
	_, err := s.runAgent(ctx, &agentRun{owner: t, role: c.RoleValidator, cwd: cwd, tracked: true})
	return err
}

// runCommands executes the configured validation commands itself and records
// their real exit codes. Commands are split on whitespace and run without a
// shell, so pipes, globs and variables are not interpreted.
func (s *session) runCommands(ctx context.Context, t *c.Task, cwd string) error {
	db := context.WithoutCancel(ctx)
	if err := s.sched.Transition(ctx, t, c.TaskRunning, "running validation commands", nil); err != nil {
		return err
	}
	attempt := s.sched.Snapshot(t).Attempts
	dir := filepath.Join(s.RunDir(s.run.ID), t.Key, fmt.Sprintf("attempt-%d", attempt))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	rep := c.Report{Status: "completed"}
	failed := 0
	for i, line := range s.Cfg.Validation.Commands {
		argv := strings.Fields(line)
		ex := &c.Execution{ID: uuid.NewString(), TaskID: t.ID, Attempt: attempt, Status: c.ExecRunning, Prompt: line, StartedAt: persistence.Now()}
		if err := s.Store.SaveExecution(ctx, ex); err != nil {
			return err
		}
		logPath := filepath.Join(dir, fmt.Sprintf("command-%d.log", i+1))
		code, runErr := runCommand(ctx, s.Cfg.Validation.Timeout, cwd, argv, logPath)
		now := persistence.Now()
		ex.ExitCode, ex.CompletedAt, ex.Status = &code, &now, c.ExecCompleted
		ex.Output = runtime.Redact(fileTail(logPath, 2000))
		if runErr != nil || code != 0 {
			ex.Status = c.ExecFailed
			failed++
			if runErr != nil {
				ex.Error = runErr.Error()
			}
		}
		if ctx.Err() != nil {
			ex.Status = c.ExecCancelled
		}
		if err := s.Store.SaveExecution(db, ex); err != nil {
			return err
		}
		if err := s.Store.AddArtifact(db, &c.Artifact{RunID: s.run.ID, TaskID: t.ID, Type: c.ArtifactTestResult, Path: logPath,
			Description: fmt.Sprintf("`%s` exit code %d", line, code)}); err != nil {
			return err
		}
		s.event(c.Event{RunID: s.run.ID, TaskID: t.ID, Type: c.EventValidationCommand, Payload: map[string]any{
			"execution_id": ex.ID, "command": line, "exit_code": code, "duration_ms": now.Sub(ex.StartedAt).Milliseconds()}})
		s.say("%-16s `%s` → exit %d", t.Key, line, code)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rep.Tests = append(rep.Tests, c.CommandRun{Command: line, ExitCode: code, Summary: ex.Error})
	}
	rep.Summary = fmt.Sprintf("%d of %d validation commands passed (executed by the orchestrator in %s)", len(rep.Tests)-failed, len(rep.Tests), cwd)
	if failed > 0 {
		rep.Status = "failed"
	}
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	reportPath := filepath.Join(dir, "report.json")
	if err := os.WriteFile(reportPath, data, 0o644); err != nil {
		return err
	}
	if err := s.Store.AddArtifact(db, &c.Artifact{RunID: s.run.ID, TaskID: t.ID, Type: c.ArtifactReport, Path: reportPath, Description: "validation result"}); err != nil {
		return err
	}
	if failed > 0 {
		return Permanent(fmt.Errorf("validation failed: %d of %d commands exited non-zero", failed, len(rep.Tests)))
	}
	return nil
}

// runCommand runs argv in dir with its output going to logPath and returns the
// real exit code (-1 if the process could not be started or was killed).
func runCommand(ctx context.Context, timeout time.Duration, dir string, argv []string, logPath string) (int, error) {
	out, err := os.Create(logPath)
	if err != nil {
		return -1, err
	}
	defer out.Close()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = out, out
	cmd.WaitDelay = 5 * time.Second
	// The command and everything it starts form one process tree (a process
	// group on Unix, a job object on Windows), killed as a whole on
	// timeout/cancel and after the command exits: `go test` and friends spawn
	// children that would otherwise outlive the validation.
	tree, err := proctree.Start(cmd)
	if err == nil {
		err = cmd.Wait()
		tree.Kill()
	}
	// A command we killed has no result of its own. Its exit status is an
	// artefact of how it was killed (-1 after SIGKILL on Unix, the code given
	// to TerminateJobObject on Windows), never something to record as the
	// command's exit code.
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return -1, fmt.Errorf("timed out after %s", timeout)
	case ctx.Err() != nil:
		return -1, fmt.Errorf("cancelled: %w", ctx.Err())
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exitErr):
		return exitErr.ExitCode(), nil // a non-zero exit is a result, not an error
	default:
		return -1, err
	}
}

func fileTail(path string, n int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() > n {
		if _, err := f.Seek(-n, 2); err != nil {
			return ""
		}
	}
	buf := make([]byte, n)
	read, _ := f.Read(buf)
	return strings.ToValidUTF8(string(buf[:read]), "")
}

// ---------- agent executions ----------

type agentRun struct {
	owner *c.Task // the task this execution belongs to
	role  c.Role
	cwd   string
	data  agents.PromptData
	// tracked: owner is the task the scheduler dispatched, so its
	// running/blocked status follows the agent. False for corrections, where
	// owner is the developer task being fixed on behalf of a reviewer.
	tracked bool
}

// runAgent performs one agent execution for a task: persists it, runs the
// agent (or re-attaches to one that survived a restart), stores what it
// produced and validates its report.
func (s *session) runAgent(ctx context.Context, ar *agentRun) (*c.Report, error) {
	db := context.WithoutCancel(ctx)
	owner := s.sched.Snapshot(ar.owner)
	spec := s.Cfg.Agent(ar.role)
	log := s.Log.With().Str("run_id", s.run.ID).Str("task_id", owner.ID).Str("task", owner.Key).Logger()

	ex, agent, attach := s.reattach(ctx, ar)
	if ex == nil {
		ex = &c.Execution{ID: uuid.NewString(), TaskID: owner.ID, Attempt: owner.Attempts, StartedAt: persistence.Now()}
		agent = &c.Agent{ID: uuid.NewString(), RunID: s.run.ID, Kind: spec.Kind, Role: ar.role, Status: "starting", Metadata: map[string]string{}}
	}
	dir := filepath.Join(s.RunDir(s.run.ID), owner.Key, short(ex.ID))
	ex.Status, ex.Prompt, ex.CompletedAt, ex.Error = c.ExecRunning, filepath.Join(dir, "prompt.md"), nil, ""

	req := agents.Request{ExecutionID: ex.ID, Role: ar.role, Kind: spec.Kind, Args: spec.Args, Cwd: ar.cwd, Dir: dir, Attach: attach}
	if attach == nil {
		d := ar.data
		d.UserRequest, d.ProjectPath, d.Cwd = s.run.UserRequest, s.Cfg.Project.Path, ar.cwd
		d.ReportPath = agents.ReportPath(ar.cwd, ex.ID)
		d.TaskKey, d.Title, d.Description, d.Criteria = owner.Key, owner.Title, owner.Description, owner.AcceptanceCriteria
		d.Upstream = s.upstream(ctx, ar.owner)
		prompt, err := agents.Render(ar.role, d)
		if err != nil {
			return nil, Permanent(err)
		}
		req.Prompt = prompt
	}
	if err := s.Store.SaveExecution(ctx, ex); err != nil {
		return nil, err
	}

	markRunning := func() {
		if !ar.tracked {
			return
		}
		if st := s.sched.Snapshot(ar.owner).Status; st == c.TaskAssigned || st == c.TaskBlocked {
			if err := s.sched.Transition(db, ar.owner, c.TaskRunning, "agent "+agent.Name, func(t *c.Task) { t.AssignedAgentID = agent.ID }); err != nil {
				log.Error().Err(err).Msg("could not mark task running")
			}
		}
	}
	req.OnSession = func(sess agents.Session) {
		now := persistence.Now()
		agent.Name, agent.WorkspaceID, agent.PaneID, agent.RuntimeSessionID = sess.Name, sess.WorkspaceID, sess.PaneID, sess.TerminalID
		agent.Status, agent.CurrentTaskID, agent.LastHeartbeat = "running", owner.ID, &now
		ex.AgentID = agent.ID
		// Persist the session before anything else: after a crash this row is
		// the only way to find the agent again.
		if err := errors.Join(s.Store.SaveAgent(db, agent), s.Store.SaveExecution(db, ex)); err != nil {
			log.Error().Err(err).Str("agent", sess.Name).Msg("could not persist agent session")
		}
		markRunning()
	}
	req.OnBlocked = func(blocked bool) {
		if blocked {
			s.say("%-16s agent %s is waiting for a human — attach with: %s", owner.Key, agent.Name, s.attachHint(agent.Name))
		}
		s.event(c.Event{RunID: s.run.ID, TaskID: owner.ID, AgentID: agent.ID, Type: c.EventAgentBlocked, Payload: map[string]any{"blocked": blocked, "agent": agent.Name}})
		if !ar.tracked {
			return
		}
		to, reason := c.TaskRunning, "agent resumed"
		if blocked {
			to, reason = c.TaskBlocked, "agent "+agent.Name+" is waiting for human input: "+s.attachHint(agent.Name)
		}
		if st := s.sched.Snapshot(ar.owner).Status; st != to && CanTransition(st, to) {
			if err := s.sched.Transition(db, ar.owner, to, reason, nil); err != nil {
				log.Error().Err(err).Msg("could not record blocked state")
			}
		}
	}
	if attach != nil {
		markRunning()
	}

	res, err := s.Agents.Execute(ctx, req)

	now := persistence.Now()
	ex.CompletedAt = &now
	if agent.Name != "" {
		agent.Status, agent.CurrentTaskID, agent.LastHeartbeat = "stopped", "", &now
		if serr := s.Store.SaveAgent(db, agent); serr != nil {
			log.Error().Err(serr).Msg("could not persist agent state")
		}
	}
	if res != nil && res.OutputPath != "" {
		ex.Output = fileTail(res.OutputPath, 2000)
		if aerr := s.Store.AddArtifact(db, &c.Artifact{RunID: s.run.ID, TaskID: owner.ID, Type: c.ArtifactLog, Path: res.OutputPath,
			Description: fmt.Sprintf("terminal output of %s agent %s", ar.role, agent.Name)}); aerr != nil {
			log.Error().Err(aerr).Msg("could not record output artifact")
		}
	}
	if errors.Is(err, agents.ErrNoReport) && res != nil && res.OutputPath != "" {
		// The usual causes (agent not signed in, a dialog the runtime does not
		// recognise as blocking) are only visible on its screen.
		err = fmt.Errorf("%w; its last screen is in %s", err, res.OutputPath)
	}
	var report *c.Report
	if err == nil {
		if aerr := s.Store.AddArtifact(db, &c.Artifact{RunID: s.run.ID, TaskID: owner.ID, Type: c.ArtifactReport, Path: res.ReportPath,
			Description: fmt.Sprintf("%s report (%s agent %s)", ar.role, spec.Kind, agent.Name)}); aerr != nil {
			err = aerr
		}
	}
	if err == nil {
		if report, err = c.ParseReport(ar.role, res.Report); err == nil && report.Status == "failed" {
			err = fmt.Errorf("agent reported failure: %s", report.Summary)
		}
	}
	switch {
	case err == nil:
		ex.Status = c.ExecCompleted
	case ctx.Err() != nil:
		ex.Status, ex.Error = c.ExecCancelled, err.Error()
	default:
		ex.Status, ex.Error = c.ExecFailed, err.Error()
	}
	if serr := s.Store.SaveExecution(db, ex); serr != nil {
		return nil, errors.Join(err, serr)
	}
	s.event(c.Event{RunID: s.run.ID, TaskID: owner.ID, AgentID: agent.ID, Type: c.ExecutionEvent(ex.Status), Payload: map[string]any{
		"execution_id": ex.ID, "role": ar.role, "kind": spec.Kind, "attempt": ex.Attempt, "reattached": attach != nil,
		"duration_ms": now.Sub(ex.StartedAt).Milliseconds(), "error": ex.Error}})
	if err != nil {
		return nil, err
	}
	return report, nil
}

// attachHint tells a human how to reach a blocked agent, if the runtime can
// attach them to it (runtime.Attacher).
func (s *session) attachHint(agent string) string {
	if at, ok := s.RT.(runtime.Attacher); ok {
		if argv, err := at.AttachCommand(agent); err == nil {
			return strings.Join(argv, " ")
		}
	}
	return "open it in the agent runtime (this runtime cannot attach from Caffè)"
}

// reattach decides what to do with an execution that a previous orchestrator
// process left unfinished for this task. If its agent is still alive, or its
// report is already on disk, the execution is continued and nothing is
// prompted again. Otherwise its outcome is unknowable: it is recorded as
// unknown and the caller starts a new execution that is told about it.
func (s *session) reattach(ctx context.Context, ar *agentRun) (*c.Execution, *c.Agent, *agents.Session) {
	s.mu.Lock()
	ex := s.resumable[ar.owner.ID]
	delete(s.resumable, ar.owner.ID)
	s.mu.Unlock()
	if ex == nil {
		return nil, nil, nil
	}
	db := context.WithoutCancel(ctx)
	giveUp := func(why string) (*c.Execution, *c.Agent, *agents.Session) {
		now := persistence.Now()
		ex.Status, ex.CompletedAt, ex.Error = c.ExecUnknown, &now, why
		if err := s.Store.SaveExecution(db, ex); err != nil {
			s.Log.Error().Err(err).Str("execution_id", ex.ID).Msg("could not mark execution unknown")
		}
		s.event(c.Event{RunID: s.run.ID, TaskID: ar.owner.ID, Type: c.ExecutionEvent(c.ExecUnknown), Payload: map[string]any{"execution_id": ex.ID, "reason": why}})
		ar.data.Interrupted = true
		if ar.data.PriorError == "" {
			ar.data.PriorError = "the orchestrator was interrupted"
		}
		return nil, nil, nil
	}
	if ex.AgentID == "" {
		return giveUp("interrupted before an agent session was recorded")
	}
	agent, err := s.Store.GetAgent(ctx, ex.AgentID)
	if err != nil {
		return giveUp("agent record not found: " + err.Error())
	}
	_, statusErr := s.RT.GetAgentStatus(ctx, agent.Name)
	_, reportErr := os.Lstat(agents.ReportPath(ar.cwd, ex.ID))
	if statusErr != nil && reportErr != nil {
		return giveUp(fmt.Sprintf("agent %s is gone (%v) and left no report", agent.Name, statusErr))
	}
	s.say("%-16s re-attaching to agent %s from the interrupted run", ar.owner.Key, agent.Name)
	return ex, agent, &agents.Session{Name: agent.Name, Kind: agent.Kind, WorkspaceID: agent.WorkspaceID, PaneID: agent.PaneID, TerminalID: agent.RuntimeSessionID}
}

const maxUpstreamChars = 4000

// upstream collects the latest report of each direct dependency. It reads
// from stored artifacts, so it works the same after a restart.
func (s *session) upstream(ctx context.Context, t *c.Task) []agents.Upstream {
	arts, err := s.Store.ListArtifacts(ctx, s.run.ID)
	if err != nil {
		s.Log.Warn().Err(err).Msg("could not load upstream reports")
		return nil
	}
	latest := map[string]string{}
	for _, a := range arts {
		if a.Type == c.ArtifactReport && a.TaskID != "" {
			latest[a.TaskID] = a.Path
		}
	}
	var out []agents.Upstream
	for _, dep := range s.graph.Deps(t) {
		path := latest[dep.ID]
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var rep c.Report
		if json.Unmarshal(data, &rep) != nil {
			continue
		}
		var b strings.Builder
		b.WriteString(rep.Summary)
		if rep.Recommendation != "" {
			b.WriteString("\n\nRecommendation: " + rep.Recommendation)
		}
		if len(rep.RelevantFiles) > 0 {
			b.WriteString("\n\nRelevant files: " + strings.Join(rep.RelevantFiles, ", "))
		}
		for _, r := range rep.Risks {
			b.WriteString("\nRisk: " + r)
		}
		for _, f := range rep.Findings {
			b.WriteString(fmt.Sprintf("\nFinding [%s] %s:%d %s", f.Severity, f.File, f.Line, f.Description))
		}
		for _, l := range rep.Limitations {
			b.WriteString("\nLimitation: " + l)
		}
		summary := b.String()
		if len(summary) > maxUpstreamChars {
			summary = strings.ToValidUTF8(summary[:maxUpstreamChars], "") + "… (truncated, see the full report)"
		}
		out = append(out, agents.Upstream{Key: dep.Key, Role: dep.Role, Summary: summary, ReportPath: path})
	}
	return out
}
