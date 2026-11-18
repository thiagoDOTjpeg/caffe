// Package persistence stores orchestration state in SQLite.
package persistence

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// ErrConflict means a compare-and-swap status update lost: the row was not in
// the expected state. It is how duplicate dispatch is prevented.
var ErrConflict = errors.New("state conflict")

// ErrNotFound is returned when a lookup matches nothing.
var ErrNotFound = errors.New("not found")

type Store struct {
	db   *sql.DB
	path string
}

// Open opens (creating if needed) the database and applies pending migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Set("_txlock", "immediate") // take the write lock up front; no deadlock on upgrade
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	// One connection: writes are serialised in-process, other processes (a
	// `caffe status` next to a `caffe run`) are handled by WAL + busy_timeout.
	// Consequence: never run a query while iterating another one's rows.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	s := &Store{db: db, path: path}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	return s, nil
}

// OpenReadOnly opens an existing, already migrated database for consumers that
// only read (status views, Caffè Studio). It differs from Open in three ways:
//
//   - mode=ro: every write fails, so a reader cannot change state by mistake;
//   - _txlock=deferred: a read transaction takes no write lock. Open's pool uses
//     immediate transactions, so a "read transaction" there would block every
//     writer for its duration;
//   - several connections: readers do not serialise behind each other.
//
// It never creates the file or runs migrations; a schema older or newer than
// this binary is an error.
func OpenReadOnly(ctx context.Context, path string) (*Store, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("open %s read-only: %w", path, err)
	}
	q := url.Values{}
	q.Set("mode", "ro")
	q.Add("_pragma", "busy_timeout(10000)")
	q.Set("_txlock", "deferred")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	s := &Store{db: db, path: path}
	var have int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&have); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s read-only: %w", path, err)
	}
	if want, err := migrationCount(); err != nil {
		db.Close()
		return nil, err
	} else if have != want {
		db.Close()
		return nil, fmt.Errorf("%s has schema version %d, this binary expects %d; run any caffe command to migrate it", path, have, want)
	}
	return s, nil
}

// ReadTx runs fn in one read transaction: every query inside sees the same
// committed state, even while other processes write. On a store from
// OpenReadOnly it holds no write lock.
func (s *Store) ReadTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback() // read-only: nothing to commit
	return fn(tx)
}

func migrationCount() (int, error) {
	entries, err := migrations.ReadDir("migrations")
	return len(entries), err
}

func (s *Store) Close() error { return s.db.Close() }

// migrate applies embedded migrations in file-name order, tracking progress in
// PRAGMA user_version.
func (s *Store) migrate(ctx context.Context) error {
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	for i, e := range entries {
		if i < version {
			continue
		}
		stmts, err := migrations.ReadFile("migrations/" + e.Name())
		if err != nil {
			return err
		}
		err = s.tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, string(stmts)); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1))
			return err
		})
		if err != nil {
			return fmt.Errorf("%s: %w", e.Name(), err)
		}
	}
	return nil
}

// Backup writes a consistent copy of the database into dir and keeps the
// newest `keep` copies.
func (s *Store) Backup(ctx context.Context, dir string, keep int) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, "caffe-"+time.Now().UTC().Format("20060102T150405.000")+".db")
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
		return "", err
	}
	old, err := filepath.Glob(filepath.Join(dir, "caffe-*.db"))
	if err != nil {
		return dst, err
	}
	sort.Strings(old)
	for i := 0; i < len(old)-keep; i++ {
		if err := os.Remove(old[i]); err != nil {
			return dst, err
		}
	}
	return dst, nil
}

func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	return tx.Commit()
}

// --- column helpers. Timestamps are RFC 3339 UTC text. ---

const tsLayout = "2006-01-02T15:04:05.000000000Z"

func ts(t time.Time) string { return t.UTC().Format(tsLayout) }

func tsPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ts(*t)
}

func parseTS(s string) time.Time {
	t, _ := time.Parse(tsLayout, s) // written only by ts(); a zero time flags corruption visibly
	return t
}

func parseTSPtr(s sql.NullString) *time.Time {
	if !s.Valid {
		return nil
	}
	t := parseTS(s.String)
	return &t
}

func toJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}" // only maps and string slices are passed; cannot fail
	}
	return string(b)
}

// Now is the single clock for persisted timestamps.
func Now() time.Time { return time.Now().UTC() }
