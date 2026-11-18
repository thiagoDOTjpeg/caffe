package core

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	c "caffe/internal/contracts"
	"caffe/internal/persistence"
)

func testStore(t *testing.T) *persistence.Store {
	t.Helper()
	s, err := persistence.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// seedTasks stores a run with tasks described as "key:dep1,dep2".
func seedTasks(t *testing.T, s *persistence.Store, maxAttempts int, specs ...string) []*c.Task {
	t.Helper()
	ctx := context.Background()
	run := &c.Run{ID: "run", ProjectPath: "/p", UserRequest: "r", Status: c.RunExecuting, CreatedAt: persistence.Now()}
	if err := s.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	var tasks []*c.Task
	for _, spec := range specs {
		key, deps, _ := strings.Cut(spec, ":")
		task := &c.Task{ID: key, RunID: run.ID, Key: key, Title: key, Description: key, Role: c.RoleResearcher,
			Status: c.TaskPending, AcceptanceCriteria: []string{"x"}, MaxAttempts: maxAttempts, CreatedAt: persistence.Now()}
		if deps != "" {
			task.Dependencies = strings.Split(deps, ",")
		}
		tasks = append(tasks, task)
	}
	if err := s.CreateTasks(ctx, tasks); err != nil {
		t.Fatal(err)
	}
	return tasks
}

func newSched(t *testing.T, s *persistence.Store, tasks []*c.Task, limit int, exec TaskFunc) *Scheduler {
	t.Helper()
	g, err := NewGraph(tasks)
	if err != nil {
		t.Fatal(err)
	}
	return NewScheduler(s, g, limit, time.Minute, exec, zerolog.Nop())
}

func statuses(tasks []*c.Task) map[string]c.TaskStatus {
	out := map[string]c.TaskStatus{}
	for _, t := range tasks {
		out[t.Key] = t.Status
	}
	return out
}

func TestTransitionsTable(t *testing.T) {
	allowed := [][2]c.TaskStatus{
		{c.TaskPending, c.TaskReady}, {c.TaskReady, c.TaskAssigned}, {c.TaskAssigned, c.TaskRunning},
		{c.TaskRunning, c.TaskCompleted}, {c.TaskRunning, c.TaskBlocked}, {c.TaskRunning, c.TaskFailed},
		{c.TaskBlocked, c.TaskReady}, {c.TaskBlocked, c.TaskRunning}, {c.TaskFailed, c.TaskReady},
		{c.TaskPending, c.TaskCancelled}, {c.TaskReady, c.TaskCancelled}, {c.TaskRunning, c.TaskCancelled},
		{c.TaskCompleted, c.TaskReviewing}, {c.TaskReviewing, c.TaskCompleted},
	}
	for _, p := range allowed {
		if !CanTransition(p[0], p[1]) {
			t.Errorf("%s -> %s must be allowed", p[0], p[1])
		}
	}
	forbidden := [][2]c.TaskStatus{
		{c.TaskPending, c.TaskRunning}, {c.TaskPending, c.TaskCompleted}, {c.TaskReady, c.TaskRunning},
		{c.TaskCompleted, c.TaskRunning}, {c.TaskCompleted, c.TaskReady}, {c.TaskCancelled, c.TaskReady},
		{c.TaskFailed, c.TaskCompleted}, {c.TaskRunning, c.TaskReady}, {c.TaskCancelled, c.TaskRunning},
	}
	for _, p := range forbidden {
		if CanTransition(p[0], p[1]) {
			t.Errorf("%s -> %s must be rejected", p[0], p[1])
		}
	}
}

func TestInvalidTransitionIsNotPersisted(t *testing.T) {
	s := testStore(t)
	tasks := seedTasks(t, s, 1, "a")
	_, err := transition(context.Background(), s, *tasks[0], c.TaskCompleted, "", nil)
	if err == nil || !strings.Contains(err.Error(), "invalid transition") {
		t.Fatalf("want invalid transition, got %v", err)
	}
	stored, _ := s.ListTasks(context.Background(), "run")
	if stored[0].Status != c.TaskPending {
		t.Fatalf("status changed to %s", stored[0].Status)
	}
}

func TestGraphRejectsCyclesAndDanglingDeps(t *testing.T) {
	mk := func(key string, deps ...string) *c.Task { return &c.Task{ID: key, Key: key, Dependencies: deps} }
	if _, err := NewGraph([]*c.Task{mk("a", "c"), mk("b", "a"), mk("c", "b")}); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle not detected: %v", err)
	}
	if _, err := NewGraph([]*c.Task{mk("a", "ghost")}); err == nil {
		t.Fatal("dangling dependency not detected")
	}
	g, err := NewGraph([]*c.Task{mk("a"), mk("b", "a"), mk("c", "a"), mk("d", "b", "c")})
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range g.Tasks() {
		task.Status = c.TaskPending
	}
	if p := g.Promotable(); len(p) != 1 || p[0].Key != "a" {
		t.Fatalf("promotable = %v", p)
	}
	g.byID["a"].Status = c.TaskCompleted
	if p := g.Promotable(); len(p) != 2 {
		t.Fatalf("after a: promotable = %d, want b and c", len(p))
	}
	if w := g.Waiting(g.byID["d"]); len(w) != 2 {
		t.Fatalf("d waits on %d tasks, want 2", len(w))
	}
}

