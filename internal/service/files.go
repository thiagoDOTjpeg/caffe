package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	c "caffe/internal/contracts"
)

// workspaceRoot is the checkout a workspace id names.
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

// ResolvePath turns p (relative to the workspace, or absolute) into an
// absolute path that, after following symlinks, is inside the workspace
// (NFR-S2). Paths come from reports and output, so they are untrusted.
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

// FileContent is a file of a workspace. Source code is shown as is (like a
// diff), not redacted.
type FileContent struct {
	Path      string // resolved, absolute
	Text      string
	Size      int64
	Truncated bool
	Binary    bool // a NUL byte in the first 8 KiB; Text is empty
}

// ReadFile reads up to max bytes of a file inside a workspace.
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
	fc := &FileContent{Path: path, Size: fi.Size(), Truncated: fi.Size() > max}
	if bytes.IndexByte(buf[:min(len(buf), 8<<10)], 0) >= 0 {
		fc.Binary = true
		return fc, nil
	}
	fc.Text = strings.ToValidUTF8(string(buf), "�")
	return fc, nil
}

// Change marks of FileList.Marks.
const (
	MarkAdded    = "A"
	MarkModified = "M"
	MarkDeleted  = "D"
	MarkManual   = "●" // uncommitted: a manual edit, or an agent still writing
)

// FileList is the content of a workspace for the explorer.
type FileList struct {
	Paths []string // slash-separated, relative to the workspace; deleted files included
	// Marks: committed changes of a developer task relative to its diff base,
	// overridden by MarkManual for uncommitted files.
	Marks map[string]string
	Base  string // the diff base the marks are relative to; "" for main

	// git status of the checkout (WS-06).
	Branch                                  string
	Staged, Unstaged, Untracked, Conflicted int
}

// Files lists a workspace (git ls-files: tracked and untracked, not
// ignored) with its change marks.
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
			fl.Paths = append(fl.Paths, p) // deleted: still shown, with its mark
		}
	}
	return fl, nil
}
