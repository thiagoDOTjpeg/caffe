package app

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func caffe(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"-C", dir}, args...))
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

func repo(t *testing.T) string {
	t.Helper()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "t"},
		{"commit", "-q", "--allow-empty", "-m", "init"}} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestInitCreatesProjectAndPersistsAcrossInvocations(t *testing.T) {
	dir := repo(t)
	if _, err := caffe(t, dir, "status"); err == nil || !strings.Contains(err.Error(), "caffe init") {
		t.Fatalf("uninitialized project: err = %v", err)
	}
	out, err := caffe(t, dir, "init")
	if err != nil || !strings.Contains(out, "Initialized") {
		t.Fatalf("init: %q, %v", out, err)
	}
	for _, f := range []string{".caffe/config.yaml", ".caffe/caffe.db", ".caffe/caffe.log"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s not created", f)
		}
	}

	c := exec.Command("git", "status", "--porcelain")
	c.Dir = dir
	if st, _ := c.Output(); len(st) != 0 {
		t.Errorf("git status after init: %q", st)
	}

	cfgPath := filepath.Join(dir, ".caffe", "config.yaml")
	data, _ := os.ReadFile(cfgPath)
	edited := strings.Replace(string(data), "max_concurrent_agents: 3", "max_concurrent_agents: 7", 1)
	os.WriteFile(cfgPath, []byte(edited), 0o644)
	if out, err := caffe(t, dir, "init"); err != nil || !strings.Contains(out, "already initialized") {
		t.Fatalf("second init: %q, %v", out, err)
	}
	out, err = caffe(t, dir, "config")
	if err != nil || !strings.Contains(out, "max_concurrent_agents: 7") {
		t.Fatalf("config: %q, %v", out, err)
	}

	sub := filepath.Join(dir, "a", "b")
	os.MkdirAll(sub, 0o755)
	if out, err := caffe(t, sub, "status"); err != nil || !strings.Contains(out, "No active runs") {
		t.Fatalf("status from subdirectory: %q, %v", out, err)
	}
	if out, err := caffe(t, dir, "runs"); err != nil || !strings.Contains(out, "STATUS") {
		t.Fatalf("runs: %q, %v", out, err)
	}
	if _, err := caffe(t, dir, "inspect", "nope"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("inspect unknown run: %v", err)
	}
}

func TestInvalidConfigurationStopsEveryCommand(t *testing.T) {
	dir := repo(t)
	if _, err := caffe(t, dir, "init"); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, ".caffe", "config.yaml")
	data, _ := os.ReadFile(cfgPath)
	os.WriteFile(cfgPath, append(data, []byte("\nmystery_option: true\n")...), 0o644)
	for _, args := range [][]string{{"status"}, {"runs"}, {"config"}, {"run", "x"}} {
		if _, err := caffe(t, dir, args...); err == nil || !strings.Contains(err.Error(), "mystery_option") {
			t.Errorf("caffe %v with invalid config: err = %v", args, err)
		}
	}
}

func TestRunIDFlag(t *testing.T) {
	dir := repo(t)
	if _, err := caffe(t, dir, "init"); err != nil {
		t.Fatal(err)
	}
	_, err := caffe(t, dir, "run", "--run-id", "nope", "do something")
	if err == nil || !strings.Contains(err.Error(), "canonical UUID") {
		t.Fatalf("err = %v", err)
	}
	if out, _ := caffe(t, dir, "runs"); strings.Count(out, "\n") != 1 {
		t.Errorf("a refused run was recorded:\n%s", out)
	}
}

func TestDetachRejectsRunID(t *testing.T) {
	dir := repo(t)
	if _, err := caffe(t, dir, "init"); err != nil {
		t.Fatal(err)
	}
	_, err := caffe(t, dir, "run", "--detach", "--run-id", "0f8fad5b-d9cb-469f-a165-70867728950e", "x")
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("err = %v", err)
	}
}

func TestInitRequiresGitRepository(t *testing.T) {
	dir := t.TempDir()
	if _, err := caffe(t, dir, "init"); err == nil || !strings.Contains(err.Error(), "not a Git repository") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".caffe")); err == nil {
		t.Error("init left files behind in a non-repository")
	}
}
