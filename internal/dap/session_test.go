package dap

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"caffe/internal/dap/daptest"
)

func TestMain(m *testing.M) {
	if os.Getenv(daptest.Env) == "1" {
		daptest.Serve(os.Args[1:])
		os.Exit(0)
	}
	os.Setenv(daptest.Env, "1")
	code := m.Run()
	os.Exit(code)
}

func fake() Config {
	return Config{Name: "fake", Command: []string{os.Args[0], "--client-addr", "{addr}"}}
}

func next(t *testing.T, s *Session, kind string) Event {
	t.Helper()
	for {
		select {
		case e := <-s.Events():
			if e.Kind == kind {
				return e
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("no %s event", kind)
		}
	}
}

func TestSessionAgainstAFakeAdapter(t *testing.T) {
	ctx := context.Background()
	s, err := Start(ctx, fake(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bps, err := s.Launch(ctx, LaunchRequest{Mode: "test", Program: "/w/pkg", Args: []string{"-test.run", "^TestHello$"}}, map[string][]int{"/w/pkg/hello.go": {14}})
	if err != nil || len(bps["/w/pkg/hello.go"]) != 1 || !bps["/w/pkg/hello.go"][0].Verified {
		t.Fatalf("launch %+v %v", bps, err)
	}
	if e := next(t, s, "stopped"); e.Reason != "breakpoint" || e.ThreadID != 1 {
		t.Fatalf("stopped %+v", e)
	}
	frames, err := s.StackTrace(ctx, 1)
	if err != nil || len(frames) != 2 || frames[0].Line != 14 || frames[0].Path != "/w/pkg/hello.go" {
		t.Fatalf("frames %+v %v", frames, err)
	}
	scopes, _ := s.Scopes(ctx, frames[0].ID)
	vars, err := s.Variables(ctx, scopes[0].Ref)
	if err != nil || len(vars) != 2 || vars[0].Value != `"from herd"` || vars[1].Ref != 2 {
		t.Fatalf("vars %+v %v", vars, err)
	}
	if out, err := s.Evaluate(ctx, "len(n)", frames[0].ID); err != nil || out != "value of len(n)" {
		t.Fatalf("evaluate %q %v", out, err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.Next(ctx, 1))
	next(t, s, "stopped")
	if f, _ := s.StackTrace(ctx, 1); f[0].Line != 15 {
		t.Fatalf("after next: line %d", f[0].Line)
	}
	if err := s.call(ctx, "restartFrame", nil, nil); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("failed request: %v", err)
	}
	must(s.Continue(ctx, 1))
	if e := next(t, s, "output"); e.Output != "PASS\n" {
		t.Fatalf("output %+v", e)
	}
	next(t, s, "terminated")
	must(s.Disconnect(ctx))
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("still connected")
	}
	if _, err := s.Threads(ctx); err == nil {
		t.Fatal("call after disconnect")
	}
}

func TestAdapterThatNeverConnects(t *testing.T) {
	_, err := Start(context.Background(), Config{Name: "x", Command: []string{os.Args[0], "-test.run=^$"}}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "exited before connecting") {
		t.Fatalf("%v", err)
	}
}

func TestRealDelve(t *testing.T) {
	if os.Getenv("CAFFE_DAP_REAL") != "1" {
		t.Skip("set CAFFE_DAP_REAL=1 to run against dlv")
	}
	if _, err := exec.LookPath("dlv"); err != nil {
		t.Skip("dlv not installed")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package demo\n\nfunc greet(n string) string {\n\treturn \"hello \" + n\n}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "hello_test.go"), []byte("package demo\n\nimport \"testing\"\n\nfunc TestHello(t *testing.T) {\n\tif greet(\"x\") != \"hello x\" {\n\t\tt.Fatal()\n\t}\n}\n"), 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	os.Unsetenv(daptest.Env)
	s, err := Start(ctx, Delve, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Disconnect(ctx)
	file := filepath.Join(dir, "hello.go")
	if _, err := s.Launch(ctx, LaunchRequest{Mode: "test", Program: dir, Cwd: dir, Args: []string{"-test.run", "^TestHello$"}}, map[string][]int{file: {4}}); err != nil {
		t.Fatal(err)
	}
	e := next(t, s, "stopped")
	frames, err := s.StackTrace(ctx, e.ThreadID)
	if err != nil || len(frames) == 0 || frames[0].Line != 4 || !strings.HasSuffix(frames[0].Name, "greet") {
		t.Fatalf("frames %+v %v", frames, err)
	}
	scopes, _ := s.Scopes(ctx, frames[0].ID)
	vars, _ := s.Variables(ctx, scopes[0].Ref)
	if len(vars) == 0 || vars[0].Name != "n" || vars[0].Value != `"x"` {
		t.Fatalf("vars %+v", vars)
	}
	if err := s.Continue(ctx, e.ThreadID); err != nil {
		t.Fatal(err)
	}
	next(t, s, "terminated")
}
