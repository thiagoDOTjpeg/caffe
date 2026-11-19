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
