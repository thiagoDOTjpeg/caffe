package service

import (
	"fmt"
	"strings"

	c "caffe/internal/contracts"
	"caffe/internal/core"
	"caffe/internal/runtime"
)

type VisualState string

const (
	VisualIdle        VisualState = "idle"
	VisualStarting    VisualState = "starting"
	VisualWorking     VisualState = "working"
	VisualWaiting     VisualState = "waiting"
	VisualBlocked     VisualState = "blocked"
	VisualLost        VisualState = "lost"
	VisualInterrupted VisualState = "interrupted"
	VisualStopped     VisualState = "stopped"
	VisualCompleted   VisualState = "completed"
	VisualFailed      VisualState = "failed"
	VisualCancelled   VisualState = "cancelled"
)

type RuntimeObservation struct {
	Known  bool
	Found  bool
	Status runtime.AgentStatus
	Err    error
}

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