func TestDependenciesAndBoundedConcurrency(t *testing.T) {
	s := testStore(t)
	// a and b are independent; c..g fan out from a; z needs everything.
	tasks := seedTasks(t, s, 1, "a", "b", "c:a", "d:a", "e:a", "f:a", "g:a", "z:b,c,d,e,f,g")
	var mu sync.Mutex
	done := map[string]bool{}
	var cur, peak int32
	sched := newSched(t, s, tasks, 3, func(ctx context.Context, task *c.Task) error {
		n := atomic.AddInt32(&cur, 1)
		for {
			if p := atomic.LoadInt32(&peak); n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		mu.Lock()
		for _, d := range task.Dependencies {
			if !done[d] {
				t.Errorf("%s started before its dependency %s completed", task.Key, d)
			}
		}
		mu.Unlock()
		time.Sleep(15 * time.Millisecond)
		mu.Lock()
		done[task.Key] = true
		mu.Unlock()
		atomic.AddInt32(&cur, -1)
		return nil
	})
	if err := sched.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	for k, st := range statuses(tasks) {
		if st != c.TaskCompleted {
			t.Errorf("%s = %s", k, st)
		}
	}
	if peak != 3 {
		t.Errorf("peak concurrency = %d, want exactly the limit 3 (independent tasks must run concurrently, never above the limit)", peak)
	}
	// What is in memory must be what is stored.
	stored, _ := s.ListTasks(context.Background(), "run")
	for _, st := range stored {
		if st.Status != c.TaskCompleted || st.Attempts != 1 || st.CompletedAt == nil {
			t.Errorf("stored %s: status=%s attempts=%d", st.Key, st.Status, st.Attempts)
		}
	}
}

func TestRetryThenSuccess(t *testing.T) {
	s := testStore(t)
	tasks := seedTasks(t, s, 3, "a", "b:a")
	var calls int32
	sched := newSched(t, s, tasks, 2, func(ctx context.Context, task *c.Task) error {
		if task.Key == "a" && atomic.AddInt32(&calls, 1) < 3 {
			return errors.New("flaky")
		}
		return nil
	})
	if err := sched.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tasks[0].Status != c.TaskCompleted || tasks[0].Attempts != 3 || tasks[1].Status != c.TaskCompleted {
		t.Fatalf("a=%s attempts=%d b=%s", tasks[0].Status, tasks[0].Attempts, tasks[1].Status)
	}
}

func TestExhaustedRetriesCancelDependents(t *testing.T) {
	s := testStore(t)
	tasks := seedTasks(t, s, 2, "a", "b:a", "c:b", "free")
	var calls int32
	sched := newSched(t, s, tasks, 2, func(ctx context.Context, task *c.Task) error {
		if task.Key == "a" {
			atomic.AddInt32(&calls, 1)
			return errors.New("always broken")
		}
		return nil
	})
	if err := sched.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := statuses(tasks)
	if got["a"] != c.TaskFailed || got["b"] != c.TaskCancelled || got["c"] != c.TaskCancelled || got["free"] != c.TaskCompleted {
		t.Fatalf("statuses = %v", got)
	}
	if calls != 2 {
		t.Fatalf("a ran %d times, want MaxAttempts=2", calls)
	}
	if !strings.Contains(tasks[1].Error, "dependency a failed") {
		t.Fatalf("b must say why it was cancelled: %q", tasks[1].Error)
	}
}

func TestPermanentFailureIsNotRetried(t *testing.T) {
	s := testStore(t)
	tasks := seedTasks(t, s, 5, "a")
	var calls int32
	sched := newSched(t, s, tasks, 1, func(context.Context, *c.Task) error {
		atomic.AddInt32(&calls, 1)
		return Permanent(errors.New("review rejected"))
	})
	if err := sched.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || tasks[0].Status != c.TaskFailed {
		t.Fatalf("calls=%d status=%s", calls, tasks[0].Status)
	}
}

func TestCancellationStopsEverything(t *testing.T) {
	s := testStore(t)
	tasks := seedTasks(t, s, 3, "a", "b:a", "c")
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{}, 4)
	sched := newSched(t, s, tasks, 1, func(ctx context.Context, task *c.Task) error {
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	})
	go func() { <-started; cancel() }()
	if err := sched.Run(ctx); err != nil {
		t.Fatal(err)
	}
	for k, st := range statuses(tasks) {
		if st != c.TaskCancelled {
			t.Errorf("%s = %s, want cancelled (no retry after cancellation)", k, st)
		}
	}
}

