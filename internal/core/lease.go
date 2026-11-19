package core

import (
	"context"
	"errors"
	"time"

	c "caffe/internal/contracts"
	"caffe/internal/persistence"
)

// HumanLeaseTTL is how long a human lease (Studio editing a worktree)
// survives without a heartbeat (architecture §9.2).
const HumanLeaseTTL = 15 * time.Second

// leaseBlockedReason is why a task waits for a worktree a human holds.
const leaseBlockedReason = "worktree held by a manual edit session"

// withLease runs fn while holding the agent lease of dev's worktree wt, so a
// human editing it in Studio and an agent never write at the same time and
// no agent commit sweeps up manual edits. Every agent of the run that works
// on dev's worktree is the same holder: the engine never waits for itself.
// While a human holds the lease, tracked (the task the scheduler
// dispatched) is blocked and the lease is retried every poll interval.
func (s *session) withLease(ctx context.Context, tracked, dev *c.Task, wt string, fn func() error) error {
	l := &persistence.Lease{Path: wt, HolderKind: persistence.LeaseAgent, HolderID: s.run.ID + "/" + dev.ID, RunID: s.run.ID, TaskID: dev.ID}
	if err := s.acquireLease(ctx, tracked, l); err != nil {
		return err
	}
	defer func() {
		if err := s.Store.ReleaseLease(context.WithoutCancel(ctx), l); err != nil {
			s.Log.Error().Err(err).Str("worktree", wt).Msg("could not release the worktree lease")
		}
	}()
	return fn()
}

// leaseHook, if set (tests only), runs before each lease acquisition.
var leaseHook func(tracked *c.Task, path string)

func (s *session) acquireLease(ctx context.Context, tracked *c.Task, l *persistence.Lease) error {
	db := context.WithoutCancel(ctx)
	if leaseHook != nil {
		leaseHook(tracked, l.Path)
	}
	blocked := false
	for {
		err := s.Store.AcquireLease(db, l)
		if err == nil {
			if blocked {
				return s.sched.Transition(db, tracked, c.TaskRunning, "worktree lease acquired", nil)
			}
			return nil
		}
		if !errors.Is(err, persistence.ErrLeaseHeld) {
			return err
		}
		cur, err := s.Store.GetLease(db, l.Path)
		if errors.Is(err, persistence.ErrNotFound) {
			continue // released in between
		}
		if err != nil {
			return err
		}
		// An abandoned human lease (Studio gone) is taken over only if it left
		// nothing behind; a dirty worktree keeps waiting for the human.
		if cur.HolderKind == persistence.LeaseHuman && cur.Expired(persistence.Now(), HumanLeaseTTL) {
			if dirty, derr := s.Git.IsDirty(db, l.Path); derr == nil && !dirty {
				if _, err := s.Store.ExpireLease(db, l.Path, persistence.Now().Add(-HumanLeaseTTL)); err != nil {
					return err
				}
				continue
			}
		}
		if !blocked {
			// A first developer execution is still assigned: running first, so
			// the state machine needs no assigned -> blocked edge.
			if s.sched.Snapshot(tracked).Status == c.TaskAssigned {
				if err := s.sched.Transition(db, tracked, c.TaskRunning, "waiting for the worktree", nil); err != nil {
					return err
				}
			}
			if err := s.sched.Transition(db, tracked, c.TaskBlocked, leaseBlockedReason, nil); err != nil {
				return err
			}
			s.say("%-16s waiting: %s", tracked.Key, leaseBlockedReason)
			blocked = true
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.Cfg.Runtime.PollInterval):
		}
	}
}
