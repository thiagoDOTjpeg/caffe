package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	c "caffe/internal/contracts"
	"caffe/internal/persistence"
)

type fakeEngine struct {
	cancelErr error
	cancelled []string
	cleaned   []bool
}

func (e *fakeEngine) Cancel(_ context.Context, id string) (*c.Run, error) {
	e.cancelled = append(e.cancelled, id)
	if errors.Is(e.cancelErr, persistence.ErrConflict) {
		return nil, e.cancelErr
	}
	return &c.Run{ID: id, Status: c.RunCancelled}, e.cancelErr
}

func (e *fakeEngine) Clean(_ context.Context, includeFailed bool) ([]string, error) {
	e.cleaned = append(e.cleaned, includeFailed)
	return []string{"removed /wt/impl (branch caffe/x/impl kept)"}, nil
}

func TestCancelRunAndClean(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	if _, err := f.svc.CancelRun(ctx, "1111"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("no engine: %v", err)
	}
	e := &fakeEngine{}
	f.svc.WithEngine(e)
	sum, err := f.svc.CancelRun(ctx, "1111") // prefix
	if err != nil || sum.Status != c.RunCancelled || e.cancelled[0] != "11111111-0000-0000-0000-000000000000" {
		t.Fatalf("cancel: %+v %v %v", sum, err, e.cancelled)
	}
	// Lost the race to another writer: a conflict, never a retry.
	e.cancelErr = fmt.Errorf("run x is not executing: %w", persistence.ErrConflict)
	if _, err := f.svc.CancelRun(ctx, "1111"); !errors.Is(err, ErrConflict) || len(e.cancelled) != 2 {
		t.Errorf("conflict: %v (calls %d)", err, len(e.cancelled))
	}
	// Already finished: a conflict without calling the engine.
	run, _ := f.w.GetRun(ctx, "1111")
	from := run.Status
	run.Status = c.RunCompleted
	must(t, f.w.UpdateRun(ctx, run, from))
	if _, err := f.svc.CancelRun(ctx, "1111"); !errors.Is(err, ErrConflict) || len(e.cancelled) != 2 {
		t.Errorf("finished run: %v (calls %d)", err, len(e.cancelled))
	}
	if _, err := f.svc.CancelRun(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing run: %v", err)
	}
	if lines, err := f.svc.Clean(ctx, true); err != nil || len(lines) != 1 || !e.cleaned[0] {
		t.Errorf("clean: %v %v", lines, err)
	}
}