func TestTaskTimeoutFailsAndRetries(t *testing.T) {
	s := testStore(t)
	tasks := seedTasks(t, s, 2, "a")
	g, _ := NewGraph(tasks)
	var calls int32
	sched := NewScheduler(s, g, 1, 30*time.Millisecond, func(ctx context.Context, _ *c.Task) error {
		atomic.AddInt32(&calls, 1)
		<-ctx.Done()
		return ctx.Err()
	}, zerolog.Nop())
	if err := sched.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || tasks[0].Status != c.TaskFailed || !strings.Contains(tasks[0].Error, "timed out") {
		t.Fatalf("calls=%d status=%s error=%q", calls, tasks[0].Status, tasks[0].Error)
	}
}

func TestPanicInHandlerIsATaskFailure(t *testing.T) {
	s := testStore(t)
	tasks := seedTasks(t, s, 1, "a")
	sched := newSched(t, s, tasks, 1, func(context.Context, *c.Task) error { panic("boom") })
	if err := sched.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tasks[0].Status != c.TaskFailed || !strings.Contains(tasks[0].Error, "boom") {
		t.Fatalf("status=%s error=%q", tasks[0].Status, tasks[0].Error)
	}
}

// Two schedulers on the same database (two orchestrator processes) must never
// both run the same task.
func TestNoDuplicateDispatchAcrossSchedulers(t *testing.T) {
	s := testStore(t)
	specs := []string{"a", "b", "c", "d", "e", "f"}
	tasks1 := seedTasks(t, s, 1, specs...)
	tasks2, err := s.ListTasks(context.Background(), "run")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	runs := map[string]int{}
	exec := func(_ context.Context, task *c.Task) error {
		mu.Lock()
		runs[task.Key]++
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
		return nil
	}
	var wg sync.WaitGroup
	for _, tasks := range [][]*c.Task{tasks1, tasks2} {
		wg.Add(1)
		go func(tasks []*c.Task) {
			defer wg.Done()
			if err := newSched(t, s, tasks, 3, exec).Run(context.Background()); err != nil {
				t.Error(err)
			}
		}(tasks)
	}
	wg.Wait()
	for _, k := range specs {
		if runs[k] != 1 {
			t.Errorf("task %s executed %d times", k, runs[k])
		}
	}
}
