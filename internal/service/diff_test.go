package service

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"caffe/internal/config"
	c "caffe/internal/contracts"
	"caffe/internal/git"
	"caffe/internal/persistence"
	"caffe/internal/runtime"
)

func stackedRepo(t *testing.T) (*Service, *git.Manager, map[string]*c.Task) {
	t.Helper()
	ctx := context.Background()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(root, "init", "-q", "-b", "main")
	run(root, "config", "user.email", "t@example.com")
	run(root, "config", "user.name", "t")
	must(t, os.WriteFile(filepath.Join(root, "README.md"), []byte("x\n"), 0o644))
	run(root, "add", "-A")
	run(root, "commit", "-qm", "init")

	cfg := config.Default("demo")
	cfg.Project.Path = root
	cfg.Git.WorktreeDirectory = filepath.Join(root, ".caffe", "worktrees")
	cfg.Persistence.Path = filepath.Join(root, ".caffe", "caffe.db")
	gm, err := git.New(ctx, root, cfg.Git.WorktreeDirectory)
	if err != nil {
		t.Fatal(err)
	}
	head, _ := gm.Head(ctx)

	w, err := persistence.Open(ctx, cfg.Persistence.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	now := persistence.Now()
	r := &c.Run{ID: "33333333-0000-0000-0000-000000000000", ProjectPath: root, UserRequest: "stack", Status: c.RunCompleted,
		CreatedAt: now, Metadata: map[string]string{"base_commit": head}}
	must(t, w.CreateRun(ctx, r))
	tasks := map[string]*c.Task{
		"base":   {ID: "t-base", RunID: r.ID, Key: "base", Title: "b", Description: "d", Role: c.RoleDeveloper, Status: c.TaskCompleted, AcceptanceCriteria: []string{"x"}, MaxAttempts: 1, CreatedAt: now},
		"top":    {ID: "t-top", RunID: r.ID, Key: "top", Title: "t", Description: "d", Role: c.RoleDeveloper, Status: c.TaskCompleted, Dependencies: []string{"t-base"}, AcceptanceCriteria: []string{"x"}, MaxAttempts: 1, CreatedAt: now},
		"review": {ID: "t-review", RunID: r.ID, Key: "review", Title: "r", Description: "d", Role: c.RoleReviewer, Status: c.TaskCompleted, Dependencies: []string{"t-top"}, AcceptanceCriteria: []string{"x"}, MaxAttempts: 1, CreatedAt: now},
	}
	must(t, w.CreateTasks(ctx, []*c.Task{tasks["base"], tasks["top"], tasks["review"]}))

	work := func(task *c.Task, from, file string) {
		wt, err := gm.CreateWorktree(ctx, filepath.Join("33333333", task.Key), "caffe/33333333/"+task.Key, from)
		must(t, err)
		must(t, os.WriteFile(filepath.Join(wt, file), []byte(task.Key+"\n"), 0o644))
		_, err = gm.CommitAll(ctx, wt, "work")
		must(t, err)
		task.WorktreePath, task.BranchName = wt, "caffe/33333333/"+task.Key
		must(t, w.UpdateTask(ctx, task, task.Status, "worktree"))
	}
	work(tasks["base"], head, "base.txt")
	work(tasks["top"], tasks["base"].BranchName, "top.txt")

	for _, key := range []string{"base", "top"} {
		task := tasks[key]
		base := head
		if key == "top" {
			mb, err := gm.MergeBase(ctx, task.WorktreePath, "HEAD", tasks["base"].BranchName)
			must(t, err)
			base = mb
		}
		diff, err := gm.Diff(ctx, task.WorktreePath, base)
		must(t, err)
		path := filepath.Join(cfg.ArtifactDir(), r.ID, key, "changes.diff")
		must(t, os.MkdirAll(filepath.Dir(path), 0o755))
		must(t, os.WriteFile(path, []byte(diff+"\n"), 0o644))
		must(t, w.AddArtifact(ctx, &c.Artifact{RunID: r.ID, TaskID: task.ID, Type: c.ArtifactDiff, Path: path}))
	}

	ro, err := persistence.OpenReadOnly(ctx, cfg.Persistence.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ro.Close() })
	return New(cfg, ro, runtime.NewFake(), gm), gm, tasks
}

func TestTaskDiffLiveMatchesStoredAndFollowsStacking(t *testing.T) {
	ctx := context.Background()
	svc, gm, tasks := stackedRepo(t)

	for _, key := range []string{"base", "top"} {
		live, err := svc.TaskDiff(ctx, tasks[key].ID)
		if err != nil {
			t.Fatal(err)
		}
		if live.Source != DiffLive || len(live.Files) != 1 || live.Files[0] != key+".txt" {
			t.Errorf("%s: source=%s files=%v", key, live.Source, live.Files)
		}

		if key == "top" && strings.Contains(live.Text, "base.txt") {
			t.Errorf("top diff includes its dependency's change:\n%s", live.Text)
		}
		stored, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(tasks[key].WorktreePath)), "..", "artifacts",
			"33333333-0000-0000-0000-000000000000", key, "changes.diff"))
		if err != nil {
			t.Fatal(err)
		}
		if live.Text+"\n" != string(stored) {
			t.Errorf("%s: live diff differs from what the engine stored", key)
		}
	}

	live, _ := svc.TaskDiff(ctx, tasks["top"].ID)
	must(t, gm.RemoveWorktree(ctx, tasks["top"].WorktreePath))
	stored, err := svc.TaskDiff(ctx, tasks["top"].ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Source != DiffStored || stored.Text != live.Text {
		t.Errorf("stored fallback: source=%s equal=%v", stored.Source, stored.Text == live.Text)
	}

	if _, err := svc.TaskDiff(ctx, tasks["review"].ID); err == nil || !strings.Contains(err.Error(), "only developer tasks") {
		t.Errorf("reviewer task: %v", err)
	}
	if _, err := svc.TaskDiff(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing task: %v", err)
	}
}
