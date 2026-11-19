package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvePathAndReadFile(t *testing.T) {
	ctx := context.Background()
	svc, _, tasks := stackedRepo(t)
	wt := tasks["base"].WorktreePath
	outside := t.TempDir()
	must(t, os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o644))
	must(t, os.Symlink(filepath.Join(outside, "secret"), filepath.Join(wt, "link")))
	must(t, os.WriteFile(filepath.Join(wt, "bin"), []byte("a\x00b"), 0o644))

	fc, err := svc.ReadFile(ctx, "t-base", "base.txt", 1<<20)
	if err != nil || fc.Text != "base\n" || fc.Path != filepath.Join(wt, "base.txt") {
		t.Fatalf("read: %+v %v", fc, err)
	}
	if fc, _ := svc.ReadFile(ctx, "t-base", "base.txt", 2); !fc.Truncated || fc.Text != "ba" {
		t.Errorf("bounded read: %+v", fc)
	}
	if fc, _ := svc.ReadFile(ctx, "t-base", "bin", 100); !fc.Binary {
		t.Error("binary not detected")
	}
	if fc, err := svc.ReadFile(ctx, MainWorkspace, "README.md", 100); err != nil || fc.Text != "x\n" {
		t.Errorf("main: %+v %v", fc, err)
	}
	for _, p := range []string{"../top/top.txt", "link", filepath.Join(outside, "secret"), "/etc/passwd", "."} {
		if _, err := svc.ResolvePath(ctx, "t-base", p); err == nil || !strings.Contains(err.Error(), "not inside") {
			t.Errorf("%s: %v", p, err)
		}
	}
	if _, err := svc.ResolvePath(ctx, "t-review", "x"); err == nil {
		t.Error("a task without a worktree resolved")
	}
}

func TestFilesAndMarks(t *testing.T) {
	ctx := context.Background()
	svc, gm, tasks := stackedRepo(t)
	must(t, gm.ExcludeLocally(ctx, ".caffe/"))
	wt := tasks["top"].WorktreePath
	must(t, os.Remove(filepath.Join(wt, "README.md")))
	_, err := gm.CommitAll(ctx, wt, "rm readme")
	must(t, err)
	must(t, os.WriteFile(filepath.Join(wt, "top.txt"), []byte("edited"), 0o644))
	must(t, os.WriteFile(filepath.Join(wt, "new file.txt"), []byte("n"), 0o644))

	fl, err := svc.Files(ctx, "t-top")
	if err != nil {
		t.Fatal(err)
	}
	// top's base is base's branch: base.txt is not top's change.
	want := map[string]string{"top.txt": MarkManual, "README.md": MarkDeleted, "new file.txt": MarkManual}
	if len(fl.Marks) != len(want) {
		t.Errorf("marks = %v", fl.Marks)
	}
	for p, m := range want {
		if fl.Marks[p] != m {
			t.Errorf("%s: mark %q, want %q (all: %v)", p, fl.Marks[p], m, fl.Marks)
		}
	}
	if fl.Branch != tasks["top"].BranchName || fl.Unstaged != 1 || fl.Untracked != 1 || fl.Staged != 0 {
		t.Errorf("status: branch %q staged %d unstaged %d untracked %d", fl.Branch, fl.Staged, fl.Unstaged, fl.Untracked)
	}
	got := strings.Join(fl.Paths, ",")
	for _, p := range []string{"base.txt", "top.txt", "new file.txt", "README.md"} {
		if !strings.Contains(got, p) {
			t.Errorf("%s not listed: %s", p, got)
		}
	}
	if main, err := svc.Files(ctx, MainWorkspace); err != nil || len(main.Marks) != 0 || strings.Join(main.Paths, ",") != "README.md" {
		t.Errorf("main: %+v %v", main, err)
	}
}
