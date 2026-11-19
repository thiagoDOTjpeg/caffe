package persistence

import (
	"context"
	"database/sql"
	"testing"

	c "caffe/internal/contracts"
)

func dataVersion(t testing.TB, conn *sql.Conn) int64 {
	t.Helper()
	var v int64
	if err := conn.QueryRowContext(context.Background(), "PRAGMA data_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDataVersionDetectsEveryCommitFromAnotherConnection(t *testing.T) {
	ctx := context.Background()
	w, path := open(t)
	seed(t, w)
	r, err := OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	conn, err := r.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	prev := dataVersion(t, conn)
	if again := dataVersion(t, conn); again != prev {
		t.Fatalf("data_version changed without any commit: %d → %d", prev, again)
	}
	for i := 0; i < 50; i++ {
		if err := w.AddEvent(ctx, &c.Event{RunID: "run-1", Type: "tick"}); err != nil {
			t.Fatal(err)
		}
		v := dataVersion(t, conn)
		if v == prev {
			t.Fatalf("commit %d not detected (data_version stayed %d)", i, v)
		}
		prev = v
	}

	if _, err := r.ListRuns(ctx, false); err != nil {
		t.Fatal(err)
	}
	if v := dataVersion(t, conn); v != prev {
		t.Fatalf("a read changed data_version: %d → %d", prev, v)
	}

	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO events (id, run_id, type, created_at) VALUES ('x', 'run-1', 't', '2026')`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if v := dataVersion(t, conn); v != prev {
		t.Fatalf("a rollback changed data_version: %d → %d", prev, v)
	}
}

func BenchmarkDataVersion(b *testing.B) {
	ctx := context.Background()
	path := b.TempDir() + "/bench.db"
	w, err := Open(ctx, path)
	if err != nil {
		b.Fatal(err)
	}
	defer w.Close()
	r, err := OpenReadOnly(ctx, path)
	if err != nil {
		b.Fatal(err)
	}
	defer r.Close()
	conn, err := r.db.Conn(ctx)
	if err != nil {
		b.Fatal(err)
	}
	defer conn.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dataVersion(b, conn)
	}
}
