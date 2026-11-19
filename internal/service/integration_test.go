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

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestIntegrationDecideAndMerge(t *testing.T) {
	svc, gm, tasks := stackedRepo(t)
	ctx := context.Background()
	must(t, gm.ExcludeLocally(ctx, ".caffe/"))
	w, err := persistence.Open(ctx, svc.cfg.Persistence.Path)
	must(t, err)
	defer w.Close()
	svc.WithWriter(w)
	root := svc.cfg.Project.Path
	runID := tasks["top"].RunID

	items, err := svc.Integration(ctx, runID)
	if err != nil || len(items) != 2 {
		t.Fatalf("items %d, %v", len(items), err)
	}
	for _, it := range items {
		if it.Review != "approved" || it.Validation != "none" || it.Merged || it.Decision != nil || !strings.Contains(it.Stat, "changed") {
			t.Errorf("%s: %+v", it.Task.Key, it)
		}
	}

	p, err := svc.PreviewMerge(ctx, tasks["top"].ID)
	must(t, err)
	if p.Ready() || !strings.Contains(strings.Join(p.Blockers, ";"), "not approved") || p.Command != "git merge --no-ff --no-edit caffe/33333333/top" || p.Target != "main" {
		t.Fatalf("preview: %+v", p)
	}
	if _, err := svc.Merge(ctx, tasks["top"].ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("merged without approval: %v", err)
	}
	must(t, svc.Decide(ctx, tasks["top"].ID, DecisionReject, "not yet"))
	must(t, svc.Decide(ctx, tasks["top"].ID, DecisionApprove, "ok now"))
	if err := svc.Decide(ctx, tasks["top"].ID, "maybe", ""); err == nil {
		t.Fatal("bad decision accepted")
	}

	must(t, os.WriteFile(filepath.Join(root, "scratch.txt"), []byte("x"), 0o644))
	p, _ = svc.PreviewMerge(ctx, tasks["top"].ID)
	if p.Ready() || !strings.Contains(strings.Join(p.Blockers, ";"), "uncommitted changes") {
		t.Fatalf("dirty main: %+v", p.Blockers)
	}
	must(t, os.Remove(filepath.Join(root, "scratch.txt")))

	p, err = svc.Merge(ctx, tasks["top"].ID)
	if err != nil {
		t.Fatalf("merge: %v %+v", err, p)
	}
	if got := gitIn(t, root, "show", "HEAD:top.txt"); got != "top" {
		t.Fatalf("top.txt on main: %q", got)
	}
	if parents := strings.Fields(gitIn(t, root, "log", "-1", "--format=%P")); len(parents) != 2 {
		t.Errorf("not a merge commit: %v", parents)
	}
	items, _ = svc.Integration(ctx, runID)
	for _, it := range items {
		if it.Task.Key == "top" && (!it.Merged || it.Decision.Decision != DecisionMerged || len(it.History) != 3 || it.Decision.Author != "t <t@example.com>") {
			t.Errorf("after merge: %+v", it)
		}
	}
	if p, _ := svc.PreviewMerge(ctx, tasks["top"].ID); p.Ready() {
		t.Error("merging twice is ready")
	}
}

func TestMergePreviewShowsConflicts(t *testing.T) {
	svc, gm, tasks := stackedRepo(t)
	ctx := context.Background()
	must(t, gm.ExcludeLocally(ctx, ".caffe/"))
	w, err := persistence.Open(ctx, svc.cfg.Persistence.Path)
	must(t, err)
	defer w.Close()
	svc.WithWriter(w)
	root := svc.cfg.Project.Path
	must(t, os.WriteFile(filepath.Join(root, "base.txt"), []byte("main's own\n"), 0o644))
	gitIn(t, root, "add", "base.txt")
	gitIn(t, root, "commit", "-qm", "conflicting")
	must(t, svc.Decide(ctx, tasks["base"].ID, DecisionApprove, ""))
	p, err := svc.PreviewMerge(ctx, tasks["base"].ID)
	must(t, err)
	if p.Ready() || len(p.Conflicts) != 1 || p.Conflicts[0] != "base.txt" {
		t.Fatalf("conflicts: %+v", p)
	}
	head := gitIn(t, root, "rev-parse", "HEAD")
	if _, err := svc.Merge(ctx, tasks["base"].ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("merged a conflict: %v", err)
	}
	if gitIn(t, root, "rev-parse", "HEAD") != head || gitIn(t, root, "status", "--porcelain") != "" {
		t.Fatal("a refused merge touched the main checkout")
	}
}
