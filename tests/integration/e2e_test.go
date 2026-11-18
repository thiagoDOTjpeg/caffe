// Package integration holds the end-to-end test against a real Herdr and real
// coding agents. It spends model tokens and needs a human once (see below),
// so it only runs with CAFFE_E2E=1:
//
//	make e2e
//
// Environment:
//
//	CAFFE_E2E_SESSION     Herdr session to use (default: the default session).
//	                     Use a dedicated headless one to keep agents out of
//	                     your own workspaces: `herdr --session caffe-e2e server`
//	CAFFE_E2E_RESEARCHER  agent kind for the researcher (default claude)
//	CAFFE_E2E_DEVELOPER   agent kind for the developer  (default claude)
//
// The test creates a brand-new repository, which no agent trusts yet. Each
// agent kind therefore shows its "trust this folder?" dialog once. caffe never
// answers such dialogs: the task goes to `blocked` and the test log prints the
// `herdr agent attach` command to run. Answer it and the test continues.
package integration

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"caffe/internal/app"
)

type logWriter struct{ t *testing.T }

func (w logWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func TestEndToEndWithRealHerdr(t *testing.T) {
	if os.Getenv("CAFFE_E2E") != "1" {
		t.Skip("set CAFFE_E2E=1 to run against a real Herdr with real agents (costs tokens)")
	}
	ctx := context.Background()
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	caffe := func(args ...string) (string, error) {
		cmd := app.NewRootCommand()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(logWriter{t})
		cmd.SetArgs(append([]string{"-C", repo}, args...))
		err := cmd.ExecuteContext(ctx)
		t.Log(out.String())
		return out.String(), err
	}

	// 1. A test Git repository.
	git("init", "-q", "-b", "main")
	git("config", "user.email", "e2e@localhost")
	git("config", "user.name", "caffe e2e")
	os.WriteFile(filepath.Join(repo, "README.md"), []byte("# e2e\n\nA scratch project.\n"), 0o644)
	git("add", "-A")
	git("commit", "-qm", "init")
	base := git("rev-parse", "HEAD")

	if _, err := caffe("init"); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(repo, ".caffe", "config.yaml")
	cfg, _ := os.ReadFile(cfgPath)
	edit := strings.NewReplacer(
		`session: ""`, "session: "+env("CAFFE_E2E_SESSION", `""`),
		"commands: []", "commands:\n        - test -f hello.txt\n        - grep -q caffe hello.txt",
	).Replace(string(cfg))
	edit = strings.Replace(edit, "researcher:\n        kind: claude", "researcher:\n        kind: "+env("CAFFE_E2E_RESEARCHER", "claude"), 1)
	edit = strings.Replace(edit, "developer:\n        kind: claude", "developer:\n        kind: "+env("CAFFE_E2E_DEVELOPER", "claude"), 1)
	if err := os.WriteFile(cfgPath, []byte(edit), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := caffe("agents"); err != nil || strings.Contains(out, "NOT AVAILABLE") {
		t.Fatalf("Herdr or a configured agent kind is not available:\n%s\n%v", out, err)
	}

	// 2-5. A harmless task: workspace, agent, prompt and result all go through Herdr.
	plan := `{"goal":"Add a hello file","tasks":[
	 {"id":"research","title":"Inspect the project","description":"List the files in the project root and read README.md. Report what the project contains.","role":"researcher","dependencies":[],"acceptance_criteria":["The files in the project root are listed in the summary"]},
	 {"id":"impl","title":"Create hello.txt","description":"Create a file named hello.txt in the root of your working directory containing exactly one line: hello from caffe","role":"developer","dependencies":["research"],"acceptance_criteria":["hello.txt contains the line 'hello from caffe'"]},
	 {"id":"validate","title":"Validate","description":"Run the checks.","role":"validator","dependencies":["impl"],"acceptance_criteria":["Checks pass"]}]}`
	planPath := filepath.Join(t.TempDir(), "plan.json")
	os.WriteFile(planPath, []byte(plan), 0o644)
	out, err := caffe("run", "Add a hello file to the project", "--plan", planPath)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	// 6. The generated artifact.
	branches := git("branch", "--list", "caffe/*", "--format=%(refname:short)")
	if strings.Count(branches, "\n") != 0 || !strings.HasSuffix(branches, "/impl") {
		t.Fatalf("want exactly one developer branch, got %q", branches)
	}
	if got := git("show", branches+":hello.txt"); got != "hello from caffe" {
		t.Errorf("hello.txt on %s = %q", branches, got)
	}
	if _, err := os.Stat(filepath.Join(repo, "hello.txt")); err == nil {
		t.Error("the change leaked into the main checkout")
	}
	if head := git("rev-parse", "HEAD"); head != base || git("status", "--porcelain") != "" {
		t.Error("the main checkout was modified")
	}

	// 7. State transitions, in order, for every task.
	for _, task := range []string{"research", "impl", "validate"} {
		last := -1
		for _, step := range []string{"pending → ready", "ready → assigned", "assigned → running", "→ completed"} {
			i := -1
			for n, line := range strings.Split(out, "\n") {
				if strings.Contains(line, "] "+task+" ") && strings.Contains(line, step) {
					i = n
				}
			}
			if i <= last {
				t.Errorf("task %s: transition %q missing or out of order", task, step)
			}
			last = i
		}
	}
	if !strings.Contains(out, "`test -f hello.txt` → exit 0") {
		t.Error("validation exit code not reported")
	}
	if report, err := caffe("inspect", "--events", strings.Fields(out[strings.Index(out, "run ")+4:])[0]); err != nil || !strings.Contains(report, "executed by the orchestrator") {
		t.Errorf("inspect: %v", err)
	}
}
