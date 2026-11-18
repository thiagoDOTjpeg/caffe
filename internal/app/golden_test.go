package app

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	c "caffe/internal/contracts"
	"caffe/internal/persistence"
)

// Golden files pin the exact output of the read-only CLI commands. They were
// first recorded against the implementation that read the store directly, so
// they prove that moving the CLI onto internal/service changed nothing a user
// can see. Re-record with: go test ./internal/app/ -run Golden -update
var update = flag.Bool("update", false, "rewrite golden files")

// goldenProject creates an initialized project with a fixed set of runs:
// one completed, one active with a live owner (fresh heartbeat), so that no
// command flags anything as interrupted and the output is deterministic.
func goldenProject(t *testing.T) string {
	t.Helper()
	time.Local = time.UTC // runs/inspect print local times
	dir := repo(t)
	if _, err := caffe(t, dir, "init"); err != nil {
		t.Fatal(err)
	}
	// A runtime that cannot exist, so `agents` output does not depend on
	// whether Herdr is installed on the machine running the tests.
	cfgPath := filepath.Join(dir, ".caffe", "config.yaml")
	data, _ := os.ReadFile(cfgPath)
	data = regexp.MustCompile(`executable: herdr`).ReplaceAll(data, []byte("executable: caffe-golden-no-such-runtime"))
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	s, err := persistence.Open(ctx, filepath.Join(dir, ".caffe", "caffe.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := func(min int) *time.Time {
		v := time.Date(2026, 11, 17, 9, min, 0, 0, time.UTC)
		return &v
	}
	ok := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	// Completed run.
	done := &c.Run{ID: "aaaaaaaa-1111-1111-1111-111111111111", ProjectPath: dir, UserRequest: "Add a hello file", Status: c.RunCompleted,
		CreatedAt: *at(0), StartedAt: at(0), CompletedAt: at(2), HeartbeatAt: at(2), Metadata: map[string]string{"base_commit": "7f5d944"}}
	ok(s.CreateRun(ctx, done))
	ok(s.CreateTasks(ctx, []*c.Task{
		{ID: "aaaaaaaa-t1", RunID: done.ID, Key: "research", Title: "Inspect", Description: "d", Role: c.RoleResearcher,
			Status: c.TaskCompleted, Attempts: 1, AcceptanceCriteria: []string{"x"}, MaxAttempts: 3, CreatedAt: *at(0), StartedAt: at(0), CompletedAt: at(1)},
		{ID: "aaaaaaaa-t2", RunID: done.ID, Key: "impl", Title: "Create hello.txt", Description: "d", Role: c.RoleDeveloper,
			Status: c.TaskCompleted, Attempts: 1, Dependencies: []string{"aaaaaaaa-t1"}, AcceptanceCriteria: []string{"x"}, MaxAttempts: 3,
			CreatedAt: *at(0), StartedAt: at(1), CompletedAt: at(2), BranchName: "caffe/aaaaaaaa/impl", WorktreePath: "/wt/impl"},
	}))
	// CreateTasks stores the plan; attempts, times and branches are written
	// by status updates, as the engine does.
	persist := func(runID string) {
		tasks, err := s.ListTasks(ctx, runID)
		ok(err)
		for _, task := range tasks {
			task.Attempts = 1
			switch task.Key {
			case "research":
				task.StartedAt, task.CompletedAt = at(0), at(1)
			case "impl":
				task.StartedAt, task.CompletedAt = at(1), at(2)
				if task.Status == c.TaskCompleted {
					task.BranchName, task.WorktreePath = "caffe/aaaaaaaa/impl", "/wt/impl"
				}
			default:
				task.Attempts = 0
			}
			ok(s.UpdateTask(ctx, task, task.Status, ""))
		}
	}
	persist(done.ID)
	zero := 0
	ok(s.SaveExecution(ctx, &c.Execution{ID: "aaaaaaaa-e1", TaskID: "aaaaaaaa-t2", Attempt: 1, Status: c.ExecCompleted,
		Prompt: "test -f hello.txt", StartedAt: *at(2), CompletedAt: at(2), ExitCode: &zero}))

	// Active run, owner alive.
	now := time.Now().UTC()
	live := &c.Run{ID: "bbbbbbbb-2222-2222-2222-222222222222", ProjectPath: dir, UserRequest: "Refactor the config loader", Status: c.RunExecuting,
		CreatedAt: *at(30), StartedAt: at(30), HeartbeatAt: &now, Metadata: map[string]string{"base_commit": "7f5d944"}}
	ok(s.CreateRun(ctx, live))
	ok(s.CreateTasks(ctx, []*c.Task{
		{ID: "bbbbbbbb-t1", RunID: live.ID, Key: "research", Title: "R", Description: "d", Role: c.RoleResearcher,
			Status: c.TaskCompleted, Attempts: 1, AcceptanceCriteria: []string{"x"}, MaxAttempts: 3, CreatedAt: *at(30), BranchName: ""},
		{ID: "bbbbbbbb-t2", RunID: live.ID, Key: "impl", Title: "I", Description: "d", Role: c.RoleDeveloper,
			Status: c.TaskBlocked, Attempts: 1, Dependencies: []string{"bbbbbbbb-t1"}, AcceptanceCriteria: []string{"x"}, MaxAttempts: 3, CreatedAt: *at(30)},
		{ID: "bbbbbbbb-t3", RunID: live.ID, Key: "docs", Title: "D", Description: "d", Role: c.RoleDeveloper,
			Status: c.TaskReady, AcceptanceCriteria: []string{"x"}, MaxAttempts: 3, CreatedAt: *at(30)},
		{ID: "bbbbbbbb-t4", RunID: live.ID, Key: "review", Title: "V", Description: "d", Role: c.RoleReviewer,
			Status: c.TaskPending, Dependencies: []string{"bbbbbbbb-t2", "bbbbbbbb-t3"}, AcceptanceCriteria: []string{"x"}, MaxAttempts: 3, CreatedAt: *at(30)},
	}))
	persist(live.ID)
	ok(s.SaveAgent(ctx, &c.Agent{ID: "bbbbbbbb-a1", RunID: live.ID, Name: "cbbbb0001-developer", Kind: "claude", Role: c.RoleDeveloper,
		Status: "running", PaneID: "w3:p1", CurrentTaskID: "bbbbbbbb-t2"}))
	ok(s.SaveAgent(ctx, &c.Agent{ID: "bbbbbbbb-a0", RunID: live.ID, Name: "cbbbb0000-researcher", Kind: "agy", Role: c.RoleResearcher, Status: "stopped"}))
	return dir
}

// eventTime masks event timestamps, which are written with the real clock.
var eventTime = regexp.MustCompile(`(?m)^\d\d:\d\d:\d\d\.\d{3}`)

func TestGoldenReadOnlyCommands(t *testing.T) {
	dir := goldenProject(t)
	for name, args := range map[string][]string{
		"runs":           {"runs"},
		"status":         {"status"},
		"tasks-live":     {"tasks", "bbbb"},
		"tasks-done":     {"tasks", "aaaa"},
		"inspect":        {"inspect", "aaaa"},
		"inspect-events": {"inspect", "aaaa", "--events"},
		"agents":         {"agents"},
	} {
		out, err := caffe(t, dir, args...)
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
		out = eventTime.ReplaceAllString(out, "HH:MM:SS.mmm")
		out = strings.ReplaceAll(out, dir, "<project>")
		// Go names the search path $PATH on Unix and %PATH% on Windows.
		out = strings.ReplaceAll(out, "%PATH%", "$PATH")
		path := filepath.Join("testdata", name+".golden")
		if *update {
			if err := os.MkdirAll("testdata", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (run with -update to record)", name, err)
		}
		if out != string(want) {
			t.Errorf("%s output changed.\n--- want\n%s\n--- got\n%s", name, want, out)
		}
	}
}
