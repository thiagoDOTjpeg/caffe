package persistence

import (
	"context"
	"database/sql"
)

type ChangeDetector struct {
	conn *sql.Conn
	last int64
	init bool
}

func (s *Store) ChangeDetector(ctx context.Context) (*ChangeDetector, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	return &ChangeDetector{conn: conn}, nil
}

func (d *ChangeDetector) Changed(ctx context.Context) (changed bool, seq int64, err error) {
	var v int64
	if err := d.conn.QueryRowContext(ctx, "PRAGMA data_version").Scan(&v); err != nil {
		return false, 0, err
	}
	changed = d.init && v != d.last
	d.last, d.init = v, true
	if err := d.conn.QueryRowContext(ctx, `SELECT COALESCE(MAX(rowid), 0) FROM events`).Scan(&seq); err != nil {
		return false, 0, err
	}
	return changed, seq, nil
}

func (d *ChangeDetector) Close() error { return d.conn.Close() }
