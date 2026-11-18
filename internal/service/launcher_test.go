package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	c "caffe/internal/contracts"
	"caffe/internal/persistence"
)

// The test binary doubles as a fake `caffe` executable: when
// CAFFE_FAKE_LAUNCH is set, TestMain behaves like `caffe -C <dir> run|resume`
// and exits instead of running tests.
func TestMain(m *testing.M) {
	if mode := os.Getenv("CAFFE_FAKE_LAUNCH"); mode != "" {
		os.Exit(fakeCaffe(mode, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeCaffe(mode string, args []string) int {
	// args: -C <dir> run --run-id <id> [--plan f] -- <request>   |   -C <dir> resume <id>
	dir := args[1]
	state := filepath.Join(dir, "fake-launch")
	os.MkdirAll(state, 0o755)
	os.WriteFile(filepath.Join(state, "args"), []byte(strings.Join(args, "\n")), 0o644)
	os.WriteFile(filepath.Join(state, "detach"), []byte(detachEvidence()), 0o644)
	if mode == "fail" {
		fmt.Fprintln(os.Stderr, "boom: planner failed, api_key=supersecret999")
		return 3
	}
	ctx := context.Background()
	s, err := persistence.Open(ctx, filepath.Join(dir, ".caffe", "caffe.db"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer s.Close()
	switch args[2] {
	case "run":
		now := persistence.Now()
		run := &c.Run{ID: args[4], ProjectPath: dir, UserRequest: args[len(args)-1], Status: c.RunPending, CreatedAt: now, HeartbeatAt: &now}
		if err := s.CreateRun(ctx, run); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	case "resume":
		run, err := s.GetRun(ctx, args[3])
		if err != nil {
			return 1
		}
		run.Status = c.RunExecuting
		if err := s.UpdateRun(ctx, run, c.RunInterrupted); err != nil {
			return 1
		}
	}
	fmt.Println("started")
	time.Sleep(700 * time.Millisecond) // the "run" keeps going after the launcher returned
	os.WriteFile(filepath.Join(state, "done"), nil, 0o644)
	return 0
}

func launcherFixture(t *testing.T, mode string) (*fixture, string) {
	t.Helper()
	f := newFixture(t)
	f.svc.exe = os.Args[0]
	t.Setenv("CAFFE_FAKE_LAUNCH", mode)
	// Under -race the fake would sleep 1 s at every exit (atexit_sleep_ms).
	t.Setenv("GORACE", strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
	return f, filepath.Join(f.cfg.Project.Path, "fake-launch")
}

func TestStartRunLaunchesDetachedProcess(t *testing.T) {
	ctx := context.Background()
	f, state := launcherFixture(t, "ok")
	request := "--dangerous looking request; rm -rf /"
	id, err := f.svc.StartRun(ctx, StartRunRequest{Request: request})
	if err != nil {
		t.Fatal(err)
	}
	// The id is known before the run exists, and the run row is there now.
	if _, err := f.svc.RunSnapshot(ctx, id); err != nil {
		t.Fatalf("run %s not visible: %v", id, err)
	}
	// StartRun returned while the child is still running...
	if _, err := os.Stat(filepath.Join(state, "done")); err == nil {
		t.Error("StartRun waited for the whole run instead of its start")
	}
	// ...detached from the caller's terminal, so it cannot be signalled
	// through it (per platform: detach_*_test.go).
	evidence, _ := os.ReadFile(filepath.Join(state, "detach"))
	assertDetached(t, string(evidence))
	// Arguments arrive intact; the request cannot be parsed as a flag.
	args, _ := os.ReadFile(filepath.Join(state, "args"))
	want := strings.Join([]string{"-C", f.cfg.Project.Path, "run", "--run-id", id, "--", request}, "\n")
	if string(args) != want {
		t.Errorf("argv:\n%s\nwant:\n%s", args, want)
	}
	// The child keeps running after we returned and finishes on its own.
	waitDone(t, state)
	console, _ := os.ReadFile(filepath.Join(f.cfg.ArtifactDir(), id, "console.log"))
	if !strings.Contains(string(console), "started") {
		t.Errorf("console.log = %q", console)
	}
}

func TestStartRunReportsEarlyExitWithConsoleTail(t *testing.T) {
	f, _ := launcherFixture(t, "fail")
	_, err := f.svc.StartRun(context.Background(), StartRunRequest{Request: "x"})
	if err == nil || !strings.Contains(err.Error(), "exited before the run started") || !strings.Contains(err.Error(), "planner failed") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "supersecret999") {
		t.Error("console tail not redacted")
	}
	if _, err := f.svc.StartRun(context.Background(), StartRunRequest{Request: "  "}); err == nil {
		t.Error("empty request accepted")
	}
}

func TestResumeRunLaunchesAndWaitsForClaim(t *testing.T) {
	ctx := context.Background()
	f, state := launcherFixture(t, "ok")
	now := persistence.Now()
	id := "22222222-0000-0000-0000-000000000000"
	must(t, f.w.CreateRun(ctx, &c.Run{ID: id, ProjectPath: f.cfg.Project.Path, UserRequest: "r", Status: c.RunInterrupted, CreatedAt: now}))
	if err := f.svc.ResumeRun(ctx, id[:8]); err != nil {
		t.Fatal(err)
	}
	v, _ := f.svc.RunSnapshot(ctx, id)
	if v.Run.Status != c.RunExecuting {
		t.Errorf("status after resume = %s", v.Run.Status)
	}
	args, _ := os.ReadFile(filepath.Join(state, "args"))
	if !strings.HasSuffix(string(args), "resume\n"+id) {
		t.Errorf("argv = %q", args)
	}
	waitDone(t, state) // the child must not outlive the test's temp dir
	// Only interrupted runs can be resumed.
	if err := f.svc.ResumeRun(ctx, id); err == nil || !strings.Contains(err.Error(), "only interrupted") {
		t.Errorf("resume of an executing run: %v", err)
	}
}

func waitDone(t *testing.T, state string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(state, "done")); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
