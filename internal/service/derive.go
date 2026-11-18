package service

import (
	"fmt"
	"strings"

	c "caffe/internal/contracts"
	"caffe/internal/core"
	"caffe/internal/runtime"
)

// VisualState is how an agent is presented. It is derived, separately from
// task state, and never decides anything: completion comes from the database.
type VisualState string

const (
	VisualIdle        VisualState = "idle"
	VisualStarting    VisualState = "starting"
	VisualWorking     VisualState = "working"
	VisualWaiting     VisualState = "waiting" // idle in the runtime, report not delivered yet
	VisualBlocked     VisualState = "blocked"
	VisualLost        VisualState = "lost"        // execution running, runtime says the agent is gone
	VisualInterrupted VisualState = "interrupted" // the orchestrator process died during the execution
	VisualStopped     VisualState = "stopped"     // agent ended with no execution of its own (planning agents)
	VisualCompleted   VisualState = "completed"
	VisualFailed      VisualState = "failed"
	VisualCancelled   VisualState = "cancelled"
)

// RuntimeObservation is what the runtime last said about an agent. The zero
// value means "not asked" (stored state only).
type RuntimeObservation struct {
	Known  bool // the runtime answered
	Found  bool // ...and the agent exists there
	Status runtime.AgentStatus
	Err    error // the runtime could not be asked (Known is false)
}

// DeriveVisualState applies the rules of docs/studio/ux-specification.md §4.1,
// first match wins. exec is the agent's latest execution, nil if it has none.
func DeriveVisualState(agentStatus string, exec *c.Execution, rt RuntimeObservation) VisualState {
	if exec != nil {
		switch exec.Status {
		case c.ExecCancelled:
			return VisualCancelled
		case c.ExecFailed, c.ExecUnknown:
			return VisualFailed
		case c.ExecCompleted:
			return VisualCompleted
		}
	}
	running := exec != nil && (exec.Status == c.ExecRunning || exec.Status == c.ExecInterrupted)
	if running && rt.Known && !rt.Found {
		return VisualLost
	}
	if rt.Known && rt.Found {
		switch rt.Status {
		case runtime.StatusBlocked:
			return VisualBlocked
		case runtime.StatusWorking:
			return VisualWorking
		case runtime.StatusIdle, runtime.StatusDone:
			if running {
				return VisualWaiting
			}
		}
	}
	switch {
	case exec != nil && exec.Status == c.ExecInterrupted:
		return VisualInterrupted
	case agentStatus == "starting" || running:
		return VisualStarting
	case exec == nil && agentStatus == "stopped":
		return VisualStopped
	}
	return VisualIdle
}

// TaskReason explains why a task is in its state, in one line: what a pending
// task waits on, that a ready task is queued, why a task failed. Empty when
// there is nothing to explain.
func TaskReason(t *c.Task, g *core.Graph, maxConcurrent int, runStatus c.RunStatus) string {
	reason := t.Error
	switch t.Status {
	case c.TaskPending:
		var waiting []string
		for _, d := range g.Waiting(t) {
			waiting = append(waiting, fmt.Sprintf("%s (%s)", d.Key, d.Status))
		}
		reason = "waiting on " + strings.Join(waiting, ", ")
	case c.TaskReady:
		reason = fmt.Sprintf("queued: at most %d agents run at once", maxConcurrent)
	case c.TaskBlocked:
		reason = "agent is waiting for human input"
	case c.TaskCompleted:
		reason = ""
	}
	if runStatus == c.RunInterrupted && !core.Finished(t.Status) {
		reason = strings.TrimSuffix("run interrupted; "+reason, "; ")
	}
	return reason
}
