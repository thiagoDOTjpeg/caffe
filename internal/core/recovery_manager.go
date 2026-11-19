package core

import (
	"context"
	"errors"
	"fmt"

	c "caffe/internal/contracts"
	"caffe/internal/persistence"
	"caffe/internal/runtime"
)

// Recover is run at startup. It only establishes facts: runs whose owning
// process stopped heartbeating are flagged interrupted. No task is restarted
// here; that takes an explicit Resume.
func (o *Orchestrator) Recover(ctx context.Context) ([]string, error) {
	ids, err := o.Store.MarkStaleRunsInterrupted(ctx, StaleAfter)
	for _, id := range ids {
		// Its agents are gone: their worktree leases must not lock humans out.
		err = errors.Join(err, o.Store.ReleaseAgentLeases(ctx, id))
		o.Log.Warn().Str("run_id", id).Str("event_type", "run.interrupted").Msg("run was interrupted; inspect it and use `caffe resume` to continue")
	}
	return ids, err
}

// Resume continues an interrupted run. Nothing is re-run blindly:
//
//   - finished tasks are left alone;
//   - a task that was in flight is re-queued, and when it is dispatched its
//     interrupted execution is re-attached if the agent is still alive or its
//     report is on disk (see session.reattach). Only if neither holds is a new
//     execution started: read-only roles are safe to repeat, and a developer
//     continues in its own preserved worktree and is told to inspect it first;
//   - interrupted executions that nothing will re-attach to (corrections,
//     superseded attempts) are recorded as unknown and their agents stopped,
//     so no orphan keeps writing to a worktree.
func (o *Orchestrator) Resume(ctx context.Context, idOrPrefix string) (*c.Run, error) {
	run, err := o.Store.GetRun(ctx, idOrPrefix)
	if err != nil {
		return nil, err
	}
	if run.Status != c.RunInterrupted {
		return nil, fmt.Errorf("run %s is %s; only interrupted runs can be resumed", run.ID, run.Status)
	}
	// Claim the run. The compare-and-swap makes sure only one process resumes it.
	now := persistence.Now()
	run.Status, run.HeartbeatAt = c.RunExecuting, &now
	if err := o.Store.UpdateRun(ctx, run, c.RunInterrupted); err != nil {
		return nil, fmt.Errorf("claim run: %w", err)
	}
	o.say("resuming run %s", run.ID)

	tasks, err := o.Store.ListTasks(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	resumable, err := o.reconcile(ctx, run, tasks)
	if err != nil {
		// Leave the run resumable rather than half-reconciled and "executing".
		run.Status = c.RunInterrupted
		return nil, errors.Join(fmt.Errorf("reconcile run: %w", err), o.Store.UpdateRun(context.WithoutCancel(ctx), run, c.RunExecuting))
	}
	if len(tasks) == 0 {
		o.say("the run was interrupted while planning; planning again (planning is read-only)")
	}
	return o.drive(ctx, run, nil, tasks, resumable)
}

// reconcile brings stored task state in line with reality and returns, per
// task, the interrupted execution worth re-attaching to.
func (o *Orchestrator) reconcile(ctx context.Context, run *c.Run, tasks []*c.Task) (map[string]*c.Execution, error) {
	execs, err := o.Store.ListExecutions(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	// Latest interrupted execution per task; older ones are orphans.
	latest := map[string]*c.Execution{}
	var orphans []*c.Execution
	for _, e := range execs {
		if e.Status != c.ExecInterrupted {
			continue
		}
		if prev := latest[e.TaskID]; prev != nil {
			orphans = append(orphans, prev)
		}
		latest[e.TaskID] = e
	}

	resumable := map[string]*c.Execution{}
	for _, t := range tasks {
		switch t.Status {
		case c.TaskAssigned, c.TaskRunning, c.TaskBlocked:
			if e := latest[t.ID]; e != nil {
				resumable[t.ID] = e
				delete(latest, t.ID)
			}
			was := t.Status
			failed, err := transition(ctx, o.Store, *t, c.TaskFailed, "orchestrator was interrupted", nil)
			if err != nil {
				return nil, err
			}
			// An interruption is not a failed attempt: give the attempt back.
			ready, err := transition(ctx, o.Store, failed, c.TaskReady, "resumed", func(t *c.Task) {
				if t.Attempts > 0 {
					t.Attempts--
				}
				t.Error = ""
			})
			if err != nil {
				return nil, err
			}
			*t = ready
			o.say("%-16s was %s when interrupted → ready", t.Key, was)
		case c.TaskReviewing:
			// A correction was cut short. Its agent is stopped below; the
			// reviewer task re-reviews whatever is in the worktree.
			done, err := transition(ctx, o.Store, *t, c.TaskCompleted, "correction interrupted; will be re-reviewed", nil)
			if err != nil {
				return nil, err
			}
			*t = done
		}
	}
	for _, e := range latest {
		orphans = append(orphans, e)
	}
	now := persistence.Now()
	for _, e := range orphans {
		if e.AgentID != "" {
			if a, err := o.Store.GetAgent(ctx, e.AgentID); err == nil && a.WorkspaceID != "" {
				// Check by name first: the workspace id may belong to someone else by now.
				if _, err := o.RT.GetAgentStatus(ctx, a.Name); err == nil {
					if err := o.RT.StopAgent(ctx, a.WorkspaceID); err != nil {
						return nil, fmt.Errorf("stop orphaned agent %s: %w", a.Name, err)
					}
				} else if !errors.Is(err, runtime.ErrNotFound) {
					return nil, fmt.Errorf("orphaned agent %s: %w", a.Name, err)
				}
				a.Status, a.CurrentTaskID = "stopped", ""
				if err := o.Store.SaveAgent(ctx, a); err != nil {
					return nil, err
				}
			}
		}
		e.Status, e.CompletedAt, e.Error = c.ExecUnknown, &now, "interrupted; outcome unknown, agent stopped on resume"
		if err := o.Store.SaveExecution(ctx, e); err != nil {
			return nil, err
		}
		o.event(c.Event{RunID: run.ID, TaskID: e.TaskID, AgentID: e.AgentID, Type: c.ExecutionEvent(c.ExecUnknown),
			Payload: map[string]any{"execution_id": e.ID, "reason": e.Error}})
	}
	if len(tasks) == 0 {
		// Interrupted while planning: a planning agent may still be around.
		if err := o.stopAgents(ctx, run.ID); err != nil {
			return nil, err
		}
	}
	return resumable, nil
}
