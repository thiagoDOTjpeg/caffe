package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	c "caffe/internal/contracts"
)

// RunSnapshot is the persisted state of one run, read in a single
// transaction. Seq is the highest event sequence number at that moment:
// every event with Seq <= RunSnapshot.Seq is already reflected in it, so an
// incremental reader continues with EventsSince(snapshot.Seq).
type RunSnapshot struct {
	Run        *c.Run
	Tasks      []*c.Task // plan order, with Dependencies
	Agents     []*c.Agent
	Executions []*c.Execution
	Artifacts  []*c.Artifact
	Seq        int64
}

// GetExecution returns one execution and the run it belongs to.
func (s *Store) GetExecution(ctx context.Context, id string) (*c.Execution, string, error) {
	var runID string
	err := s.db.QueryRowContext(ctx, `SELECT t.run_id FROM executions e JOIN tasks t ON t.id = e.task_id WHERE e.id = ?`, id).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", fmt.Errorf("execution %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, "", err
	}
	execs, err := listExecutions(ctx, s.db, runID)
	if err != nil {
		return nil, "", err
	}
	for _, e := range execs {
		if e.ID == id {
			return e, runID, nil
		}
	}
	return nil, "", fmt.Errorf("execution %q: %w", id, ErrNotFound)
}

// GetTask returns one task (with its dependencies) by id.
func (s *Store) GetTask(ctx context.Context, id string) (*c.Task, error) {
	var runID string
	err := s.db.QueryRowContext(ctx, `SELECT run_id FROM tasks WHERE id = ?`, id).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("task %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	tasks, err := listTasks(ctx, s.db, runID)
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, fmt.Errorf("task %q: %w", id, ErrNotFound)
}

// GetArtifact returns one artifact record.
func (s *Store) GetArtifact(ctx context.Context, id string) (*c.Artifact, error) {
	var runID string
	err := s.db.QueryRowContext(ctx, `SELECT run_id FROM artifacts WHERE id = ?`, id).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("artifact %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	arts, err := listArtifacts(ctx, s.db, runID)
	if err != nil {
		return nil, err
	}
	for _, a := range arts {
		if a.ID == id {
			return a, nil
		}
	}
	return nil, fmt.Errorf("artifact %q: %w", id, ErrNotFound)
}

// RunSnapshot reads a run and everything that belongs to it in one read
// transaction, so the parts are consistent with each other even while
// another process is writing. Use a store from OpenReadOnly: on a store from
// Open the transaction takes the write lock (see OpenReadOnly).
func (s *Store) RunSnapshot(ctx context.Context, runID string) (*RunSnapshot, error) {
	snap := &RunSnapshot{}
	err := s.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		if snap.Run, err = getRun(ctx, tx, runID); err != nil {
			return err
		}
		id := snap.Run.ID // runID may have been a prefix
		if snap.Tasks, err = listTasks(ctx, tx, id); err != nil {
			return err
		}
		if snap.Agents, err = listAgents(ctx, tx, id); err != nil {
			return err
		}
		if snap.Executions, err = listExecutions(ctx, tx, id); err != nil {
			return err
		}
		if snap.Artifacts, err = listArtifacts(ctx, tx, id); err != nil {
			return err
		}
		// Global, not per run: the cursor is shared by all readers.
		return tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(rowid), 0) FROM events`).Scan(&snap.Seq)
	})
	if err != nil {
		return nil, err
	}
	return snap, nil
}
