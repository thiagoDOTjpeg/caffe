package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, yaml string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(root), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestDefaultRoundTrips(t *testing.T) {
	root := t.TempDir()
	if err := Write(root, Default("demo")); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Orchestration.MaxConcurrentAgents != 3 || cfg.Orchestration.DefaultTimeout != 30*time.Minute {
		t.Errorf("orchestration = %+v", cfg.Orchestration)
	}
	if cfg.Persistence.Path != filepath.Join(root, ".caffe", "caffe.db") || cfg.Git.WorktreeDirectory != filepath.Join(root, ".caffe", "worktrees") {
		t.Errorf("paths not resolved against the project root: %s, %s", cfg.Persistence.Path, cfg.Git.WorktreeDirectory)
	}
	if !cfg.Security.PreserveFailedWorktrees {
		t.Error("failed worktrees must be preserved by default")
	}
}

func TestSpecExampleLoads(t *testing.T) {
	root := write(t, `
project:
  name: my-project
  path: .
runtime:
  provider: herdr
  executable: herdr
orchestration:
  max_concurrent_agents: 3
  max_task_retries: 2
  default_timeout: 30m
agents:
  researcher:
    kind: agy
  developer:
    kind: claude
  reviewer:
    kind: codex
  validator:
    kind: claude
git:
  worktree_directory: .caffe/worktrees
  branch_prefix: caffe
persistence:
  driver: sqlite
  path: .caffe/caffe.db
security:
  require_confirmation_for_merge: true
  preserve_failed_worktrees: true
`)
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent("researcher").Kind != "agy" || cfg.Agent("reviewer").Kind != "codex" || cfg.Agent("planner").Kind != "claude" {
		t.Errorf("agents = %+v", cfg.Agents)
	}
}

func TestInvalidSettingsAreNeverIgnored(t *testing.T) {
	cases := map[string]struct{ yaml, want string }{
		"unknown key":         {"orchestration:\n  max_concurent_agents: 5\n", "max_concurent_agents"},
		"zero concurrency":    {"orchestration:\n  max_concurrent_agents: 0\n", "max_concurrent_agents"},
		"negative retries":    {"orchestration:\n  max_task_retries: -1\n", "max_task_retries"},
		"tiny timeout":        {"orchestration:\n  default_timeout: 5s\n", "default_timeout"},
		"bad duration":        {"orchestration:\n  default_timeout: soon\n", "time.Duration"},
		"other runtime":       {"runtime:\n  provider: tmux\n", "runtime.provider"},
		"other driver":        {"persistence:\n  driver: postgres\n", "persistence.driver"},
		"unknown role":        {"agents:\n  hacker:\n    kind: claude\n", "agents.hacker"},
		"bad kind":            {"agents:\n  developer:\n    kind: \"claude; rm -rf /\"\n", "agents.developer.kind"},
		"bad branch prefix":   {"git:\n  branch_prefix: \"../x y\"\n", "branch_prefix"},
		"bad session":         {"runtime:\n  session: \"a b\"\n", "runtime.session"},
		"empty command":       {"validation:\n  commands: [\"  \"]\n", "validation.commands[0]"},
		"not yaml":            {"{{{", "yaml"},
		"wrong type":          {"orchestration:\n  max_concurrent_agents: many\n", "into int"},
		"studio theme":        {"studio:\n  theme: neon\n", "studio.theme"},
		"studio unknown key":  {"studio:\n  colour: red\n", "colour"},
		"studio blank editor": {"studio:\n  editor: \"   \"\n", "studio.editor"},
		"studio wrong type":   {"studio:\n  mouse: maybe\n", "bool"},
	}
	for name, tc := range cases {
		_, err := Load(write(t, tc.yaml))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want error mentioning %q, got %v", name, tc.want, err)
		}
	}

	_, err := Load(write(t, "orchestration:\n  max_concurrent_agents: 0\n  max_task_retries: 99\n"))
	if err == nil || !strings.Contains(err.Error(), "max_concurrent_agents") || !strings.Contains(err.Error(), "max_task_retries") {
		t.Errorf("want both problems reported, got %v", err)
	}
}

func TestMissingProject(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil || !strings.Contains(err.Error(), "caffe init") {
		t.Fatalf("err = %v", err)
	}
}

func TestStudioSection(t *testing.T) {

	cfg, err := Load(write(t, "project:\n  name: x\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Studio.Theme != "caffe" || cfg.Studio.Mouse || cfg.Studio.ReduceMotion || !cfg.Studio.Avatars || cfg.Studio.Editor != "" {
		t.Errorf("defaults = %+v", cfg.Studio)
	}

	cfg, err = Load(write(t, "studio:\n  theme: high-contrast\n  mouse: true\n  editor: nvim -p\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Studio.Theme != "high-contrast" || !cfg.Studio.Mouse || !cfg.Studio.Avatars || cfg.Studio.Editor != "nvim -p" {
		t.Errorf("partial = %+v", cfg.Studio)
	}
	for _, theme := range StudioThemes {
		if _, err := Load(write(t, "studio:\n  theme: "+theme+"\n")); err != nil {
			t.Errorf("theme %s rejected: %v", theme, err)
		}
	}
}
