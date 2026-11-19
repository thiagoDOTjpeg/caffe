package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	c "caffe/internal/contracts"
	"caffe/internal/core"
)

const maxDiffBytes = 4 << 20

type DiffSource string

const (
	DiffLive   DiffSource = "live"
	DiffStored DiffSource = "stored"
)

type Diff struct {
	TaskKey   string
	Base      string
	Text      string
	Files     []string
	Source    DiffSource
	Truncated bool
}

func (s *Service) TaskDiff(ctx context.Context, taskID string) (*Diff, error) {
	task, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return nil, notFound(err)
	}
	if task.Role != c.RoleDeveloper {
		return nil, fmt.Errorf("task %s is a %s task; only developer tasks have changes", task.Key, task.Role)
	}
	snap, err := s.store.RunSnapshot(ctx, task.RunID)
	if err != nil {
		return nil, notFound(err)
	}
	if task.WorktreePath != "" && s.git != nil {
		if _, err := os.Stat(task.WorktreePath); err == nil {
			return s.liveDiff(ctx, task, snap.Run.Metadata["base_commit"], snap.Tasks)
		}
	}
	var latest *c.Artifact
	for _, a := range snap.Artifacts {
		if a.TaskID == task.ID && a.Type == c.ArtifactDiff {
			latest = a
		}
	}
	if latest == nil {
		return nil, fmt.Errorf("task %s has no worktree and no stored diff: %w", task.Key, ErrNotFound)
	}
	return s.storedDiff(task, latest)
}

func (s *Service) diffBase(ctx context.Context, task *c.Task, runBase string, tasks []*c.Task) (string, error) {
	g, err := core.NewGraph(tasks)
	if err != nil {
		return "", err
	}
	var self *c.Task
	for _, t := range g.Tasks() {
		if t.ID == task.ID {
			self = t
		}
	}
	return core.DiffBase(ctx, s.git, task.WorktreePath, g, self, runBase, func(t *c.Task) string { return t.BranchName })
}

func (s *Service) liveDiff(ctx context.Context, task *c.Task, runBase string, tasks []*c.Task) (*Diff, error) {
	wt := task.WorktreePath
	base, err := s.diffBase(ctx, task, runBase, tasks)
	if err != nil {
		return nil, err
	}
	text, err := s.git.Diff(ctx, wt, base)
	if err != nil {
		return nil, err
	}
	files, err := s.git.ChangedFiles(ctx, wt, base)
	if err != nil {
		return nil, err
	}
	d := &Diff{TaskKey: task.Key, Base: base, Text: text, Files: files, Source: DiffLive}
	if len(d.Text) > maxDiffBytes {
		d.Text, d.Truncated = strings.ToValidUTF8(d.Text[:maxDiffBytes], ""), true
	}
	return d, nil
}

func (s *Service) storedDiff(task *c.Task, a *c.Artifact) (*Diff, error) {
	if !within(a.Path, s.cfg.ArtifactDir()) {
		return nil, fmt.Errorf("diff artifact of task %s points outside the artifact directory: %s", task.Key, a.Path)
	}
	f, err := os.Open(a.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("diff artifact of task %s is missing on disk: %w", task.Key, ErrNotFound)
		}
		return nil, err
	}
	defer f.Close()
	buf, err := io.ReadAll(io.LimitReader(f, maxDiffBytes+1))
	if err != nil {
		return nil, err
	}
	d := &Diff{TaskKey: task.Key, Source: DiffStored}
	if len(buf) > maxDiffBytes {
		buf, d.Truncated = buf[:maxDiffBytes], true
	}

	d.Text = strings.TrimSuffix(strings.ToValidUTF8(string(buf), ""), "\n")
	return d, nil
}
