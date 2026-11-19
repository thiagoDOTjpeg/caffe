package service

import (
	"context"
	"errors"
	"fmt"

	c "caffe/internal/contracts"
	"caffe/internal/persistence"
)

// Commands that change state go through the engine (architecture §4). New
// and resumed runs are separate processes (launcher.go); cancel and clean
// are short writes done by the engine in the caller's process.

// ErrConflict means the state changed underneath the caller (someone else
// acted first); the caller refreshes and tells the user, never retries.
var ErrConflict = errors.New("state changed")

// ErrUnsupported means the service has no engine to perform a command.
var ErrUnsupported = errors.New("unsupported")

// Engine is what the service needs from core.Orchestrator to change state.
type Engine interface {
	Cancel(ctx context.Context, idOrPrefix string) (*c.Run, error)
	Clean(ctx context.Context, includeFailed bool) ([]string, error)
}

// WithEngine lets the service perform commands through e.
func (s *Service) WithEngine(e Engine) *Service {
	s.engine = e
	return s
}

// CancelRun cancels a run that is not finished and stops its agents.
func (s *Service) CancelRun(ctx context.Context, runID string) (*RunSummary, error) {
	if s.engine == nil {
		return nil, fmt.Errorf("cancel: %w", ErrUnsupported)
	}
	r, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return nil, notFound(err)
	}
	if r.Status.Terminal() {
		return nil, fmt.Errorf("run %s is already %s: %w", short8(r.ID), r.Status, ErrConflict)
	}
	run, err := s.engine.Cancel(ctx, r.ID)
	if errors.Is(err, persistence.ErrConflict) {
		return nil, fmt.Errorf("run %s changed meanwhile: %w", short8(r.ID), ErrConflict)
	}
	if run == nil {
		return nil, err
	}
	sum := s.summary(run)
	return &sum, err // the run is cancelled even when stopping an agent failed
}

// Clean removes the worktrees of finished runs; see core.Orchestrator.Clean.
// It returns one line per worktree removed or kept.
func (s *Service) Clean(ctx context.Context, includeFailed bool) ([]string, error) {
	if s.engine == nil {
		return nil, fmt.Errorf("clean: %w", ErrUnsupported)
	}
	return s.engine.Clean(ctx, includeFailed)
}

func short8(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
