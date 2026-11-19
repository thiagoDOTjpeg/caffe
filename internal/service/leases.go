package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"caffe/internal/core"
	"caffe/internal/persistence"
)

var (
	ErrLeaseHeld = errors.New("worktree held by another holder")
	ErrDirty     = errors.New("worktree has uncommitted changes")
)

type Lease struct {
	WorkspaceID string
	Path        string
	HolderKind  string
	HolderID    string
	AcquiredAt  time.Time
	HeartbeatAt time.Time
}

func (s *Service) WithWriter(store *persistence.Store) *Service {
	s.w = store
	return s
}

func toLease(ws string, l *persistence.Lease) *Lease {
	return &Lease{WorkspaceID: ws, Path: l.Path, HolderKind: l.HolderKind, HolderID: l.HolderID, AcquiredAt: l.AcquiredAt, HeartbeatAt: l.HeartbeatAt}
}

func (l *Lease) row() *persistence.Lease {
	return &persistence.Lease{Path: l.Path, HolderKind: l.HolderKind, HolderID: l.HolderID}
}

func (s *Service) leaseRoot(ctx context.Context, workspaceID string) (string, error) {
	if s.w == nil || s.git == nil {
		return "", fmt.Errorf("leases: %w", ErrUnsupported)
	}
	if workspaceID == "" || workspaceID == MainWorkspace {
		return "", fmt.Errorf("the main checkout is never edited by Caffè: %w", ErrUnsupported)
	}
	return s.workspaceRoot(ctx, workspaceID)
}

func (s *Service) AcquireLease(ctx context.Context, workspaceID, holderID string) (*Lease, error) {
	root, err := s.leaseRoot(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	t, err := s.store.GetTask(ctx, workspaceID)
	if err != nil {
		return nil, notFound(err)
	}
	l := &persistence.Lease{Path: root, HolderKind: persistence.LeaseHuman, HolderID: holderID, RunID: t.RunID, TaskID: t.ID}
	for range 2 {
		err := s.w.AcquireLease(ctx, l)
		if err == nil {
			return toLease(workspaceID, l), nil
		}
		if !errors.Is(err, persistence.ErrLeaseHeld) {
			return nil, err
		}
		cur, err := s.w.GetLease(ctx, root)
		if errors.Is(err, persistence.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if cur.HolderKind == persistence.LeaseAgent {
			return nil, fmt.Errorf("an agent is writing in %s (task %s): %w", root, t.Key, ErrLeaseHeld)
		}
		if !cur.Expired(persistence.Now(), core.HumanLeaseTTL) {
			return nil, fmt.Errorf("another Caffè Studio is editing %s: %w", root, ErrLeaseHeld)
		}
		if _, err := s.w.ExpireLease(ctx, root, persistence.Now().Add(-core.HumanLeaseTTL)); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("%s: %w", root, ErrLeaseHeld)
}

func (s *Service) RenewLease(ctx context.Context, l *Lease) error {
	if s.w == nil {
		return fmt.Errorf("leases: %w", ErrUnsupported)
	}
	row := l.row()
	if err := s.w.RenewLease(ctx, row); err != nil {
		if errors.Is(err, persistence.ErrLeaseHeld) {
			return fmt.Errorf("the lease of %s was lost: %w", l.Path, ErrLeaseHeld)
		}
		return err
	}
	l.HeartbeatAt = row.HeartbeatAt
	return nil
}

func (s *Service) ReleaseLease(ctx context.Context, l *Lease) error {
	if s.w == nil {
		return fmt.Errorf("leases: %w", ErrUnsupported)
	}
	dirty, err := s.git.IsDirty(ctx, l.Path)
	if err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf("commit or discard the manual changes in %s first: %w", l.Path, ErrDirty)
	}
	return s.w.ReleaseLease(ctx, l.row())
}

func (s *Service) held(ctx context.Context, l *Lease) error {
	cur, err := s.store.GetLease(ctx, l.Path)
	if err != nil || cur.HolderKind != l.HolderKind || cur.HolderID != l.HolderID {
		return fmt.Errorf("%s is not leased by this Studio: %w", l.Path, ErrLeaseHeld)
	}
	return nil
}

func (s *Service) CommitManual(ctx context.Context, l *Lease, message string) (bool, error) {
	if err := s.held(ctx, l); err != nil {
		return false, err
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return false, errors.New("a commit message is required")
	}
	return s.git.CommitAll(ctx, l.Path, "manual: "+message)
}

func (s *Service) DiscardManual(ctx context.Context, l *Lease) error {
	if err := s.held(ctx, l); err != nil {
		return err
	}
	return s.git.Restore(ctx, l.Path)
}

func (s *Service) leaseOf(ctx context.Context, w *Workspace) {
	if l, err := s.store.GetLease(ctx, w.Path); err == nil {
		w.Lease = toLease(w.ID, l)
	}
}

type SaveRequest struct {
	Lease      *Lease
	Path       string
	Text       string
	LoadedMod  time.Time
	LoadedSize int64
	Overwrite  bool
}

func (s *Service) SaveFile(ctx context.Context, workspaceID string, req SaveRequest) (*FileContent, error) {
	if _, err := s.leaseRoot(ctx, workspaceID); err != nil {
		return nil, err
	}
	if req.Lease == nil || req.Lease.WorkspaceID != workspaceID {
		return nil, fmt.Errorf("save: the worktree is not leased by this Studio: %w", ErrLeaseHeld)
	}
	if err := s.held(ctx, req.Lease); err != nil {
		return nil, err
	}
	path, err := s.ResolvePath(ctx, workspaceID, req.Path)
	if err != nil {
		return nil, err
	}
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
		if !req.Overwrite && (!fi.ModTime().Equal(req.LoadedMod) || fi.Size() != req.LoadedSize) {
			return nil, fmt.Errorf("%s changed on disk since it was opened: %w", req.Path, ErrConflict)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".caffe-*")
	if err != nil {
		return nil, err
	}
	_, werr := tmp.WriteString(req.Text)
	if err := errors.Join(werr, tmp.Sync(), tmp.Close(), os.Chmod(tmp.Name(), mode)); err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return &FileContent{Path: path, Text: req.Text, Size: fi.Size(), ModTime: fi.ModTime()}, nil
}
