package persistence

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const (
	LeaseAgent = "agent"
	LeaseHuman = "human"
)

var ErrLeaseHeld = errors.New("worktree lease held by another holder")

type Lease struct {
	Path        string
	HolderKind  string
	HolderID    string
	RunID       string
	TaskID      string
	AcquiredAt  time.Time
	HeartbeatAt time.Time
}

func (l *Lease) Expired(now time.Time, ttl time.Duration) bool {
	return now.Sub(l.HeartbeatAt) > ttl
}

func (s *Store) AcquireLease(ctx context.Context, l *Lease) error {
	now := Now()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO worktree_leases (path, holder_kind, holder_id, run_id, task_id, acquired_at, heartbeat_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET heartbeat_at = excluded.heartbeat_at
		WHERE holder_kind = excluded.holder_kind AND holder_id = excluded.holder_id`,
		l.Path, l.HolderKind, l.HolderID, l.RunID, l.TaskID, ts(now), ts(now))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrLeaseHeld
	}
	l.HeartbeatAt = now
	if l.AcquiredAt.IsZero() {
		l.AcquiredAt = now
	}
	return nil
}

func (s *Store) RenewLease(ctx context.Context, l *Lease) error {
	now := Now()
	res, err := s.db.ExecContext(ctx, `UPDATE worktree_leases SET heartbeat_at = ?
		WHERE path = ? AND holder_kind = ? AND holder_id = ?`, ts(now), l.Path, l.HolderKind, l.HolderID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrLeaseHeld
	}
	l.HeartbeatAt = now
	return nil
}

func (s *Store) ReleaseLease(ctx context.Context, l *Lease) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM worktree_leases
		WHERE path = ? AND holder_kind = ? AND holder_id = ?`, l.Path, l.HolderKind, l.HolderID)
	return err
}

func (s *Store) ExpireLease(ctx context.Context, path string, before time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM worktree_leases WHERE path = ? AND heartbeat_at < ?`, path, ts(before))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) ReleaseAgentLeases(ctx context.Context, runID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM worktree_leases WHERE holder_kind = ? AND run_id = ?`, LeaseAgent, runID)
	return err
}

func (s *Store) GetLease(ctx context.Context, path string) (*Lease, error) {
	l, err := scanLease(s.db.QueryRowContext(ctx, `SELECT `+leaseCols+` FROM worktree_leases WHERE path = ?`, path))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return l, err
}

func (s *Store) ListLeases(ctx context.Context) ([]*Lease, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+leaseCols+` FROM worktree_leases ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Lease
	for rows.Next() {
		l, err := scanLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

const leaseCols = `path, holder_kind, holder_id, run_id, task_id, acquired_at, heartbeat_at`

func scanLease(row interface{ Scan(...any) error }) (*Lease, error) {
	var l Lease
	var acq, hb string
	if err := row.Scan(&l.Path, &l.HolderKind, &l.HolderID, &l.RunID, &l.TaskID, &acq, &hb); err != nil {
		return nil, err
	}
	l.AcquiredAt, l.HeartbeatAt = parseTS(acq), parseTS(hb)
	return &l, nil
}
