package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"

	c "caffe/internal/contracts"
	"caffe/internal/persistence"
)

type TaskFunc func(ctx context.Context, t *c.Task) error

type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

func Permanent(err error) error { return &permanentError{err} }

type Scheduler struct {
	store   *persistence.Store
	graph   *Graph
	limit   int
	timeout time.Duration
	exec    TaskFunc
	log     zerolog.Logger

	OnChange func(t c.Task, from c.TaskStatus, reason string)

	OnActive func(active map[c.Role]int)

	mu sync.Mutex
}

func NewScheduler(store *persistence.Store, graph *Graph, limit int, timeout time.Duration, exec TaskFunc, log zerolog.Logger) *Scheduler {
	return &Scheduler{store: store, graph: graph, limit: limit, timeout: timeout, exec: exec, log: log}
}

func setMutable(dst, src *c.Task) {
	dst.Status, dst.AssignedAgentID, dst.WorktreePath, dst.BranchName = src.Status, src.AssignedAgentID, src.WorktreePath, src.BranchName
	dst.Attempts, dst.StartedAt, dst.CompletedAt, dst.Error = src.Attempts, src.StartedAt, src.CompletedAt, src.Error
}

func (s *Scheduler) Snapshot(t *c.Task) c.Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	return *t
}

func (s *Scheduler) Transition(ctx context.Context, t *c.Task, to c.TaskStatus, reason string, mut func(*c.Task)) error {
	cur := s.Snapshot(t)
	next, err := transition(ctx, s.store, cur, to, reason, mut)
	if err != nil {
		return err
	}
	s.mu.Lock()
	setMutable(t, &next)
	s.mu.Unlock()
	if s.OnChange != nil {
		s.OnChange(next, cur.Status, reason)
	}
	return nil
}

type result struct {
	task *c.Task
	err  error
}

func (s *Scheduler) Run(ctx context.Context) error {
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	db := context.WithoutCancel(ctx)

	results := make(chan result, s.limit)
	var wg sync.WaitGroup
	active := map[c.Role]int{}
	running := 0
	var fatal error
	fail := func(err error) {
		if fatal == nil {
			fatal = err
			stop()
		}
	}

	for {
		if ctx.Err() == nil {
			if err := s.promote(db); errors.Is(err, persistence.ErrConflict) {
				if err := s.refresh(db); err != nil {
					fail(err)
				}
			} else if err != nil {
				fail(err)
			}
			for ctx.Err() == nil && running < s.limit {
				t, queued := s.nextReady()
				if t == nil {
					break
				}
				err := s.Transition(db, t, c.TaskAssigned, "", func(t *c.Task) { t.Attempts++ })
				if errors.Is(err, persistence.ErrConflict) {

					if err := s.refresh(db); err != nil {
						fail(err)
					}
					continue
				}
				if err != nil {
					fail(err)
					break
				}
				running++
				active[t.Role]++
				s.log.Debug().Str("task_id", t.ID).Str("task", t.Key).Int("queue_size", queued-1).Int("concurrent_agents", running).Msg("task dispatched")
				wg.Add(1)
				go s.work(ctx, t, results, &wg)
			}
			if s.OnActive != nil {
				s.OnActive(active)
			}
		}
		if running == 0 {
			break
		}
		r := <-results
		running--
		active[r.task.Role]--
		if err := s.settle(db, ctx, r); err != nil {
			fail(err)
		}
	}
	wg.Wait()

	if ctx.Err() != nil {
		for _, t := range s.graph.Tasks() {
			if st := s.Snapshot(t).Status; st == c.TaskPending || st == c.TaskReady {
				if err := s.Transition(db, t, c.TaskCancelled, "run stopped before the task started", nil); err != nil && fatal == nil {
					fatal = err
				}
			}
		}
	}
	return fatal
}

func (s *Scheduler) work(ctx context.Context, t *c.Task, results chan<- result, wg *sync.WaitGroup) {
	defer wg.Done()
	tctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	var err error
	func() {
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("task handler panicked: %v", p)
			}
		}()
		err = s.exec(tctx, t)
	}()
	if err != nil && errors.Is(tctx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
		err = fmt.Errorf("task timed out after %s: %w", s.timeout, err)
	}
	results <- result{t, err}
}

func (s *Scheduler) promote(ctx context.Context) error {
	s.mu.Lock()
	ready := s.graph.Promotable()
	doomed := s.graph.Doomed()
	s.mu.Unlock()
	for _, t := range ready {
		if err := s.Transition(ctx, t, c.TaskReady, "dependencies completed", nil); err != nil {
			return err
		}
	}
	for t, dep := range doomed {
		if err := s.Transition(ctx, t, c.TaskCancelled, fmt.Sprintf("dependency %s %s", dep.Key, dep.Status), nil); err != nil {
			return err
		}
	}
	if len(doomed) > 0 {
		return s.promote(ctx)
	}
	return nil
}

func (s *Scheduler) nextReady() (*c.Task, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var first *c.Task
	n := 0
	for _, t := range s.graph.Tasks() {
		if t.Status == c.TaskReady {
			if first == nil {
				first = t
			}
			n++
		}
	}
	return first, n
}

func (s *Scheduler) settle(db, run context.Context, r result) error {
	t := r.task
	cur := s.Snapshot(t)
	var err error
	switch {
	case r.err == nil:
		if cur.Status == c.TaskAssigned || cur.Status == c.TaskBlocked {
			if err = s.Transition(db, t, c.TaskRunning, "", nil); err != nil {
				break
			}
		}
		err = s.Transition(db, t, c.TaskCompleted, "", nil)
	case run.Err() != nil:
		err = s.Transition(db, t, c.TaskCancelled, "run stopped: "+r.err.Error(), nil)
	default:
		if err = s.Transition(db, t, c.TaskFailed, r.err.Error(), nil); err != nil {
			break
		}
		var perm *permanentError
		if !errors.As(r.err, &perm) && cur.Attempts < cur.MaxAttempts {
			err = s.Transition(db, t, c.TaskReady, fmt.Sprintf("retry after attempt %d of %d", cur.Attempts, cur.MaxAttempts), nil)
		}
	}
	if errors.Is(err, persistence.ErrConflict) {
		s.log.Warn().Err(err).Str("task", t.Key).Msg("task changed outside the scheduler; reloading")
		return s.refresh(db)
	}
	return err
}

func (s *Scheduler) refresh(ctx context.Context) error {
	tasks := s.graph.Tasks()
	if len(tasks) == 0 {
		return nil
	}
	fresh, err := s.store.ListTasks(ctx, tasks[0].RunID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range fresh {
		if t := s.graph.byID[f.ID]; t != nil {
			setMutable(t, f)
		}
	}
	return nil
}
