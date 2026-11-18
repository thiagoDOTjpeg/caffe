package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	c "caffe/internal/contracts"
	"caffe/internal/runtime"
)

// brokenRuntime is a runtime that cannot be reached.
type brokenRuntime struct{ *runtime.Fake }

func (brokenRuntime) ReadAgentOutput(context.Context, string) (string, error) {
	return "", fmt.Errorf("%w: server not running", runtime.ErrUnavailable)
}

func TestAgentOutputLiveCapturedAndNone(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	fake := runtime.NewFake()
	f.svc.rt = fake

	// The developer agent is alive in the runtime: live output.
	ws, _ := fake.CreateWorkspace(ctx, f.cfg.Project.Path, "x")
	if _, err := fake.StartAgent(ctx, "claude", ws.PaneID, "c3-developer", nil); err != nil {
		t.Fatal(err)
	}
	out, err := f.svc.AgentOutput(ctx, "ag-dev", 50)
	if err != nil || out.Source != OutputLive || !strings.Contains(out.Text, "fake output of c3-developer") {
		t.Fatalf("live = %+v, %v", out, err)
	}

	// The researcher finished: its captured screen, redacted and bounded.
	dir := filepath.Join(f.cfg.ArtifactDir(), "11111111-0000-0000-0000-000000000000", "research", "exresear")
	must(t, os.MkdirAll(dir, 0o755))
	var screen strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&screen, "line %d\n", i)
	}
	screen.WriteString("export API_KEY=supersecret777\n")
	path := filepath.Join(dir, "output.txt")
	must(t, os.WriteFile(path, []byte(screen.String()), 0o600))
	must(t, f.w.AddArtifact(ctx, &c.Artifact{RunID: "11111111-0000-0000-0000-000000000000", TaskID: "t-research", Type: c.ArtifactLog, Path: path}))
	out, err = f.svc.AgentOutput(ctx, "ag-res", 5)
	if err != nil {
		t.Fatal(err)
	}
	if out.Source != OutputCaptured || !out.Truncated || strings.Count(out.Text, "\n") != 4 || !strings.HasPrefix(out.Text, "line 27") {
		t.Errorf("captured = %+v", out)
	}
	if strings.Contains(out.Text, "supersecret777") {
		t.Error("captured output not redacted")
	}

	// A planning agent has no execution and no captured output.
	if out, err := f.svc.AgentOutput(ctx, "ag-plan", 5); err != nil || out.Source != OutputNone || out.Text != "" {
		t.Errorf("planner = %+v, %v", out, err)
	}

	// A running agent that the runtime no longer knows falls back to what was
	// captured for it (nothing, here), instead of failing.
	if err := fake.StopAgent(ctx, ws.ID); err != nil {
		t.Fatal(err)
	}
	if out, err := f.svc.AgentOutput(ctx, "ag-dev", 5); err != nil || out.Source != OutputNone {
		t.Errorf("vanished agent = %+v, %v", out, err)
	}

	// An unreachable runtime is reported as such, not as "no output".
	f.svc.rt = brokenRuntime{runtime.NewFake()}
	if _, err := f.svc.AgentOutput(ctx, "ag-dev", 5); !errors.Is(err, ErrUnavailable) {
		t.Errorf("broken runtime: %v", err)
	}
	if _, err := f.svc.AgentOutput(ctx, "nope", 5); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing agent: %v", err)
	}
	if _, err := f.svc.AgentOutput(ctx, "ag-dev", 0); err == nil {
		t.Error("lines 0 accepted")
	}
}

func TestLastLines(t *testing.T) {
	for _, tc := range []struct {
		in    string
		n     int
		want  string
		trunc bool
	}{
		{"a\nb\nc\n", 2, "b\nc", true},
		{"a\nb\n", 5, "a\nb", false},
		{"", 3, "", false},
		{"only", 1, "only", false},
	} {
		got, trunc := lastLines(tc.in, tc.n)
		if got != tc.want || trunc != tc.trunc {
			t.Errorf("lastLines(%q, %d) = %q, %v", tc.in, tc.n, got, trunc)
		}
	}
}
