package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	c "caffe/internal/contracts"
)

func (s *Service) workspaceRoot(ctx context.Context, id string) (string, error) {
	if id == "" || id == MainWorkspace {
		return s.cfg.Project.Path, nil
	}
	t, err := s.store.GetTask(ctx, id)
	if err != nil {
		return "", notFound(err)
	}
	if t.WorktreePath == "" {
		return "", fmt.Errorf("task %s has no worktree: %w", t.Key, ErrNotFound)
	}
	return t.WorktreePath, nil
}

func (s *Service) ResolvePath(ctx context.Context, workspaceID, p string) (string, error) {
	root, err := s.workspaceRoot(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	p = filepath.Clean(p)
	if !within(p, root) {
		return "", fmt.Errorf("%s is not inside the workspace %s", p, root)
	}
	return p, nil
}

type FileContent struct {
	Path      string
	Text      string
	Size      int64
	Truncated bool
	Binary    bool
	ModTime   time.Time
}

func (s *Service) ReadFile(ctx context.Context, workspaceID, p string, max int64) (*FileContent, error) {
	path, err := s.ResolvePath(ctx, workspaceID, p)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	buf, err := io.ReadAll(io.LimitReader(f, max))
	if err != nil {
		return nil, err
	}
	fc := &FileContent{Path: path, Size: fi.Size(), Truncated: fi.Size() > max, ModTime: fi.ModTime()}
	if bytes.IndexByte(buf[:min(len(buf), 8<<10)], 0) >= 0 {
		fc.Binary = true
		return fc, nil
	}
	fc.Text = strings.ToValidUTF8(string(buf), "�")
	return fc, nil
}

const (
	MarkAdded    = "A"
	MarkModified = "M"
	MarkDeleted  = "D"
	MarkManual   = "●"
)

type FileList struct {
	Paths []string

	Marks map[string]string
	Base  string

	Branch                                  string
	Staged, Unstaged, Untracked, Conflicted int
}

func (s *Service) Files(ctx context.Context, workspaceID string) (*FileList, error) {
	if s.git == nil {
		return nil, fmt.Errorf("listing files needs Git")
	}
	root, err := s.workspaceRoot(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	paths, err := s.git.ListFiles(ctx, root)
	if err != nil {
		return nil, err
	}
	fl := &FileList{Paths: paths, Marks: map[string]string{}}
	if workspaceID != "" && workspaceID != MainWorkspace {
		task, err := s.store.GetTask(ctx, workspaceID)
		if err != nil {
			return nil, notFound(err)
		}
		snap, err := s.store.RunSnapshot(ctx, task.RunID)
		if err != nil {
			return nil, notFound(err)
		}
		if task.Role == c.RoleDeveloper {
			if fl.Base, err = s.diffBase(ctx, task, snap.Run.Metadata["base_commit"], snap.Tasks); err != nil {
				return nil, err
			}
			if fl.Marks, err = s.git.CommittedChanges(ctx, root, fl.Base); err != nil {
				return nil, err
			}
		}
	}
	st, err := s.git.Status2(ctx, root)
	if err != nil {
		return nil, err
	}
	fl.Branch = st.Branch
	for _, e := range st.Entries {
		fl.Marks[e.Path] = MarkManual
		switch {
		case e.XY == "??":
			fl.Untracked++
		case e.XY[0] == 'U' || e.XY[1] == 'U' || e.XY == "AA" || e.XY == "DD":
			fl.Conflicted++
		default:
			if e.XY[0] != '.' {
				fl.Staged++
			}
			if e.XY[1] != '.' {
				fl.Unstaged++
			}
		}
	}
	listed := make(map[string]bool, len(paths))
	for _, p := range paths {
		listed[p] = true
	}
	for p := range fl.Marks {
		if !listed[p] {
			fl.Paths = append(fl.Paths, p)
		}
	}
	return fl, nil
}
