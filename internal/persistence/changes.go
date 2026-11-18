package persistence

import (
	"context"
	"database/sql"
)

// ChangeDetector tells whether another connection committed since the last
// call. It owns one pinned connection: PRAGMA data_version is per connection,
// so reading it through the pool would compare unrelated values (see
// dataversion_test.go). Not safe for concurrent use.
type ChangeDetector struct {
	conn *sql.Conn
	last int64
	init bool
}

// ChangeDetector pins a connection of the store for change detection. Close it
// when done; it holds one of the pool's connections.
func (s *Store) ChangeDetector(ctx context.Context) (*ChangeDetector, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	return &ChangeDetector{conn: conn}, nil
}

// Changed reports whether another connection committed since the previous
// call (the first call only records the baseline and reports false), and the
// highest event Seq at that moment.
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
