package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"caffe/internal/contracts"
)

const (
	Dir  = ".caffe"
	File = "config.yaml"
)

type Config struct {
	Project       Project              `yaml:"project"`
	Runtime       Runtime              `yaml:"runtime"`
	Orchestration Orchestration        `yaml:"orchestration"`
	Agents        map[string]AgentSpec `yaml:"agents"`
	Validation    Validation           `yaml:"validation"`
	Git           Git                  `yaml:"git"`
	Persistence   Persistence          `yaml:"persistence"`
	Security      Security             `yaml:"security"`
	Studio        Studio               `yaml:"studio"`
}

type Project struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

type Runtime struct {
	Provider   string `yaml:"provider"`
	Executable string `yaml:"executable"`

	Session        string        `yaml:"session"`
	CommandTimeout time.Duration `yaml:"command_timeout"`
	PollInterval   time.Duration `yaml:"poll_interval"`

	IdleNudgeAfter  time.Duration `yaml:"idle_nudge_after"`
	IdleGiveUpAfter time.Duration `yaml:"idle_give_up_after"`
}

type Orchestration struct {
	MaxConcurrentAgents int           `yaml:"max_concurrent_agents"`
	MaxTaskRetries      int           `yaml:"max_task_retries"`
	MaxReviewRounds     int           `yaml:"max_review_rounds"`
	DefaultTimeout      time.Duration `yaml:"default_timeout"`
}

type AgentSpec struct {
	Kind string `yaml:"kind"`

	Args []string `yaml:"args"`
}

type Validation struct {
	Commands []string      `yaml:"commands"`
	Timeout  time.Duration `yaml:"timeout"`
}

type Git struct {
	WorktreeDirectory string `yaml:"worktree_directory"`
	BranchPrefix      string `yaml:"branch_prefix"`
}

type Persistence struct {
	Driver string `yaml:"driver"`
	Path   string `yaml:"path"`
}

type Security struct {
	RequireConfirmationForMerge bool `yaml:"require_confirmation_for_merge"`
	PreserveFailedWorktrees     bool `yaml:"preserve_failed_worktrees"`
}

type Studio struct {
	Theme        string `yaml:"theme"`
	Mouse        bool   `yaml:"mouse"`
	ReduceMotion bool   `yaml:"reduce_motion"`
	Avatars      bool   `yaml:"avatars"`

	Editor string `yaml:"editor"`
}

var StudioThemes = []string{"caffe", "high-contrast", "mono"}

func Default(projectName string) *Config {
	return &Config{
		Project: Project{Name: projectName, Path: "."},
		Runtime: Runtime{
			Provider: "herdr", Executable: "herdr",
			CommandTimeout: 60 * time.Second, PollInterval: 3 * time.Second,
			IdleNudgeAfter: 45 * time.Second, IdleGiveUpAfter: 5 * time.Minute,
		},
		Orchestration: Orchestration{
			MaxConcurrentAgents: 3, MaxTaskRetries: 2, MaxReviewRounds: 2, DefaultTimeout: 30 * time.Minute,
		},
		Agents: map[string]AgentSpec{
			"planner":    {Kind: "claude"},
			"researcher": {Kind: "claude"},
			"developer":  {Kind: "claude"},
			"reviewer":   {Kind: "claude"},
			"validator":  {Kind: "claude"},
		},
		Validation:  Validation{Timeout: 15 * time.Minute},
		Git:         Git{WorktreeDirectory: ".caffe/worktrees", BranchPrefix: "caffe"},
		Persistence: Persistence{Driver: "sqlite", Path: ".caffe/caffe.db"},
		Security:    Security{RequireConfirmationForMerge: true, PreserveFailedWorktrees: true},
		Studio:      Studio{Theme: "caffe", Avatars: true},
	}
}

func Path(projectRoot string) string { return filepath.Join(projectRoot, Dir, File) }

func Load(projectRoot string) (*Config, error) {
	data, err := os.ReadFile(Path(projectRoot))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no Caffè project at %s (run `caffe init`)", projectRoot)
	}
	if err != nil {
		return nil, err
	}
	cfg := Default(filepath.Base(projectRoot))
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", Path(projectRoot), err)
	}
	if err := cfg.resolve(projectRoot); err != nil {
		return nil, fmt.Errorf("%s: %w", Path(projectRoot), err)
	}
	return cfg, nil
}

