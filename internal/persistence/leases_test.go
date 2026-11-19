package persistence

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestLeaseLifecycle(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	agent := &Lease{Path: "/w/a", HolderKind: LeaseAgent, HolderID: "exec-1", RunID: "r1", TaskID: "t1"}
	human := &Lease{Path: "/w/a", HolderKind: LeaseHuman, HolderID: "studio-1"}
	if err := s.AcquireLease(ctx, agent); err != nil {
		t.Fatal(err)
	}
	if err := s.AcquireLease(ctx, agent); err != nil {
		t.Fatalf("same holder re-acquires: %v", err)
	}
	if err := s.AcquireLease(ctx, human); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("second holder: %v", err)
	}
	if err := s.RenewLease(ctx, human); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("renew by non-holder: %v", err)
	}
	if err := s.ReleaseLease(ctx, human); err != nil {
		t.Fatal(err)
	}
	if l, err := s.GetLease(ctx, "/w/a"); err != nil || l.HolderID != "exec-1" || l.TaskID != "t1" {
		t.Fatalf("release by non-holder changed it: %+v %v", l, err)
	}
	if err := s.ReleaseLease(ctx, agent); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetLease(ctx, "/w/a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after release: %v", err)
	}

	// Expiry removes only an old heartbeat.
	if err := s.AcquireLease(ctx, human); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ExpireLease(ctx, "/w/a", Now().Add(-time.Minute)); ok || err != nil {
		t.Fatalf("fresh lease expired: %v %v", ok, err)
	}
	l, _ := s.GetLease(ctx, "/w/a")
	if l.Expired(Now(), time.Minute) || !l.Expired(Now().Add(2*time.Minute), time.Minute) {
		t.Error("Expired")
	}
	if ok, err := s.ExpireLease(ctx, "/w/a", Now().Add(time.Second)); !ok || err != nil {
		t.Fatalf("old lease not expired: %v %v", ok, err)
	}

	// Recovery drops the run's agent leases only.
	for i, l := range []*Lease{
		{Path: "/w/1", HolderKind: LeaseAgent, HolderID: "e1", RunID: "r1"},
		{Path: "/w/2", HolderKind: LeaseAgent, HolderID: "e2", RunID: "r2"},
		{Path: "/w/3", HolderKind: LeaseHuman, HolderID: "s", RunID: "r1"},
	} {
		if err := s.AcquireLease(ctx, l); err != nil {
			t.Fatal(i, err)
		}
	}
	if err := s.ReleaseAgentLeases(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	all, _ := s.ListLeases(ctx)
	if len(all) != 2 || all[0].Path != "/w/2" || all[1].Path != "/w/3" {
		t.Fatalf("left: %+v", all)
	}
}

// Two processes (two connections) racing for one worktree: exactly one wins.
func TestLeaseAcquireRace(t *testing.T) {
	a, path := open(t)
	b, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for round := range 20 {
		p := fmt.Sprintf("/w/%d", round)
		var wins sync.WaitGroup
		var mu sync.Mutex
		won := 0
		for i, s := range []*Store{a, b, a, b} {
			wins.Add(1)
			go func() {
				defer wins.Done()
				err := s.AcquireLease(context.Background(), &Lease{Path: p, HolderKind: LeaseHuman, HolderID: fmt.Sprint(i)})
				if err == nil {
					mu.Lock()
					won++
					mu.Unlock()
				} else if !errors.Is(err, ErrLeaseHeld) {
					t.Error(err)
				}
			}()
		}
		wins.Wait()
		if won != 1 {
			t.Fatalf("round %d: %d winners", round, won)
		}
	}
}
