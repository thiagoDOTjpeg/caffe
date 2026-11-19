package studio

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rs/zerolog"

	c "caffe/internal/contracts"
	"caffe/internal/service"
)

// fakeSource serves a run and an event log that tests change between loads.
type fakeSource struct {
	mu     sync.Mutex
	runs   []service.RunSummary
	snaps  map[string]*service.RunView
	events []c.Event
	err    error
	afters []int64 // EventsSince calls

	report    string
	started   []service.StartRunRequest
	calls     []string
	cmdErr    error
	output    map[string]*service.AgentOutput
	outErr    error
	rtObs     map[string]service.RuntimeObservation
	outCalls  int
	attachArg []string // nil: the runtime cannot attach
	execs     map[string]*service.ExecutionDetail
	prompts   map[string]string // execution id → prompt text
	artifacts map[string]string // artifact id → text
}

func (f *fakeSource) Execution(_ context.Context, id string) (*service.ExecutionDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.execs[id]; ok {
		return d, nil
	}
	return nil, service.ErrNotFound
}

func (f *fakeSource) ReadPrompt(_ context.Context, id string, _ int64) (*service.ArtifactContent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.prompts[id]; ok {
		return &service.ArtifactContent{Text: p, Size: int64(len(p))}, nil
	}
	return nil, service.ErrNotFound
}

func (f *fakeSource) ReadArtifact(_ context.Context, id string, max int64) (*service.ArtifactContent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.artifacts[id]; ok {
		return &service.ArtifactContent{Text: t[:min(int64(len(t)), max)], Size: int64(len(t)), Truncated: int64(len(t)) > max}, nil
	}
	return nil, service.ErrNotFound
}

func (f *fakeSource) Runs(context.Context, bool) ([]service.RunSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs, f.err
}

func (f *fakeSource) RunSnapshot(_ context.Context, id string) (*service.RunView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snaps[id], f.err
}

func (f *fakeSource) EventsSince(_ context.Context, after int64, _ string, limit int) ([]c.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.afters = append(f.afters, after)
	var out []c.Event
	for _, e := range f.events {
		if e.Seq > after && len(out) < limit {
			out = append(out, e)
		}
	}
	return out, f.err
}

func (f *fakeSource) Capabilities(context.Context) (*service.RuntimeInfo, error) {
	return &service.RuntimeInfo{Version: "0.9.5"}, nil
}

func (f *fakeSource) EventsBefore(_ context.Context, before int64, runID string, limit int) ([]c.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []c.Event
	for i := len(f.events) - 1; i >= 0 && len(out) < limit; i-- {
		e := f.events[i]
		if (before == 0 || e.Seq < before) && (runID == "" || e.RunID == runID) {
			out = append([]c.Event{e}, out...)
		}
	}
	return out, f.err
}

func (f *fakeSource) Report(_ context.Context, runID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.report == "" {
		return "", service.ErrNotFound
	}
	return f.report, nil
}

func (f *fakeSource) StartRun(_ context.Context, req service.StartRunRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, req)
	if f.cmdErr != nil {
		return "", f.cmdErr
	}
	id := "newrun00-1"
	r := service.RunSummary{ID: id, Request: req.Request, Status: c.RunPlanning}
	f.runs = append([]service.RunSummary{r}, f.runs...)
	f.snaps[id] = &service.RunView{Run: r}
	return id, nil
}

func (f *fakeSource) ResumeRun(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "resume "+id)
	return f.cmdErr
}

func (f *fakeSource) CancelRun(_ context.Context, id string) (*service.RunSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "cancel "+id)
	if f.cmdErr != nil {
		return nil, f.cmdErr
	}
	for i := range f.runs {
		if f.runs[i].ID == id {
			f.runs[i].Status = c.RunCancelled
			if s := f.snaps[id]; s != nil {
				s.Run.Status = c.RunCancelled
			}
			return &f.runs[i], nil
		}
	}
	return nil, service.ErrNotFound
}

func (f *fakeSource) Clean(_ context.Context, includeFailed bool) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("clean %v", includeFailed))
	return []string{"removed /wt/a (branch caffe/x/a kept)", "kept    /wt/b (run failed, task b failed; use --failed to remove)"}, f.cmdErr
}

func (f *fakeSource) AgentOutput(_ context.Context, id string, _ int) (*service.AgentOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outCalls++
	if f.outErr != nil {
		return nil, f.outErr
	}
	if o, ok := f.output[id]; ok {
		return o, nil
	}
	return &service.AgentOutput{AgentID: id, Source: service.OutputNone}, nil
}

