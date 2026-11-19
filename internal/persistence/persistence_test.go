package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	c "caffe/internal/contracts"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "x.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func seed(t *testing.T, s *Store) (*c.Run, []*c.Task) {
	t.Helper()
	ctx := context.Background()
	r := &c.Run{ID: "run-1", ProjectPath: "/p", UserRequest: "do it", Status: c.RunPending, CreatedAt: Now(), Metadata: map[string]string{"k": "v"}}
	if err := s.CreateRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	a := &c.Task{ID: "a", RunID: r.ID, Key: "a", Title: "A", Description: "d", Role: c.RoleResearcher, Status: c.TaskPending,
		AcceptanceCriteria: []string{"x"}, MaxAttempts: 2, CreatedAt: Now()}
	b := &c.Task{ID: "b", RunID: r.ID, Key: "b", Title: "B", Description: "d", Role: c.RoleDeveloper, Status: c.TaskPending,
		AcceptanceCriteria: []string{"y"}, MaxAttempts: 2, CreatedAt: Now(), Dependencies: []string{"a"}}
	if err := s.CreateTasks(ctx, []*c.Task{a, b}); err != nil {
		t.Fatal(err)
	}
	return r, []*c.Task{a, b}
}

func TestRoundTripAndReopen(t *testing.T) {
	ctx := context.Background()
	s, path := open(t)
	r, _ := seed(t, s)

	exit := 3
	if err := s.SaveExecution(ctx, &c.Execution{ID: "e1", TaskID: "b", Attempt: 1, Status: c.ExecFailed, StartedAt: Now(), ExitCode: &exit}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAgent(ctx, &c.Agent{ID: "ag", RunID: r.ID, Name: "n", Kind: "claude", Role: c.RoleDeveloper, Status: "idle"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddArtifact(ctx, &c.Artifact{RunID: r.ID, TaskID: "b", Type: c.ArtifactDiff, Path: "/x.diff"}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s2, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got, err := s2.GetRun(ctx, "run")
	if err != nil || got.UserRequest != "do it" || got.Metadata["k"] != "v" {
		t.Fatalf("run = %+v, %v", got, err)
	}
	tasks, err := s2.ListTasks(ctx, r.ID)
	if err != nil || len(tasks) != 2 || tasks[1].Key != "b" || len(tasks[1].Dependencies) != 1 || tasks[1].Dependencies[0] != "a" {
		t.Fatalf("tasks = %+v, %v", tasks, err)
	}
	execs, err := s2.ListExecutions(ctx, r.ID)
	if err != nil || len(execs) != 1 || execs[0].ExitCode == nil || *execs[0].ExitCode != 3 {
		t.Fatalf("executions = %+v, %v", execs, err)
	}
	if arts, err := s2.ListArtifacts(ctx, r.ID); err != nil || len(arts) != 1 {
		t.Fatalf("artifacts = %v, %v", arts, err)
	}
	if ags, err := s2.ListAgents(ctx, r.ID); err != nil || len(ags) != 1 {
		t.Fatalf("agents = %v, %v", ags, err)
	}
	if _, err := s2.GetRun(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestUpdateTaskIsCompareAndSwap(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	_, tasks := seed(t, s)
	a := *tasks[0]
	a.Status = c.TaskReady
	if err := s.UpdateTask(ctx, &a, c.TaskPending, ""); err != nil {
		t.Fatal(err)
	}

	stale := *tasks[0]
	stale.Status = c.TaskReady
	if err := s.UpdateTask(ctx, &stale, c.TaskPending, ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
	evs, err := s.ListEvents(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range evs {
		if e.Type == "task.ready" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want exactly one task.ready event, got %d", n)
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	s, _ := open(t)
	err := s.SaveExecution(context.Background(), &c.Execution{ID: "e", TaskID: "missing", Attempt: 1, Status: c.ExecRunning, StartedAt: Now()})
	if err == nil {
		t.Fatal("execution for a missing task must be rejected")
	}
}

func TestStaleRunsBecomeInterrupted(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	r, _ := seed(t, s)
	r.Status = c.RunExecuting
	if err := s.UpdateRun(ctx, r, c.RunPending); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveExecution(ctx, &c.Execution{ID: "e1", TaskID: "a", Attempt: 1, Status: c.ExecRunning, StartedAt: Now()}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Heartbeat(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if ids, err := s.MarkStaleRunsInterrupted(ctx, time.Minute); err != nil || len(ids) != 0 {
		t.Fatalf("live run flagged: %v, %v", ids, err)
	}
	time.Sleep(5 * time.Millisecond)
	ids, err := s.MarkStaleRunsInterrupted(ctx, time.Millisecond)
	if err != nil || len(ids) != 1 {
		t.Fatalf("stale run not flagged: %v, %v", ids, err)
	}
	got, _ := s.GetRun(ctx, r.ID)
	execs, _ := s.ListExecutions(ctx, r.ID)
	if got.Status != c.RunInterrupted || execs[0].Status != c.ExecInterrupted {
		t.Fatalf("run=%s exec=%s", got.Status, execs[0].Status)
	}
}

func TestEventsSinceIsAGaplessCursorAcrossWriters(t *testing.T) {
	ctx := context.Background()
	s1, path := open(t)
	seed(t, s1)
	if err := s1.CreateRun(ctx, &c.Run{ID: "run-2", ProjectPath: "/p", UserRequest: "r", Status: c.RunPending, CreatedAt: Now()}); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	const perWriter = 60
	errs := make(chan error, 2)
	for i, s := range []*Store{s1, s2} {
		go func(s *Store, run string) {
			for n := 0; n < perWriter; n++ {
				e := &c.Event{RunID: run, Type: "test.tick", Payload: map[string]any{"n": n}}
				if err := s.AddEvent(ctx, e); err != nil {
					errs <- err
					return
				}
				if e.Seq <= 0 {
					errs <- fmt.Errorf("AddEvent did not set Seq")
					return
				}
			}
			errs <- nil
		}(s, []string{"run-1", "run-2"}[i])
	}

	seen := map[string]bool{}
	var last int64
	deadline := time.Now().Add(10 * time.Second)
	writersDone := 0
	for {
		page, err := s1.EventsSince(ctx, last, "", 7)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range page {
			if e.Seq <= last {
				t.Fatalf("seq went backwards: %d after %d", e.Seq, last)
			}
			if seen[e.ID] {
				t.Fatalf("event %s delivered twice", e.ID)
			}
			seen[e.ID] = true
			last = e.Seq
		}
		select {
		case err := <-errs:
			if err != nil {
				t.Fatal(err)
			}
			writersDone++
		default:
		}
		if writersDone == 2 && len(page) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
	}
	all, err := s1.EventsSince(ctx, 0, "", 10_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != len(all) {
		t.Fatalf("cursor saw %d events, log has %d", len(seen), len(all))
	}

	run2, err := s1.EventsSince(ctx, 0, "run-2", 10_000)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range run2 {
		if e.RunID != "run-2" {
			t.Fatalf("filter leaked %s", e.RunID)
		}
	}
	if len(run2) != perWriter+1 {
		t.Fatalf("run-2 has %d events, want %d", len(run2), perWriter+1)
	}
	listed, _ := s1.ListEvents(ctx, "run-2")
	if len(listed) != len(run2) || listed[0].Seq != run2[0].Seq {
		t.Fatalf("ListEvents Seq disagrees with EventsSince")
	}
	if _, err := s1.EventsSince(ctx, 0, "", 0); err == nil {
		t.Fatal("limit 0 accepted")
	}
}

func TestReadOnlyStore(t *testing.T) {
	ctx := context.Background()
	w, path := open(t)
	seed(t, w)
	r, err := OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if got, err := r.GetRun(ctx, "run-1"); err != nil || got.UserRequest != "do it" {
		t.Fatalf("read: %v, %v", got, err)
	}
	if err := r.AddEvent(ctx, &c.Event{RunID: "run-1", Type: "x"}); err == nil {
		t.Fatal("write through the read-only store succeeded")
	}

	var before, inside, after int
	count := func(q interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	}) int {
		var n int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM events`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	err = r.ReadTx(ctx, func(tx *sql.Tx) error {
		before = count(tx)
		done := make(chan error, 1)
		go func() { done <- w.AddEvent(ctx, &c.Event{RunID: "run-1", Type: "during"}) }()
		select {
		case err := <-done:
			if err != nil {
				return err
			}
		case <-time.After(3 * time.Second):
			return errors.New("writer blocked by a read transaction")
		}
		inside = count(tx)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	after = count(r.db)
	if inside != before || after != before+1 {
		t.Fatalf("snapshot not stable: before=%d inside=%d after=%d", before, inside, after)
	}

	w.Close()
	if _, err := r.GetRun(ctx, "run-1"); err != nil {
		t.Fatalf("read after writer closed: %v", err)
	}
	r2, err := OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatalf("open read-only with no writer: %v", err)
	}
	r2.Close()
}

func TestRunSnapshotIsConsistentUnderConcurrentWrites(t *testing.T) {
	ctx := context.Background()
	w, path := open(t)
	seed(t, w)
	r, err := OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	const n = 150
	done := make(chan error, 1)
	go func() {
		for i := 0; i < n; i++ {
			ex := &c.Execution{ID: fmt.Sprintf("e-%03d", i), TaskID: "a", Attempt: 1, Status: c.ExecRunning, StartedAt: Now()}
			if err := w.SaveExecution(ctx, ex); err != nil {
				done <- err
				return
			}
			if err := w.AddEvent(ctx, &c.Event{RunID: "run-1", Type: "marker", Payload: map[string]any{"i": i}}); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	var snaps []*RunSnapshot
	for writing := true; writing; {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			writing = false
		default:
		}
		snap, err := r.RunSnapshot(ctx, "run")
		if err != nil {
			t.Fatal(err)
		}
		snaps = append(snaps, snap)
	}

	events, err := w.EventsSince(ctx, 0, "run-1", 10_000)
	if err != nil {
		t.Fatal(err)
	}
	for _, snap := range snaps {
		markers := 0
		for _, e := range events {
			if e.Type == "marker" && e.Seq <= snap.Seq {
				markers++
			}
		}
		if len(snap.Executions) < markers {
			t.Fatalf("torn snapshot: Seq %d reflects %d markers but only %d executions", snap.Seq, markers, len(snap.Executions))
		}
	}
	last := snaps[len(snaps)-1]
	if last.Run.ID != "run-1" || len(last.Tasks) != 2 || last.Tasks[1].Dependencies[0] != "a" || len(last.Executions) != n {
		t.Fatalf("final snapshot incomplete: run=%s tasks=%d executions=%d", last.Run.ID, len(last.Tasks), len(last.Executions))
	}
	if len(snaps) < 3 {
		t.Logf("only %d snapshots overlapped the writer; consistency was barely exercised", len(snaps))
	}
	if _, err := r.RunSnapshot(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing run: %v", err)
	}
}

func TestOpenReadOnlyRefusesMissingOrUnmigrated(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if _, err := OpenReadOnly(ctx, filepath.Join(dir, "absent.db")); err == nil {
		t.Fatal("missing database opened")
	}
	if _, err := os.Stat(filepath.Join(dir, "absent.db")); err == nil {
		t.Fatal("OpenReadOnly created a file")
	}
	raw := filepath.Join(dir, "raw.db")
	db, err := sql.Open("sqlite", "file:"+raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE x (a)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := OpenReadOnly(ctx, raw); err == nil || !strings.Contains(err.Error(), "schema version 0") {
		t.Fatalf("unmigrated database: %v", err)
	}
}

func TestBackupKeepsNewest(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	seed(t, s)
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		if _, err := s.Backup(ctx, dir, 2); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "*.db"))
	if len(left) != 2 {
		t.Fatalf("want 2 backups kept, got %d", len(left))
	}
	b, err := Open(ctx, left[1])
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err := b.GetRun(ctx, "run-1"); err != nil {
		t.Fatalf("backup is not a usable database: %v", err)
	}
}

func TestEventsBeforePagesBackwards(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	seed(t, s)
	if err := s.CreateRun(ctx, &c.Run{ID: "run-2", ProjectPath: "/p", UserRequest: "r", Status: c.RunPending, CreatedAt: Now()}); err != nil {
		t.Fatal(err)
	}
	for n := range 10 {
		run := []string{"run-1", "run-2"}[n%2]
		if err := s.AddEvent(ctx, &c.Event{RunID: run, Type: "test.tick", Payload: map[string]any{"n": n}}); err != nil {
			t.Fatal(err)
		}
	}
	all, _ := s.EventsSince(ctx, 0, "run-1", 1000)

	var back []c.Event
	for before := int64(0); ; {
		page, err := s.EventsBefore(ctx, before, "run-1", 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		if len(page) > 1 && page[0].Seq > page[1].Seq {
			t.Fatal("page not oldest first")
		}
		back = append(page, back...)
		before = page[0].Seq
	}
	if len(back) != len(all) {
		t.Fatalf("walked %d events, want %d", len(back), len(all))
	}
	for i := range all {
		if back[i].ID != all[i].ID {
			t.Fatalf("event %d: %s, want %s", i, back[i].ID, all[i].ID)
		}
	}
}
