package service

import (
	"context"
	"sync"
	"time"

	"caffe/internal/persistence"
)

// Change says that persisted state changed, not what changed: the consumer
// re-reads (EventsSince from its last Seq, RunSnapshot of what it shows).
// Err is set instead when the database could not be checked; the consumer
// marks its data stale and waits for the next Change.
type Change struct {
	Seq int64 // highest event Seq when the change was seen
	Err error
}

// DefaultWatchInterval is how often the watcher checks for commits. Each check
// costs ~15 µs (ADR-002), so this is a latency choice, not a cost one.
const DefaultWatchInterval = 250 * time.Millisecond

// Watcher reports commits made by other connections (the engine's run
// processes) without reading data. See ADR-002.
type Watcher struct {
	ch     chan Change
	mu     sync.Mutex
	paused bool
	wake   chan struct{}
}

// Watch starts a watcher; it stops and closes Changes() when ctx is done.
func (s *Service) Watch(ctx context.Context, interval time.Duration) (*Watcher, error) {
	if interval <= 0 {
		interval = DefaultWatchInterval
	}
	det, err := s.store.ChangeDetector(ctx)
	if err != nil {
		return nil, err
	}
	if _, _, err := det.Changed(ctx); err != nil { // baseline
		det.Close()
		return nil, err
	}
	w := &Watcher{ch: make(chan Change, 1), wake: make(chan struct{}, 1)}
	go w.loop(ctx, det, interval)
	return w, nil
}

// Changes delivers at most one pending Change: if the consumer is behind,
// the pending one is replaced by the newest, since each means "re-read".
func (w *Watcher) Changes() <-chan Change { return w.ch }

// Pause stops checking (e.g. while Studio hands the terminal to an editor).
// Commits made meanwhile are reported once on Resume.
func (w *Watcher) Pause() {
	w.mu.Lock()
	w.paused = true
	w.mu.Unlock()
}

// Resume restarts checking immediately.
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

// publish never blocks: a pending, unread Change is replaced by c.
func (w *Watcher) publish(c Change) {
	for {
		select {
		case w.ch <- c:
			return
		default:
		}
		select {
		case <-w.ch: // drop the stale one
		default:
		}
	}
}
