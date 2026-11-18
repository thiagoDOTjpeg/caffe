package studio

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	c "caffe/internal/contracts"
	"caffe/internal/service"
)

// source is the part of service.Service the live state reads; tests use a fake.
type source interface {
	Runs(ctx context.Context, activeOnly bool) ([]service.RunSummary, error)
	RunSnapshot(ctx context.Context, runID string) (*service.RunView, error)
	EventsSince(ctx context.Context, after int64, runID string, limit int) ([]c.Event, error)
	Capabilities(ctx context.Context) (*service.RuntimeInfo, error)
	Execution(ctx context.Context, id string) (*service.ExecutionDetail, error)
	ReadPrompt(ctx context.Context, executionID string, max int64) (*service.ArtifactContent, error)
	ReadArtifact(ctx context.Context, id string, max int64) (*service.ArtifactContent, error)
}

// Timings of architecture §7. Variables so tests can shrink them.
var (
	debounce   = 100 * time.Millisecond
	backoffMin = time.Second
	backoffMax = 30 * time.Second
)

const (
	feedCap  = 2000 // events kept in memory (architecture §5.4)
	feedPage = 500  // events read per refresh; more pages follow at once
)

// live is the database-backed state: runs, the selected run's snapshot and
// the event feed. It keeps its last good value when a refresh fails.
type live struct {
	runs     []service.RunSummary
	runID    string // selected run, kept across refreshes
	snap     *service.RunView
	loaded   bool
	loadedAt time.Time

	err        error // last failure; nil when fresh
	staleSince time.Time
	backoff    time.Duration

	feed    []c.Event // ring of the newest feedCap events, all runs, Seq order
	lastSeq int64

	watchErr error // the watcher could not start: data never refreshes

	pending bool // a debounce tick, retry or load is outstanding
	dirty   bool // a change arrived while pending
}

func (l live) stale() bool { return l.err != nil }

type (
	changeMsg  service.Change
	refreshMsg struct{}
	loadedMsg  struct {
		at     time.Time
		runs   []service.RunSummary
		runID  string
		snap   *service.RunView
		events []c.Event
		more   bool // the event page was full
		err    error
	}
)

// waitChange delivers the next watcher change; Update re-arms it.
func waitChange(ch <-chan service.Change) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ch, ok := <-ch
		if !ok {
			return nil // watcher stopped: Studio is exiting
		}
		return changeMsg(ch)
	}
}

func after(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return refreshMsg{} })
}

// chooseRun keeps the selected run if it still exists, else picks the newest
// active run, else the newest run. runs are newest first.
func chooseRun(runs []service.RunSummary, current string) string {
	for _, r := range runs {
		if r.ID == current {
			return current
		}
	}
	for _, r := range runs {
		if active(r) {
			return r.ID
		}
	}
	if len(runs) > 0 {
		return runs[0].ID
	}
	return ""
}

// load reads everything the live state shows. It runs as a command, off the
// update loop.
func (l live) load(src source) tea.Cmd {
	current, lastSeq, first := l.runID, l.lastSeq, !l.loaded
	return func() tea.Msg {
		ctx := context.Background()
		msg := loadedMsg{at: time.Now()}
		if msg.runs, msg.err = src.Runs(ctx, false); msg.err != nil {
			return msg
		}
		msg.runID = chooseRun(msg.runs, current)
		if msg.runID != "" {
			if msg.snap, msg.err = src.RunSnapshot(ctx, msg.runID); msg.err != nil {
				return msg
			}
			if first { // start the feed near the head, not at the beginning of history
				lastSeq = max(0, msg.snap.Seq-feedCap)
			}
		}
		msg.events, msg.err = src.EventsSince(ctx, lastSeq, "", feedPage)
		msg.more = len(msg.events) == feedPage
		return msg
	}
}

func (m model) updateLive(msg tea.Msg) (model, tea.Cmd) {
	l := &m.live
	switch msg := msg.(type) {
	case changeMsg:
		next := waitChange(m.changes)
		if msg.Err != nil {
			l.fail(msg.Err, time.Now())
			return m, next
		}
		if l.pending {
			l.dirty = true
			return m, next
		}
		l.pending = true
		return m, tea.Batch(next, after(debounce))
	case refreshMsg:
		return m, l.load(m.src)
	case loadedMsg:
		l.pending = false
		if msg.err != nil {
			l.fail(msg.err, msg.at)
			l.backoff = min(max(2*l.backoff, backoffMin), backoffMax)
			l.pending = true
			return m, after(l.backoff)
		}
		l.apply(msg)
		if msg.more || l.dirty {
			l.dirty, l.pending = false, true
			if msg.more {
				return m, l.load(m.src)
			}
			return m, after(debounce)
		}
	}
	return m, nil
}

func (l *live) fail(err error, at time.Time) {
	if l.err == nil {
		l.staleSince = at
	}
	l.err = err
}

func (l *live) apply(msg loadedMsg) {
	l.runs, l.runID, l.snap = msg.runs, msg.runID, msg.snap
	l.loaded, l.loadedAt = true, msg.at
	l.err, l.backoff = nil, 0
	for _, e := range msg.events {
		if e.Seq <= l.lastSeq {
			continue // already seen
		}
		l.feed = append(l.feed, e)
		l.lastSeq = e.Seq
	}
	if n := len(l.feed); n > feedCap {
		l.feed = append([]c.Event(nil), l.feed[n-feedCap:]...)
	}
}

// staleLine is the banner shown while data is not fresh.
func (l live) staleLine() string {
	if !l.stale() {
		if l.watchErr != nil {
			return fmt.Sprintf("⚠ not updating — %v", l.watchErr)
		}
		return ""
	}
	return fmt.Sprintf("⚠ stale since %s — %v", l.staleSince.Format("15:04:05"), l.err)
}

// feedLines renders the newest n events of the selected run, oldest first.
func (l live) feedLines(n int) []string {
	if n <= 0 {
		return nil
	}
	keys := map[string]string{}
	if l.snap != nil {
		for _, t := range l.snap.Tasks {
			keys[t.ID] = t.Key
		}
	}
	var out []string
	for i := len(l.feed) - 1; i >= 0 && len(out) < n; i-- {
		e := l.feed[i]
		if e.RunID != l.runID {
			continue
		}
		line := fmt.Sprintf("  %s  %-18s", e.CreatedAt.Local().Format("15:04:05"), e.Type)
		if k := keys[e.TaskID]; k != "" {
			line += k
		}
		out = append(out, line)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
