package service

import (
	"context"
	"os"

	c "caffe/internal/contracts"
)

// MainWorkspace is the id of the project's own checkout.
const MainWorkspace = "main"

// Workspace is a checkout Studio can show: the main checkout or a task's
// worktree (architecture §9.1).
type Workspace struct {
	ID                     string // MainWorkspace or the task id
	Kind                   string // "main" | "task"
	Path                   string
	Branch                 string
	RunID, TaskID, TaskKey string
	TaskStatus             c.TaskStatus
	// Missing: recorded in the database but gone from disk (cleaned).
	Missing bool
	Dirty   bool
}

// Workspaces lists the main checkout, then every task worktree the engine
// recorded, newest run first. Git errors on one worktree do not fail the
// list: that worktree is reported as not dirty.
func (s *Service) Workspaces(ctx context.Context) ([]Workspace, error) {
	main := Workspace{ID: MainWorkspace, Kind: "main", Path: s.cfg.Project.Path}
	if s.git != nil {
		main.Branch, _ = s.git.Branch(ctx, main.Path)
		main.Dirty, _ = s.git.IsDirty(ctx, main.Path)
	}
	all := []Workspace{main}
	runs, err := s.store.ListRuns(ctx, false)
	if err != nil {
		return nil, err
	}
	for _, r := range runs {
		tasks, err := s.store.ListTasks(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		for _, t := range tasks {
			if t.WorktreePath == "" {
				continue
			}
			w := Workspace{ID: t.ID, Kind: "task", Path: t.WorktreePath, Branch: t.BranchName,
				RunID: r.ID, TaskID: t.ID, TaskKey: t.Key, TaskStatus: t.Status}
			if _, err := os.Stat(w.Path); err != nil {
				w.Missing = true
			} else if s.git != nil {
				w.Dirty, _ = s.git.IsDirty(ctx, w.Path)
			}
			all = append(all, w)
		}
	}
	return all, nil
}

// ValidationCommands are the configured validation.commands, for running
// them by hand in a workspace (EX-03).
func (s *Service) ValidationCommands() []string {
	return append([]string(nil), s.cfg.Validation.Commands...)
}
