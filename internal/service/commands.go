package service

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	c "caffe/internal/contracts"
	"caffe/internal/persistence"
	"caffe/internal/runtime"
)

var ErrConflict = errors.New("state changed")

var ErrUnsupported = errors.New("unsupported")

type Engine interface {
	Cancel(ctx context.Context, idOrPrefix string) (*c.Run, error)
	Clean(ctx context.Context, includeFailed bool) ([]string, error)
}

func (s *Service) WithEngine(e Engine) *Service {
	s.engine = e
	return s
}

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
	return &sum, err
}

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

func (s *Service) CanAttach() bool {
	_, ok := s.rt.(runtime.Attacher)
	return ok
}

func (s *Service) AttachAgent(ctx context.Context, agentID string) (*exec.Cmd, error) {
	at, ok := s.rt.(runtime.Attacher)
	if !ok {
		return nil, fmt.Errorf("attach: the runtime cannot attach to agents: %w", ErrUnsupported)
	}
	a, err := s.store.GetAgent(ctx, agentID)
	if err != nil {
		return nil, notFound(err)
	}
	if a.Status == "stopped" || a.Name == "" {
		return nil, fmt.Errorf("agent %s has stopped: %w", short8(a.ID), ErrConflict)
	}
	argv, err := at.AttachCommand(a.Name)
	if err != nil {
		return nil, err
	}
	return exec.Command(argv[0], argv[1:]...), nil
}
