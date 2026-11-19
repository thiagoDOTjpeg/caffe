package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaces(t *testing.T) {
	ctx := context.Background()
	svc, gm, tasks := stackedRepo(t)
	must(t, gm.ExcludeLocally(ctx, ".caffe/"))
	must(t, os.WriteFile(filepath.Join(tasks["base"].WorktreePath, "wip.txt"), []byte("x"), 0o644))
	must(t, gm.RemoveWorktree(ctx, tasks["top"].WorktreePath))

	ws, err := svc.Workspaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Workspace{}
	for _, w := range ws {
		byID[w.ID] = w
	}
	if len(ws) != 3 || ws[0].ID != MainWorkspace || ws[0].Branch != "main" || ws[0].Dirty {
		t.Fatalf("workspaces = %+v", ws)
	}
	if b := byID["t-base"]; !b.Dirty || b.Missing || b.TaskKey != "base" || b.Branch != tasks["base"].BranchName {
		t.Errorf("base = %+v", b)
	}
	if top := byID["t-top"]; !top.Missing || top.Dirty {
		t.Errorf("top = %+v", top)
	}
}
