package herdr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"caffe/internal/runtime"
)

func TestMain(m *testing.M) {
	if log := os.Getenv("CAFFE_FAKE_HERDR_LOG"); log != "" {
		os.Exit(fakeHerdrMain(log, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeHerdrMain(logPath string, args []string) int {
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		fmt.Fprint(f, "--call\n")
		for _, a := range args {
			fmt.Fprintf(f, "%s\n", a)
		}
		f.Close()
	}
	arg := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}
	out := func(s string) int { fmt.Println(s); return 0 }
	fail := func(s string) int { fmt.Println(s); return 1 }
	switch arg(0) + " " + arg(1) {
	case "--version ":
		return out("herdr 0.9.5")
	case "workspace list":
		return out(`{"id":"x","result":{"type":"workspace_list","workspaces":[]}}`)
	case "workspace create":
		return out(`{"id":"x","result":{"root_pane":{"pane_id":"w7:p1"},"workspace":{"workspace_id":"w7"},"type":"workspace_created"}}`)
	case "workspace close":
		if arg(2) == "gone" {
			return fail(`{"error":{"code":"workspace_not_found","message":"workspace gone not found"},"id":"x"}`)
		}
		return out(`{"id":"x","result":{"type":"ok"}}`)
	case "agent start":
		switch arg(2) {
		case "--help":
			return out("  [possible values: " + installedKind + ", definitely-not-installed-kind]")
		case "trustme":
			fmt.Fprintln(os.Stderr, `{"error":{"code":"agent_not_ready","message":"blocked during startup"},"id":"x"}`)
			return 1
		}
		return out(`{"id":"x","result":{"agent":{"agent":"claude","agent_status":"idle","name":"` + arg(2) + `","pane_id":"w7:p1","terminal_id":"term_1"},"type":"agent_started"}}`)
	case "agent get":
		switch arg(2) {
		case "missing":
			return fail(`{"error":{"code":"agent_not_found","message":"agent target missing not found"},"id":"x"}`)
		case "trustme":
			return out(`{"id":"x","result":{"agent":{"agent":"claude","agent_status":"blocked","name":"trustme","pane_id":"w7:p1"}}}`)
		case "future":
			return out(`{"id":"x","result":{"agent":{"agent":"claude","agent_status":"levitating","name":"future"}}}`)
		}
		return out(`{"id":"x","result":{"agent":{"agent":"claude","agent_status":"working","name":"a"}}}`)
	case "agent prompt":
		switch arg(2) {
		case "blocked":
			return fail(`{"error":{"code":"agent_blocked","message":"agent is blocked"},"id":"x"}`)
		case "slow":
			time.Sleep(5 * time.Second)
		case "crash":
			fmt.Fprintln(os.Stderr, "panic: token=supersecretvalue123 exploded")
			return 3
		}
		return out(`{"id":"x","result":{"type":"agent_prompted"}}`)
	case "agent read":
		fmt.Print("line one\nline two\n")
		return 0
	}
	fmt.Fprintln(os.Stderr, "unknown command")
	return 2
}

const installedKind = "caffe-fake-kind"

func fakeHerdr(t *testing.T) (exe, dir string) {
	t.Helper()
	dir = t.TempDir()
	bin := t.TempDir()
	name := installedKind
	if goruntime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte("not run"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CAFFE_FAKE_HERDR_LOG", filepath.Join(dir, "calls.log"))

	t.Setenv("GORACE", strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
	return os.Args[0], dir
}

func adapter(t *testing.T, timeout time.Duration) (*Adapter, string) {
	exe, dir := fakeHerdr(t)
	return New(exe, "", timeout, zerolog.Nop()), dir
}

func TestLifecycleAgainstFakeExecutable(t *testing.T) {
	ctx := context.Background()
	a, dir := adapter(t, 5*time.Second)

	caps, err := a.Capabilities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if caps.Version != "0.9.5" || !caps.Supports(installedKind) || caps.Supports("definitely-not-installed-kind") || len(caps.Missing) != 1 {
		t.Fatalf("capabilities = %+v", caps)
	}

	ws, err := a.CreateWorkspace(ctx, "/some dir/with space", "label")
	if err != nil || ws.ID != "w7" || ws.PaneID != "w7:p1" {
		t.Fatalf("workspace = %+v, %v", ws, err)
	}
	s, err := a.StartAgent(ctx, "claude", ws.PaneID, "dev", []string{"--model", "x"})
	if err != nil || s.Name != "dev" || s.Status != runtime.StatusIdle || s.TerminalID != "term_1" {
		t.Fatalf("session = %+v, %v", s, err)
	}

	prompt := "do it; rm -rf / $(whoami) `id`\n'second line' \"quoted\""
	if err := a.SendPrompt(ctx, "dev", prompt); err != nil {
		t.Fatal(err)
	}
	if st, err := a.GetAgentStatus(ctx, "dev"); err != nil || st != runtime.StatusWorking {
		t.Fatalf("status = %q, %v", st, err)
	}
	if out, err := a.ReadAgentOutput(ctx, "dev"); err != nil || !strings.Contains(out, "line two") {
		t.Fatalf("output = %q, %v", out, err)
	}
	if err := a.StopAgent(ctx, ws.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.StopAgent(ctx, "gone"); err != nil {
		t.Fatalf("stopping an already-closed workspace must not fail: %v", err)
	}

	log, _ := os.ReadFile(filepath.Join(dir, "calls.log"))
	calls := string(log)
	for _, want := range []string{
		"--call\nworkspace\ncreate\n--cwd\n/some dir/with space\n--label\nlabel\n--no-focus\n",
		"--call\nagent\nstart\ndev\n--kind\nclaude\n--pane\nw7:p1\n--timeout\n45000\n--\n--model\nx\n",
		"--call\nagent\nprompt\ndev\n" + prompt + "\n--call",
	} {
		if !strings.Contains(calls, want) {
			t.Errorf("argv not passed as expected.\nwant: %q\ngot:  %q", want, calls)
		}
	}
}

func TestSessionFlagIsPrepended(t *testing.T) {
	exe, dir := fakeHerdr(t)
	a := New(exe, "isolated", 5*time.Second, zerolog.Nop())

	_, _ = a.GetAgentStatus(context.Background(), "x")
	log, _ := os.ReadFile(filepath.Join(dir, "calls.log"))
	if !strings.HasPrefix(string(log), "--call\n--session\nisolated\nagent\nget\nx\n") {
		t.Fatalf("argv = %q", log)
	}
}

func TestErrorsAreStructured(t *testing.T) {
	ctx := context.Background()
	a, _ := adapter(t, 5*time.Second)

	_, err := a.GetAgentStatus(ctx, "missing")
	var he *Error
	if !errors.Is(err, runtime.ErrNotFound) || !errors.As(err, &he) || he.Code != "agent_not_found" || he.ExitCode != 1 {
		t.Fatalf("want ErrNotFound with herdr code and exit code, got %v (%+v)", err, he)
	}
	if err := a.SendPrompt(ctx, "blocked", "p"); !errors.Is(err, runtime.ErrBlocked) {
		t.Fatalf("want ErrBlocked, got %v", err)
	}

	err = a.SendPrompt(ctx, "crash", "p")
	if !errors.As(err, &he) || he.ExitCode != 3 || strings.Contains(err.Error(), "supersecretvalue123") || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("want exit 3 with redacted stderr, got %v", err)
	}

	if st, err := a.GetAgentStatus(ctx, "future"); err != nil || st != runtime.StatusUnknown {
		t.Fatalf("status = %q, %v", st, err)
	}
}

func TestStartBlockedReturnsSession(t *testing.T) {
	a, _ := adapter(t, 5*time.Second)
	s, err := a.StartAgent(context.Background(), "claude", "w7:p1", "trustme", nil)
	if !errors.Is(err, runtime.ErrNotReady) || s == nil || s.Status != runtime.StatusBlocked {
		t.Fatalf("want a blocked session with ErrNotReady, got %+v, %v", s, err)
	}
}

func TestTimeoutIsUncertainNotFailure(t *testing.T) {
	a, _ := adapter(t, 300*time.Millisecond)
	start := time.Now()
	err := a.SendPrompt(context.Background(), "slow", "p")
	if !errors.Is(err, runtime.ErrUncertain) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want ErrUncertain wrapping DeadlineExceeded, got %v", err)
	}
	if d := time.Since(start); d > 4*time.Second {
		t.Fatalf("timeout not enforced, took %s", d)
	}
}

func TestCancellationStopsTheProcess(t *testing.T) {
	a, _ := adapter(t, 30*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	err := a.SendPrompt(ctx, "slow", "p")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if d := time.Since(start); d > 4*time.Second {
		t.Fatalf("cancellation not honoured, took %s", d)
	}
}

func TestMissingExecutableIsUnavailable(t *testing.T) {
	a := New(filepath.Join(t.TempDir(), "no-such-herdr"), "", time.Second, zerolog.Nop())
	if _, err := a.Capabilities(context.Background()); !errors.Is(err, runtime.ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
}

func TestRedact(t *testing.T) {
	fakeKey := "sk-" + strings.Repeat("a1B2", 6)
	for in, leak := range map[string]string{
		"API_KEY=abcdef123456":           "abcdef123456",
		"Authorization: Bearer-abcdefgh": "Bearer-abcdefgh",
		"key " + fakeKey:                 fakeKey,
		`{"password": "hunter2hunter2"}`: "hunter2hunter2",
	} {
		if out := runtime.Redact(in); strings.Contains(out, leak) {
			t.Errorf("runtime.Redact(%q) = %q still leaks", in, out)
		}
	}
	if out := runtime.Redact("nothing secret here"); out != "nothing secret here" {
		t.Errorf("clean text was altered: %q", out)
	}
}

func TestAttachCommand(t *testing.T) {
	var _ runtime.Attacher = (*Adapter)(nil)
	plain := New("/opt/herdr/bin/herdr", "", time.Second, zerolog.Nop())
	argv, err := plain.AttachCommand("c0112dd6a-developer")
	if err != nil || strings.Join(argv, " ") != "/opt/herdr/bin/herdr agent attach c0112dd6a-developer" {
		t.Errorf("argv = %q, %v", argv, err)
	}
	isolated := New("herdr", "caffe", time.Second, zerolog.Nop())
	argv, _ = isolated.AttachCommand("x")
	if strings.Join(argv, "\x00") != "herdr\x00--session\x00caffe\x00agent\x00attach\x00x" {
		t.Errorf("argv with session = %q", argv)
	}
	if _, err := plain.AttachCommand(""); err == nil {
		t.Error("empty agent name accepted")
	}
}
