// Package core owns orchestration: the task graph, the scheduler, task
// execution policies and recovery.
package core

import (
	"context"
	"fmt"

	c "caffe/internal/contracts"
	"caffe/internal/persistence"
)

// taskTransitions is the complete task lifecycle. Anything not listed is
// rejected.
//
//	pending -> ready -> assigned -> running -> completed
//
// assigned: claimed by a scheduler worker. running: the agent has the prompt
// (or validation commands started). blocked: the agent waits for a human.
// reviewing: a completed developer task being corrected after a rejected review.
var taskTransitions = map[c.TaskStatus][]c.TaskStatus{
	c.TaskPending:   {c.TaskReady, c.TaskCancelled},
	c.TaskReady:     {c.TaskAssigned, c.TaskCancelled},
	c.TaskAssigned:  {c.TaskRunning, c.TaskFailed, c.TaskCancelled},
	c.TaskRunning:   {c.TaskCompleted, c.TaskBlocked, c.TaskFailed, c.TaskCancelled},
	c.TaskBlocked:   {c.TaskRunning, c.TaskReady, c.TaskFailed, c.TaskCancelled},
	c.TaskFailed:    {c.TaskReady}, // retry
	c.TaskCompleted: {c.TaskReviewing},
	c.TaskReviewing: {c.TaskCompleted, c.TaskFailed, c.TaskCancelled},
	c.TaskCancelled: {},
}

// CanTransition reports whether a task may move from one status to another.
func CanTransition(from, to c.TaskStatus) bool {
	for _, t := range taskTransitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

// Finished reports whether a task will not change any more without an explicit
// retry. A failed task is finished once the scheduler decided not to retry it.
func Finished(s c.TaskStatus) bool {
	return s == c.TaskCompleted || s == c.TaskFailed || s == c.TaskCancelled
}

// transition validates and persists one task state change and returns the
// updated task. The input is not modified, so a failed write leaves the
// caller's view consistent with the database.
func transition(ctx context.Context, store *persistence.Store, t c.Task, to c.TaskStatus, reason string, mut func(*c.Task)) (c.Task, error) {
	from := t.Status
	if from != to && !CanTransition(from, to) {
		return t, fmt.Errorf("task %s: invalid transition %s -> %s", t.Key, from, to)
	}
	t.Status = to
	now := persistence.Now()
	switch to {
	case c.TaskRunning:
		if t.StartedAt == nil {
			t.StartedAt = &now
		}
	case c.TaskCompleted, c.TaskFailed, c.TaskCancelled:
		t.CompletedAt = &now
	case c.TaskReady, c.TaskReviewing:
		t.CompletedAt = nil
	}
	if to == c.TaskFailed || to == c.TaskCancelled {
		t.Error = reason
	}
	if mut != nil {
		mut(&t)
	}
	if err := store.UpdateTask(ctx, &t, from, reason); err != nil {
		return t, err
	}
	return t, nil
}
