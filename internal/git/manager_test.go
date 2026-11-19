package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func newRepo(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "init")
	return dir
}

func TestWorktreeLifecycle(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	m, err := New(ctx, repo, filepath.Join(repo, ".caffe", "worktrees"))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.ExcludeLocally(ctx, ".caffe/"); err != nil {
		t.Fatal(err)
	}
	if err := m.ExcludeLocally(ctx, ".caffe/"); err != nil {
		t.Fatal(err)
	}
	base, _ := m.Head(ctx)

	wt, err := m.CreateWorktree(ctx, "r1/impl", "caffe/r1/impl", base)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.CreateWorktree(ctx, "r1/other", "caffe/r1/impl", base); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("branch collision not detected: %v", err)
	}
	if _, err := m.CreateWorktree(ctx, "r1/impl", "caffe/r1/impl2", base); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("path collision not detected: %v", err)
	}
	if _, err := m.CreateWorktree(ctx, "../../escape", "x", base); err == nil {
		t.Fatal("worktree outside the managed directory must be refused")
	}

	wt2, err := m.CreateWorktree(ctx, "r1/second", "caffe/r1/second", base)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "new.txt"), []byte("n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(wt2, "a.txt")); string(b) != "one\n" {
		t.Fatalf("worktrees are not isolated: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "a.txt")); string(b) != "one\n" {
		t.Fatalf("main checkout was modified: %q", b)
	}

	if dirty, err := m.IsDirty(ctx, wt); err != nil || !dirty {
		t.Fatalf("dirty = %v, %v", dirty, err)
	}

	if err := m.RemoveWorktree(ctx, wt); err == nil {
		t.Fatal("dirty worktree was removed")
	}
	if ok, err := m.CommitAll(ctx, wt, "caffe: impl"); err != nil || !ok {
		t.Fatalf("commit = %v, %v", ok, err)
	}
	if ok, err := m.CommitAll(ctx, wt, "again"); err != nil || ok {
		t.Fatalf("nothing to commit, got %v, %v", ok, err)
	}
	diff, err := m.Diff(ctx, wt, base)
	if err != nil || !strings.Contains(diff, "+two") || !strings.Contains(diff, "new.txt") {
		t.Fatalf("diff = %q, %v", diff, err)
	}
	files, err := m.ChangedFiles(ctx, wt, base)
	if err != nil || len(files) != 2 {
		t.Fatalf("changed = %v, %v", files, err)
	}

	list, err := m.ListWorktrees(ctx)
	if err != nil || len(list) != 2 || list[0].Branch != "caffe/r1/impl" {
		t.Fatalf("worktrees = %+v, %v", list, err)
	}

	if dirty, _ := m.IsDirty(ctx, repo); dirty {
		st, _ := m.Status(ctx, repo)
		t.Fatalf("main checkout dirty: %q", st)
	}

	if err := m.RemoveWorktree(ctx, wt); err != nil {
		t.Fatal(err)
	}
	if !m.branchExists(ctx, "caffe/r1/impl") {
		t.Fatal("branch must survive worktree removal")
	}
	if err := m.RemoveWorktree(ctx, repo); err == nil {
		t.Fatal("removing a path outside the managed directory must be refused")
	}
}

func TestNewRejectsNonRepos(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if _, err := New(ctx, dir, filepath.Join(dir, "wt")); err == nil {
		t.Fatal("plain directory accepted")
	}
	repo := newRepo(t)
	sub := filepath.Join(repo, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := New(ctx, sub, filepath.Join(sub, "wt")); err == nil {
		t.Fatal("subdirectory of a repository accepted as project root")
	}
}
