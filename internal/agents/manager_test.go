package agents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"caffe/internal/contracts"
	"caffe/internal/runtime"
)

const execID = "0a1b2c3d-0000-0000-0000-000000000000"

func setup(t *testing.T) (*Manager, *runtime.Fake, Request) {
	t.Helper()
	rt := runtime.NewFake()
	m := &Manager{RT: rt, Poll: 5 * time.Millisecond, Log: zerolog.Nop(), IdleNudgeAfter: 15 * time.Millisecond, IdleGiveUpAfter: 80 * time.Millisecond}
	req := Request{ExecutionID: execID, Role: contracts.RoleDeveloper, Kind: "claude", Cwd: t.TempDir(), Prompt: "full prompt", Dir: t.TempDir()}
	return m, rt, req
}

func TestExecuteDeliversReportAndCleansUp(t *testing.T) {
	m, rt, req := setup(t)
	rt.OnPrompt = func(_, _, cwd, prompt string) {

		if strings.Contains(prompt, "full prompt") {
			t.Errorf("prompt body leaked into terminal input: %q", prompt)
		}
		os.WriteFile(ReportPath(cwd, execID), []byte("```json\n{\"status\":\"completed\",\"summary\":\"ok\"}\n```"), 0o644)
	}
	var sessions []Session
	req.OnSession = func(s Session) { sessions = append(sessions, s) }

	res, err := m.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Report) != `{"status":"completed","summary":"ok"}` {
		t.Fatalf("report = %s", res.Report)
	}
	if b, _ := os.ReadFile(filepath.Join(req.Dir, "prompt.md")); string(b) != "full prompt" {
		t.Fatalf("prompt file = %q", b)
	}
	if _, err := os.Stat(ReportPath(req.Cwd, execID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("report must be moved out of the working directory")
	}
	if _, err := os.Stat(res.ReportPath); err != nil {
		t.Fatal(err)
	}
	if len(sessions) == 0 || sessions[0].WorkspaceID == "" || sessions[0].Name != "c0a1b2c3d-developer" {
		t.Fatalf("session not reported before start: %+v", sessions)
	}
	if live := rt.Live(); len(live) != 0 {
		t.Fatalf("agent left running: %v", live)
	}
}

func TestIdleIsNotCompletion(t *testing.T) {
	m, rt, req := setup(t)

	_, err := m.Execute(context.Background(), req)
	if !errors.Is(err, ErrNoReport) {
		t.Fatalf("want ErrNoReport, got %v", err)
	}
	if len(rt.Prompts) != 2 || !strings.Contains(rt.Prompts[1], "stopped without delivering") {
		t.Fatalf("want exactly one reminder, prompts = %q", rt.Prompts)
	}
	if len(rt.Live()) != 0 {
		t.Fatal("agent left running after failure")
	}
}

