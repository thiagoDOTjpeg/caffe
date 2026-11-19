package persistence

import (
	"context"
	"time"
)

const (
	DecisionApprove = "approve"
	DecisionReject  = "reject"
	DecisionMerged  = "merged"
)

type Decision struct {
	ID        int64
	RunID     string
	TaskID    string
	Decision  string
	Note      string
	Author    string
	CreatedAt time.Time
}

func (s *Store) AddDecision(ctx context.Context, d *Decision) error {
	if d.CreatedAt.IsZero() {
		d.CreatedAt = Now()
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO integration_decisions (run_id, task_id, decision, note, author, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, d.RunID, d.TaskID, d.Decision, d.Note, d.Author, ts(d.CreatedAt))
	if err != nil {
		return err
	}
	d.ID, err = res.LastInsertId()
	return err
}

func (s *Store) ListDecisions(ctx context.Context, runID string) ([]*Decision, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, run_id, task_id, decision, note, author, created_at
		FROM integration_decisions WHERE run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Decision
	for rows.Next() {
		var d Decision
		var at string
		if err := rows.Scan(&d.ID, &d.RunID, &d.TaskID, &d.Decision, &d.Note, &d.Author, &at); err != nil {
			return nil, err
		}
		d.CreatedAt = parseTS(at)
		out = append(out, &d)
	}
	return out, rows.Err()
}
