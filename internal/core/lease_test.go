package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"caffe/internal/config"
	c "caffe/internal/contracts"
	"caffe/internal/persistence"
)

func fastPoll(cfg *config.Config) { cfg.Runtime.PollInterval = 100 * time.Millisecond }

// hookLease makes fn run before the n-th lease acquisition (1-based) whose
// tracked task is key.
func hookLease(t *testing.T, key string, n int, fn func(path string)) {
	var mu sync.Mutex
	seen := 0
	leaseHook = func(tracked *c.Task, path string) {
		mu.Lock()
		defer mu.Unlock()
		if tracked.Key == key {
			if seen++; seen == n {
				fn(path)
			}
		}
	}
	t.Cleanup(func() { leaseHook = nil })
}

// humanUntilBlocked takes a human lease on path and gives it up once task
// key of the only run is blocked for it; the channel closes then.
func (e *env) humanUntilBlocked(path, key string, release func(l *persistence.Lease)) <-chan struct{} {
	ctx := context.Background()
	l := &persistence.Lease{Path: path, HolderKind: persistence.LeaseHuman, HolderID: "studio-test"}
	if err := e.store.AcquireLease(ctx, l); err != nil {
		e.t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			runs, _ := e.store.ListRuns(ctx, false)
			if len(runs) == 1 {
				ts, _ := e.store.ListTasks(ctx, runs[0].ID)
				for _, t := range ts {
					if t.Key == key && t.Status == c.TaskBlocked {
						release(l)
						return
					}
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		e.t.Error("task never blocked on the human lease")
		release(l)
	}()
	return done
}

func (e *env) blockedOnLease(runID, key string) bool {
	evs, _ := e.store.ListEvents(context.Background(), runID)
	for _, ev := range evs {
		if ev.Type == "task.blocked" && strings.Contains(fmt.Sprint(ev.Payload), leaseBlockedReason) {
			if t := e.tasks(runID)[key]; t != nil && t.ID == ev.TaskID {
				return true
			}
		}
	}
	return false
}

func TestDeveloperWaitsForAHumanLease(t *testing.T) {
	e := newEnv(t, fastPoll)
	e.sc.handle = goodAgents
	var held <-chan struct{}
	hookLease(t, "impl", 1, func(path string) {
		held = e.humanUntilBlocked(path, "impl", func(l *persistence.Lease) { e.store.ReleaseLease(context.Background(), l) })
	})
	run, err := e.o.Run(context.Background(), "Add the feature", nil)
	<-held
	if err != nil || run.Status != c.RunCompleted {
		t.Fatalf("run = %s (%s), %v", run.Status, run.Error, err)
	}
	if !e.blockedOnLease(run.ID, "impl") {
		t.Error("impl never recorded as blocked on the lease")
	}
	if all, _ := e.store.ListLeases(context.Background()); len(all) != 0 {
		t.Errorf("leases left: %+v", all)
	}
}

func TestCorrectionWaitsForAHumanLease(t *testing.T) {
	e := newEnv(t, fastPoll)
	reviews := 0
	e.sc.handle = func(role c.Role, cwd, prompt string) string {
		if role == c.RoleReviewer {
			if reviews++; reviews == 1 {
				return rejectReport
			}
		}
		return goodAgents(role, cwd, prompt)
	}
	var held <-chan struct{}
	// The reviewer's acquisitions: 1 review, 2 correction, 3 second review.
	hookLease(t, "review", 2, func(path string) {
		held = e.humanUntilBlocked(path, "review", func(l *persistence.Lease) { e.store.ReleaseLease(context.Background(), l) })
	})
	run, err := e.o.Run(context.Background(), "Add the feature", nil)
	<-held
	if err != nil || run.Status != c.RunCompleted || reviews != 2 {
		t.Fatalf("run = %s (%s), %v, reviews %d", run.Status, run.Error, err, reviews)
	}
	if !e.blockedOnLease(run.ID, "review") {
		t.Error("the correction did not wait for the human lease")
	}
}

// An abandoned human lease is taken over only once the worktree is clean.
func TestExpiredHumanLeaseBlocksWhileDirty(t *testing.T) {
	e := newEnv(t, fastPoll)
	e.sc.handle = goodAgents
	db, err := sql.Open("sqlite", e.o.Cfg.Persistence.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var held <-chan struct{}
	hookLease(t, "impl", 1, func(path string) {
		os.WriteFile(filepath.Join(path, "manual.txt"), []byte("unsaved work\n"), 0o644)
		held = e.humanUntilBlocked(path, "impl", func(*persistence.Lease) {
			// Studio is gone (no heartbeat) and the human cleaned up.
			os.Remove(filepath.Join(path, "manual.txt"))
		})
		old := persistence.Now().Add(-time.Minute).Format("2006-01-02T15:04:05.000000000Z")
		if _, err := db.Exec(`UPDATE worktree_leases SET heartbeat_at = ? WHERE path = ?`, old, path); err != nil {
			t.Error(err)
		}
	})
	run, err := e.o.Run(context.Background(), "Add the feature", nil)
	<-held
	if err != nil || run.Status != c.RunCompleted {
		t.Fatalf("run = %s (%s), %v", run.Status, run.Error, err)
	}
	if !e.blockedOnLease(run.ID, "impl") {
		t.Error("an expired lease over a dirty worktree did not block")
	}
	if files := gitRun(t, e.repo, "ls-tree", "-r", "--name-only", e.tasks(run.ID)["impl"].BranchName); strings.Contains(files, "manual.txt") {
		t.Error("manual work was committed by the agent")
	}
}

func TestRecoverReleasesAgentLeases(t *testing.T) {
	e := newEnv(t, nil)
	run, _, wt := e.crash(false)
	ctx := context.Background()
	if err := e.store.AcquireLease(ctx, &persistence.Lease{Path: wt, HolderKind: persistence.LeaseAgent, HolderID: "x", RunID: run.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.o.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.GetLease(ctx, wt); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("agent lease survived recovery: %v", err)
	}
}
