package service

import (
	"context"
	"os"

	c "caffe/internal/contracts"
)

const MainWorkspace = "main"

type Workspace struct {
	ID                     string
	Kind                   string
	Path                   string
	Branch                 string
	RunID, TaskID, TaskKey string
	TaskStatus             c.TaskStatus

	Missing bool
	Dirty   bool
	Lease   *Lease
}

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
			s.leaseOf(ctx, &w)
			all = append(all, w)
		}
	}
	return all, nil
}

func (s *Service) ValidationCommands() []string {
	return append([]string(nil), s.cfg.Validation.Commands...)
}
