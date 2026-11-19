package service

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"caffe/internal/persistence"
)

func TestHumanLeaseEditCommitAndRelease(t *testing.T) {
	svc, _, tasks := stackedRepo(t)
	ctx := context.Background()
	w, err := persistence.Open(ctx, svc.cfg.Persistence.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	svc.WithWriter(w)
	top := tasks["top"]

	if _, err := svc.AcquireLease(ctx, MainWorkspace, "studio-1"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("main checkout leased: %v", err)
	}
	agent := &persistence.Lease{Path: top.WorktreePath, HolderKind: persistence.LeaseAgent, HolderID: "r/t"}
	must(t, w.AcquireLease(ctx, agent))
	if _, err := svc.AcquireLease(ctx, top.ID, "studio-1"); !errors.Is(err, ErrLeaseHeld) || !strings.Contains(err.Error(), "agent is writing") {
		t.Fatalf("acquired over an agent: %v", err)
	}
	must(t, w.ReleaseLease(ctx, agent))

	l, err := svc.AcquireLease(ctx, top.ID, "studio-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AcquireLease(ctx, top.ID, "studio-2"); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("second studio: %v", err)
	}
	must(t, svc.RenewLease(ctx, l))
	ws, _ := svc.Workspaces(ctx)
	var shown *Lease
	for _, x := range ws {
		if x.ID == top.ID {
			shown = x.Lease
		}
	}
	if shown == nil || shown.HolderID != "studio-1" {
		t.Fatalf("workspace lease: %+v", shown)
	}

	must(t, os.WriteFile(filepath.Join(top.WorktreePath, "manual.txt"), []byte("by hand\n"), 0o644))
	if err := svc.ReleaseLease(ctx, l); !errors.Is(err, ErrDirty) {
		t.Fatalf("released a dirty worktree: %v", err)
	}
	if _, err := svc.CommitManual(ctx, &Lease{Path: l.Path, HolderKind: l.HolderKind, HolderID: "studio-2"}, "x"); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("commit without the lease: %v", err)
	}
	if ok, err := svc.CommitManual(ctx, l, "fix the thing"); !ok || err != nil {
		t.Fatalf("commit: %v %v", ok, err)
	}
	out, _ := exec.Command("git", "-C", top.WorktreePath, "log", "-1", "--format=%s|%ae").Output()
	if got := strings.TrimSpace(string(out)); got != "manual: fix the thing|t@example.com" {
		t.Errorf("commit: %q", got)
	}

	must(t, os.WriteFile(filepath.Join(top.WorktreePath, "scratch.txt"), []byte("oops\n"), 0o644))
	must(t, svc.DiscardManual(ctx, l))
	if _, err := os.Stat(filepath.Join(top.WorktreePath, "scratch.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Error("discard kept the file")
	}
	must(t, svc.ReleaseLease(ctx, l))
	if _, err := w.GetLease(ctx, l.Path); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("lease after release: %v", err)
	}
	if err := svc.RenewLease(ctx, l); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("renew after release: %v", err)
	}
}

func TestSaveFileIsSafe(t *testing.T) {
	svc, _, tasks := stackedRepo(t)
	ctx := context.Background()
	w, err := persistence.Open(ctx, svc.cfg.Persistence.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	svc.WithWriter(w)
	top := tasks["top"]
	f, err := svc.ReadFile(ctx, top.ID, "top.txt", 1<<20)
	must(t, err)
	req := SaveRequest{Path: "top.txt", Text: "edited\n", LoadedMod: f.ModTime, LoadedSize: f.Size}
	if _, err := svc.SaveFile(ctx, top.ID, req); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("saved without a lease: %v", err)
	}
	if _, err := svc.SaveFile(ctx, MainWorkspace, req); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("saved in the main checkout: %v", err)
	}
	l, err := svc.AcquireLease(ctx, top.ID, "studio-1")
	must(t, err)
	req.Lease = l
	saved, err := svc.SaveFile(ctx, top.ID, req)
	must(t, err)
	if b, _ := os.ReadFile(filepath.Join(top.WorktreePath, "top.txt")); string(b) != "edited\n" {
		t.Fatalf("content %q", b)
	}
	must(t, os.WriteFile(filepath.Join(top.WorktreePath, "top.txt"), []byte("someone else, longer\n"), 0o644))
	req.Text, req.LoadedMod, req.LoadedSize = "mine\n", saved.ModTime, saved.Size
	if _, err := svc.SaveFile(ctx, top.ID, req); !errors.Is(err, ErrConflict) {
		t.Fatalf("overwrote a file changed on disk: %v", err)
	}
	req.Overwrite = true
	_, err = svc.SaveFile(ctx, top.ID, req)
	must(t, err)
	if b, _ := os.ReadFile(filepath.Join(top.WorktreePath, "top.txt")); string(b) != "mine\n" {
		t.Fatalf("overwrite: %q", b)
	}
	if _, err := svc.SaveFile(ctx, top.ID, SaveRequest{Lease: l, Path: "../../escape.txt", Text: "x"}); err == nil {
		t.Fatal("saved outside the workspace")
	}
	left, _ := filepath.Glob(filepath.Join(top.WorktreePath, ".top.txt.caffe-*"))
	if len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}
