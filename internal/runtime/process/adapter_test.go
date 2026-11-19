//go:build unix

package process

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"caffe/internal/agents"
	"caffe/internal/contracts"
	"caffe/internal/runtime"
)

const execID = "0a1b2c3d-0000-0000-0000-000000000000"

func sh(script string) Command {
	return func(prompt string, _ []string) []string { return []string{"sh", "-c", script, "sh", prompt} }
}

func execute(t *testing.T, script string) (*agents.Result, error, string) {
	t.Helper()
	dir := t.TempDir()
	rt := New(dir, map[string]Command{"fake": sh(script)})
	m := &agents.Manager{RT: rt, Poll: 10 * time.Millisecond, Log: zerolog.Nop(), IdleNudgeAfter: time.Second, IdleGiveUpAfter: time.Second}
	req := agents.Request{ExecutionID: execID, Role: contracts.RoleDeveloper, Kind: "fake", Cwd: t.TempDir(), Prompt: "do it", Dir: t.TempDir()}
	res, err := m.Execute(context.Background(), req)
	return res, err, dir
}

func TestHeadlessAgentDeliversReport(t *testing.T) {
	res, err, dir := execute(t, `set -- $1; for w; do r=$w; done; sleep 0.2; echo '{"status":"completed","summary":"ok"}' > "$r"`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(res.Report), `"ok"`) {
		t.Fatalf("report = %s", res.Report)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("agent state left behind: %v", left)
	}
}

func TestHeadlessAgentThatExitsWithoutReportFailsAtOnce(t *testing.T) {
	start := time.Now()
	res, err, _ := execute(t, `echo "Please run /login" >&2; exit 3`)
	if !errors.Is(err, agents.ErrAgentGone) {
		t.Fatalf("want ErrAgentGone, got %v", err)
	}
	if time.Since(start) > 900*time.Millisecond {
		t.Fatal("waited for the idle timers instead of failing on exit")
	}
	out, _ := os.ReadFile(res.OutputPath)
	if !strings.Contains(string(out), "Please run /login") || !strings.Contains(string(out), "exit status 3") {
		t.Fatalf("output does not explain the failure:\n%s", out)
	}
}

func TestStopKillsTheAgentFromAnotherAdapter(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	kinds := map[string]Command{"fake": sh(`sleep 30`)}
	rt := New(dir, kinds)
	ws, err := rt.CreateWorkspace(ctx, t.TempDir(), "c1-developer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.StartAgent(ctx, "fake", ws.PaneID, "c1-developer", nil); err != nil {
		t.Fatal(err)
	}
	if st, _ := rt.GetAgentStatus(ctx, "c1-developer"); st != runtime.StatusIdle {
		t.Fatalf("before the prompt status = %s", st)
	}
	if err := rt.SendPrompt(ctx, "c1-developer", "go"); err != nil {
		t.Fatal(err)
	}
	other := New(dir, kinds)
	if st, _ := other.GetAgentStatus(ctx, "c1-developer"); st != runtime.StatusWorking {
		t.Fatalf("another process sees status %s", st)
	}
	if err := other.StopAgent(ctx, ws.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := rt.GetAgentStatus(ctx, "c1-developer"); errors.Is(err, runtime.ErrNotFound) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("agent still alive after stop")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestUnknownKindIsRejected(t *testing.T) {
	rt := New(t.TempDir(), Builtin)
	ws, _ := rt.CreateWorkspace(context.Background(), t.TempDir(), "c1-planner")
	if _, err := rt.StartAgent(context.Background(), "agy", ws.PaneID, "c1-planner", nil); err == nil {
		t.Fatal("agy has no headless mode here and must be rejected")
	}
}
