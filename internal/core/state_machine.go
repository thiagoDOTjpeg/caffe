package core

import (
	"context"
	"fmt"

	c "caffe/internal/contracts"
	"caffe/internal/persistence"
)

var taskTransitions = map[c.TaskStatus][]c.TaskStatus{
	c.TaskPending:   {c.TaskReady, c.TaskCancelled},
	c.TaskReady:     {c.TaskAssigned, c.TaskCancelled},
	c.TaskAssigned:  {c.TaskRunning, c.TaskFailed, c.TaskCancelled},
	c.TaskRunning:   {c.TaskCompleted, c.TaskBlocked, c.TaskFailed, c.TaskCancelled},
	c.TaskBlocked:   {c.TaskRunning, c.TaskReady, c.TaskFailed, c.TaskCancelled},
	c.TaskFailed:    {c.TaskReady},
	c.TaskCompleted: {c.TaskReviewing},
	c.TaskReviewing: {c.TaskCompleted, c.TaskFailed, c.TaskCancelled},
	c.TaskCancelled: {},
}

func CanTransition(from, to c.TaskStatus) bool {
	for _, t := range taskTransitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

func Finished(s c.TaskStatus) bool {
	return s == c.TaskCompleted || s == c.TaskFailed || s == c.TaskCancelled
}

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
