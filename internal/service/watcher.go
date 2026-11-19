package service

import (
	"context"
	"sync"
	"time"

	"caffe/internal/persistence"
)

type Change struct {
	Seq int64
	Err error
}

const DefaultWatchInterval = 250 * time.Millisecond

type Watcher struct {
	ch     chan Change
	mu     sync.Mutex
	paused bool
	wake   chan struct{}
}

func (s *Service) Watch(ctx context.Context, interval time.Duration) (*Watcher, error) {
	if interval <= 0 {
		interval = DefaultWatchInterval
	}
	det, err := s.store.ChangeDetector(ctx)
	if err != nil {
		return nil, err
	}
	if _, _, err := det.Changed(ctx); err != nil {
		det.Close()
		return nil, err
	}
	w := &Watcher{ch: make(chan Change, 1), wake: make(chan struct{}, 1)}
	go w.loop(ctx, det, interval)
	return w, nil
}

func (w *Watcher) Changes() <-chan Change { return w.ch }

func (w *Watcher) Pause() {
	w.mu.Lock()
	w.paused = true
	w.mu.Unlock()
}

func (w *Watcher) Resume() {
	w.mu.Lock()
	w.paused = false
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *Watcher) isPaused() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.paused
}

func (w *Watcher) loop(ctx context.Context, det *persistence.ChangeDetector, interval time.Duration) {
	defer close(w.ch)
	defer det.Close()
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-w.wake:
		}
		if w.isPaused() {
			continue
		}
		changed, seq, err := det.Changed(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			w.publish(Change{Err: err})
		case changed:
			w.publish(Change{Seq: seq})
		}
	}
}

func (w *Watcher) publish(c Change) {
	for {
		select {
		case w.ch <- c:
			return
		default:
		}
		select {
		case <-w.ch:
		default:
		}
	}
}