var (
	branchPrefixRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(/[A-Za-z0-9][A-Za-z0-9._-]*)*$`)
	sessionRE      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	kindRE         = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
)

func (c *Config) resolve(projectRoot string) error {
	abs := func(p string) string {
		if filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
		return filepath.Join(projectRoot, p)
	}
	c.Project.Path = abs(c.Project.Path)
	c.Git.WorktreeDirectory = abs(c.Git.WorktreeDirectory)
	c.Persistence.Path = abs(c.Persistence.Path)

	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }

	if c.Project.Name == "" {
		add("project.name is empty")
	}
	if c.Runtime.Provider != "herdr" && c.Runtime.Provider != "process" {
		add("runtime.provider %q is not supported (herdr or process)", c.Runtime.Provider)
	}
	if c.Runtime.Executable == "" {
		add("runtime.executable is empty")
	}
	if c.Runtime.Session != "" && !sessionRE.MatchString(c.Runtime.Session) {
		add("runtime.session %q is not a valid session name", c.Runtime.Session)
	}
	if c.Runtime.CommandTimeout < time.Second {
		add("runtime.command_timeout must be at least 1s")
	}
	if c.Runtime.PollInterval < 100*time.Millisecond {
		add("runtime.poll_interval must be at least 100ms")
	}
	if c.Runtime.IdleNudgeAfter < time.Second {
		add("runtime.idle_nudge_after must be at least 1s")
	}
	if c.Runtime.IdleGiveUpAfter < time.Second {
		add("runtime.idle_give_up_after must be at least 1s")
	}
	if n := c.Orchestration.MaxConcurrentAgents; n < 1 || n > 32 {
		add("orchestration.max_concurrent_agents must be between 1 and 32, got %d", n)
	}
	if n := c.Orchestration.MaxTaskRetries; n < 0 || n > 10 {
		add("orchestration.max_task_retries must be between 0 and 10, got %d", n)
	}
	if n := c.Orchestration.MaxReviewRounds; n < 0 || n > 10 {
		add("orchestration.max_review_rounds must be between 0 and 10, got %d", n)
	}
	if c.Orchestration.DefaultTimeout < time.Minute {
		add("orchestration.default_timeout must be at least 1m")
	}
	for role, spec := range c.Agents {
		if r := contracts.Role(role); !r.ValidForTask() && r != contracts.RolePlanner {
			add("agents.%s is not a known role", role)
		}
		if !kindRE.MatchString(spec.Kind) {
			add("agents.%s.kind %q is invalid", role, spec.Kind)
		}
	}
	for _, role := range append([]contracts.Role{contracts.RolePlanner}, contracts.TaskRoles...) {
		if _, ok := c.Agents[string(role)]; !ok {
			add("agents.%s is missing", role)
		}
	}
	for i, cmd := range c.Validation.Commands {
		if len(strings.Fields(cmd)) == 0 {
			add("validation.commands[%d] is empty", i)
		}
	}
	if len(c.Validation.Commands) > 0 && c.Validation.Timeout < time.Second {
		add("validation.timeout must be at least 1s")
	}
	if !branchPrefixRE.MatchString(c.Git.BranchPrefix) {
		add("git.branch_prefix %q is not a valid branch prefix", c.Git.BranchPrefix)
	}
	if !slices.Contains(StudioThemes, c.Studio.Theme) {
		add("studio.theme %q is not one of %s", c.Studio.Theme, strings.Join(StudioThemes, ", "))
	}
	if c.Studio.Editor != "" && len(strings.Fields(c.Studio.Editor)) == 0 {
		add("studio.editor is blank")
	}
	if c.Persistence.Driver != "sqlite" {
		add("persistence.driver %q is not supported (only sqlite)", c.Persistence.Driver)
	}
	if len(errs) > 0 {
		return errors.New("invalid configuration: " + strings.Join(errs, "; "))
	}
	return nil
}

func (c *Config) Agent(role contracts.Role) AgentSpec { return c.Agents[string(role)] }

func (c *Config) ArtifactDir() string {
	return filepath.Join(c.Project.Path, Dir, "artifacts")
}

func Write(projectRoot string, c *Config) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(projectRoot, Dir), 0o755); err != nil {
		return err
	}
	return os.WriteFile(Path(projectRoot), data, 0o644)
}