func TestBriefIdlenessIsNotNudged(t *testing.T) {
	m, rt, req := setup(t)
	m.IdleNudgeAfter = 5 * time.Second
	rt.OnPrompt = func(_, _, cwd, _ string) {
		time.AfterFunc(80*time.Millisecond, func() {
			os.WriteFile(ReportPath(cwd, execID), []byte(`{"status":"completed","summary":"after background work"}`), 0o644)
		})
	}
	res, err := m.Execute(context.Background(), req)
	if err != nil || !strings.Contains(string(res.Report), "after background work") {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if len(rt.Prompts) != 1 {
		t.Fatalf("agent was nudged while legitimately idle: %q", rt.Prompts)
	}
}

func TestReminderRecoversForgetfulAgent(t *testing.T) {
	m, rt, req := setup(t)
	rt.OnPrompt = func(_, _, cwd, prompt string) {
		if strings.Contains(prompt, "stopped without delivering") {
			os.WriteFile(ReportPath(cwd, execID), []byte(`{"status":"completed","summary":"late"}`), 0o644)
		}
	}
	res, err := m.Execute(context.Background(), req)
	if err != nil || !strings.Contains(string(res.Report), "late") {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
}

func TestBlockedAtStartupWaitsForHuman(t *testing.T) {
	m, rt, req := setup(t)
	rt.StartErr = fmt.Errorf("%w: trust dialog", runtime.ErrNotReady)
	rt.OnPrompt = func(_, _, cwd, _ string) {
		os.WriteFile(ReportPath(cwd, execID), []byte(`{"status":"completed","summary":"ok"}`), 0o644)
	}
	var transitions []bool
	req.OnBlocked = func(b bool) {
		transitions = append(transitions, b)
		if b {
			time.AfterFunc(20*time.Millisecond, func() { rt.SetStatus("c0a1b2c3d-developer", runtime.StatusIdle) })
		}
	}
	if _, err := m.Execute(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if len(transitions) != 2 || !transitions[0] || transitions[1] {
		t.Fatalf("blocked transitions = %v", transitions)
	}
	if len(rt.Prompts) != 1 {
		t.Fatalf("prompt must be sent once, after unblocking: %q", rt.Prompts)
	}
}

func TestUncertainPromptIsNotResentWhenAgentIsWorking(t *testing.T) {
	m, rt, req := setup(t)
	rt.PromptErr = fmt.Errorf("%w: timeout", runtime.ErrUncertain)
	rt.OnPrompt = func(_, _, cwd, _ string) {
		time.Sleep(30 * time.Millisecond)
		os.WriteFile(ReportPath(cwd, execID), []byte(`{"status":"completed","summary":"ok"}`), 0o644)
	}
	if _, err := m.Execute(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if len(rt.Prompts) != 1 {
		t.Fatalf("timed-out prompt was resent although the agent was working: %d prompts", len(rt.Prompts))
	}
}

func TestCancellationStopsAgent(t *testing.T) {
	m, rt, req := setup(t)
	rt.OnPrompt = func(_, _, _, _ string) { time.Sleep(time.Second) }
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := m.Execute(ctx, req)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want DeadlineExceeded, got %v", err)
	}
	if len(rt.Live()) != 0 {
		t.Fatal("agent left running after timeout")
	}
}

func TestAttachDoesNotPromptAgain(t *testing.T) {
	m, rt, req := setup(t)

	ws, _ := rt.CreateWorkspace(context.Background(), req.Cwd, "x")
	rt.StartAgent(context.Background(), "claude", ws.PaneID, "old-agent", nil)
	os.WriteFile(ReportPath(req.Cwd, execID), []byte(`{"status":"completed","summary":"from before"}`), 0o644)

	req.Attach = &Session{Name: "old-agent", Kind: "claude", WorkspaceID: ws.ID}
	res, err := m.Execute(context.Background(), req)
	if err != nil || !strings.Contains(string(res.Report), "from before") {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if len(rt.Prompts) != 0 {
		t.Fatalf("re-attach must not prompt: %q", rt.Prompts)
	}

	req.Attach = &Session{Name: "vanished"}
	if _, err := m.Execute(context.Background(), req); !errors.Is(err, ErrAgentGone) {
		t.Fatalf("want ErrAgentGone, got %v", err)
	}
}

func TestReportMustBeRegularFile(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret")
	os.WriteFile(secret, []byte(`{"status":"completed","summary":"stolen"}`), 0o600)
	link := filepath.Join(dir, "report.json")
	if err := os.Symlink(secret, link); err != nil {
		t.Skip(err)
	}
	if _, err := readReport(link); err == nil {
		t.Fatal("symlinked report was followed")
	}
}

func TestRenderEveryRole(t *testing.T) {
	data := PromptData{
		UserRequest: "Add JWT", ProjectPath: "/p", Cwd: "/p/wt", ReportPath: "/p/wt/.caffe-report-x.json",
		TaskKey: "impl", Title: "Implement", Description: "Do the thing", Criteria: []string{"criterion-one"},
		Upstream: []Upstream{{Key: "research", Role: contracts.RoleResearcher, Summary: "upstream-summary"}},
		Branch:   "caffe/r/impl", Findings: []contracts.Finding{{Severity: "high", File: "a.go", Line: 7, Description: "finding-text"}},
		Interrupted: true, DiffPath: "/p/x.diff", BaseCommit: "abc", Context: "tree", MaxTasks: 8,
	}
	for _, role := range append([]contracts.Role{contracts.RolePlanner}, contracts.TaskRoles...) {
		out, err := Render(role, data)
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		for _, want := range []string{"Add JWT", "/p/wt/.caffe-report-x.json", "Constraints", "```json"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s prompt lacks %q", role, want)
			}
		}
		if role != contracts.RolePlanner {
			for _, want := range []string{"criterion-one", "upstream-summary", "Do the thing"} {
				if !strings.Contains(out, want) {
					t.Errorf("%s prompt lacks %q", role, want)
				}
			}
		}
	}
	dev, _ := Render(contracts.RoleDeveloper, data)
	for _, want := range []string{"a.go:7", "finding-text", "previous attempt", "caffe/r/impl"} {
		if !strings.Contains(dev, want) {
			t.Errorf("developer prompt lacks %q", want)
		}
	}
}