func (f *fakeSource) AgentRuntimeStatus(_ context.Context, agents []service.AgentView) map[string]service.RuntimeObservation {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]service.RuntimeObservation{}
	for _, a := range agents {
		if o, ok := f.rtObs[a.ID]; ok {
			out[a.ID] = o
		}
	}
	return out
}

func (f *fakeSource) CanAttach() bool { return f.attachArg != nil }

func (f *fakeSource) AttachAgent(_ context.Context, id string) (*exec.Cmd, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "attach "+id)
	if f.cmdErr != nil {
		return nil, f.cmdErr
	}
	return exec.Command(f.attachArg[0], f.attachArg[1:]...), nil
}

func (f *fakeSource) set(fn func(f *fakeSource)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func newFake() *fakeSource {
	run := service.RunSummary{ID: "run-active-1", Request: "add login", Status: c.RunExecuting}
	return &fakeSource{
		runs: []service.RunSummary{{ID: "run-done-0", Status: c.RunCompleted}, run},
		snaps: map[string]*service.RunView{"run-active-1": {Run: run, Seq: 2,
			Tasks: []service.TaskView{{Task: c.Task{ID: "t1", Key: "api"}}}}},
		events: []c.Event{
			{Seq: 1, RunID: "run-active-1", TaskID: "t1", Type: "task.transition"},
			{Seq: 2, RunID: "run-done-0", Type: "run.transition"},
		},
	}
}

func shrinkTimings(t *testing.T) {
	d, lo, hi := debounce, backoffMin, backoffMax
	debounce, backoffMin, backoffMax = time.Millisecond, time.Millisecond, 4*time.Millisecond
	t.Cleanup(func() { debounce, backoffMin, backoffMax = d, lo, hi })
}

// drive runs cmd and every command it leads to, as Bubble Tea does (each in
// its own goroutine, results to Update one at a time), until nothing arrives
// for a quiet period: the model has settled. Long timers (toast expiry, the
// health and live-agent ticks) fire after it and are dropped.
func drive(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	const quiet = 50 * time.Millisecond // fake sources answer in microseconds
	msgs := make(chan tea.Msg, 256)
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			msg := c()
			if batch, ok := msg.(tea.BatchMsg); ok {
				for _, b := range batch {
					run(b)
				}
				return
			}
			if msg != nil {
				msgs <- msg
			}
		}()
	}
	run(cmd)
	for steps := 0; ; steps++ {
		if steps > 200 {
			t.Fatal("model never settled")
		}
		select {
		case msg := <-msgs:
			res, more := m.Update(msg)
			m = res.(model)
			run(more)
		case <-time.After(quiet):
			return m
		}
	}
}

func liveModel(src source) model {
	return newModel(src, Project{Name: "demo", Root: "/p"}, &crashGuard{log: zerolog.Nop()})
}

func TestInitialLoadSelectsActiveRunAndFeed(t *testing.T) {
	m := liveModel(newFake())
	m = drive(t, m, m.Init())
	if m.live.runID != "run-active-1" || m.live.snap == nil {
		t.Fatalf("selected %q, snapshot %v", m.live.runID, m.live.snap)
	}
	if m.live.lastSeq != 2 || len(m.live.feed) != 2 {
		t.Errorf("lastSeq %d, feed %d", m.live.lastSeq, len(m.live.feed))
	}
	lines := m.live.feedLines(10)
	if len(lines) != 1 || !strings.Contains(lines[0], "task.transition   api") {
		t.Errorf("feed of the selected run: %q", lines)
	}
}

func TestChangeRefreshesAndDedupes(t *testing.T) {
	shrinkTimings(t)
	f := newFake()
	m := liveModel(f)
	m = drive(t, m, m.Init())
	f.set(func(f *fakeSource) {
		f.snaps["run-active-1"].Run.Status = c.RunReviewing
		f.events = append(f.events, c.Event{Seq: 3, RunID: "run-active-1", Type: "task.transition"})
	})
	start := time.Now()
	m = drive(t, m, func() tea.Msg { return changeMsg{Seq: 3} })
	if got := m.live.snap.Run.Status; got != c.RunReviewing {
		t.Errorf("status %s after change", got)
	}
	if time.Since(start) > time.Second {
		t.Error("transition took over 1 s")
	}
	if len(m.live.feed) != 3 || f.afters[len(f.afters)-1] != 2 {
		t.Errorf("feed %d, last EventsSince after %v", len(m.live.feed), f.afters)
	}
	// A replayed event (Seq ≤ lastSeq) is dropped.
	res, _ := m.Update(loadedMsg{runs: f.runs, runID: m.live.runID, snap: m.live.snap, events: f.events})
	if n := len(res.(model).live.feed); n != 3 {
		t.Errorf("duplicates kept: feed %d", n)
	}
}

