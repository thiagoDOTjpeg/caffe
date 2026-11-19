package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