func TestChangeWhilePendingIsNotLost(t *testing.T) {
	m := liveModel(newFake())
	m.live.pending = true // a load is in flight
	res, cmd := m.Update(changeMsg{Seq: 9})
	m = res.(model)
	if !m.live.dirty || cmd != nil {
		t.Fatalf("dirty %v, cmd %v", m.live.dirty, cmd != nil)
	}
	res, cmd = m.Update(loadedMsg{})
	if m = res.(model); m.live.dirty || !m.live.pending || cmd == nil {
		t.Errorf("no follow-up refresh: dirty %v pending %v", m.live.dirty, m.live.pending)
	}
}

func TestFailureKeepsDataMarksStaleAndBacksOff(t *testing.T) {
	m := liveModel(newFake())
	m = drive(t, m, m.Init())
	good := m.live.snap
	at := time.Date(2026, 11, 18, 10, 0, 0, 0, time.Local)
	var want []time.Duration
	for i := range 7 {
		res, cmd := m.Update(loadedMsg{at: at.Add(time.Duration(i) * time.Minute), err: errors.New("database is locked")})
		m = res.(model)
		if cmd == nil {
			t.Fatal("no retry scheduled")
		}
		want = append(want, m.live.backoff)
	}
	if got := want; got[0] != time.Second || got[1] != 2*time.Second || got[6] != 30*time.Second {
		t.Errorf("backoff %v", got)
	}
	if m.live.snap != good {
		t.Error("last good snapshot dropped")
	}
	if s := m.live.staleLine(); s != "⚠ stale since 10:00:00 — database is locked" {
		t.Errorf("banner %q", s)
	}
	m.width, m.height = 120, 40
	if v := m.View().Content; !strings.Contains(v, "stale since 10:00:00") || !strings.Contains(v, "add login") {
		t.Error("view lacks the stale banner or the last good data")
	}
}

func TestRecoveryClearsStale(t *testing.T) {
	shrinkTimings(t)
	f := newFake()
	f.err = errors.New("disk I/O error")
	m := liveModel(f)
	res, cmd := m.Update(loadedMsg{err: f.err}) // first failure schedules a retry
	m = res.(model)
	f.set(func(f *fakeSource) { f.err = nil })
	m = drive(t, m, cmd)
	if m.live.stale() || m.live.backoff != 0 || m.live.snap == nil {
		t.Errorf("stale %v backoff %v snap %v", m.live.stale(), m.live.backoff, m.live.snap)
	}
}

func TestWatcherErrorMarksStale(t *testing.T) {
	m := liveModel(newFake())
	res, _ := m.Update(changeMsg{Err: errors.New("database is locked")})
	if !res.(model).live.stale() {
		t.Error("watcher error not shown as stale")
	}
}

func TestFeedIsBounded(t *testing.T) {
	var l live
	evs := make([]c.Event, feedCap+10)
	for i := range evs {
		evs[i] = c.Event{Seq: int64(i + 1)}
	}
	l.apply(loadedMsg{events: evs})
	if len(l.feed) != feedCap || l.feed[0].Seq != 11 || l.lastSeq != int64(feedCap+10) {
		t.Errorf("feed %d from seq %d, lastSeq %d", len(l.feed), l.feed[0].Seq, l.lastSeq)
	}
}

func TestFirstLoadStartsNearTheHead(t *testing.T) {
	f := newFake()
	f.snaps["run-active-1"].Seq = 10_000
	m := liveModel(f)
	m = drive(t, m, m.Init())
	if f.afters[0] != 10_000-feedCap {
		t.Errorf("first EventsSince after %d", f.afters[0])
	}
}

func TestChooseRun(t *testing.T) {
	runs := []service.RunSummary{{ID: "c", Status: c.RunCompleted}, {ID: "i", Status: c.RunInterrupted}, {ID: "e", Status: c.RunExecuting}}
	for _, tc := range []struct{ current, want string }{{"", "e"}, {"c", "c"}, {"gone", "e"}} {
		if got := chooseRun(runs, tc.current); got != tc.want {
			t.Errorf("chooseRun(%q) = %q, want %q", tc.current, got, tc.want)
		}
	}
	if got := chooseRun(runs[:2], ""); got != "c" {
		t.Errorf("no active run: %q", got)
	}
}
