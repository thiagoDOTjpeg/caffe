package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"caffe/internal/agents"
	"caffe/internal/contracts"
)

type PlanningInput struct {
	RunID       string
	UserRequest string
	ProjectPath string
	Context     string
	MaxTasks    int
	Dir         string
}

type LLMProvider interface {
	GeneratePlan(ctx context.Context, input PlanningInput) (*contracts.ExecutionPlan, error)
}

type AgentProvider struct {
	Agents *agents.Manager
	Kind   string
	Args   []string

	OnSession func(runID string, s agents.Session)
	OnBlocked func(runID string, blocked bool)
}

func (p *AgentProvider) GeneratePlan(ctx context.Context, in PlanningInput) (*contracts.ExecutionPlan, error) {
	execID := uuid.NewString()
	prompt, err := agents.Render(contracts.RolePlanner, agents.PromptData{
		UserRequest: in.UserRequest, ProjectPath: in.ProjectPath, Cwd: in.ProjectPath,
		ReportPath: agents.ReportPath(in.ProjectPath, execID), Context: in.Context, MaxTasks: in.MaxTasks,
	})
	if err != nil {
		return nil, err
	}
	res, err := p.Agents.Execute(ctx, agents.Request{
		ExecutionID: execID, Role: contracts.RolePlanner, Kind: p.Kind, Args: p.Args,
		Cwd: in.ProjectPath, Prompt: prompt, Dir: in.Dir,
		OnSession: func(s agents.Session) {
			if p.OnSession != nil {
				p.OnSession(in.RunID, s)
			}
		},
		OnBlocked: func(b bool) {
			if p.OnBlocked != nil {
				p.OnBlocked(in.RunID, b)
			}
		},
	})
	if err != nil {
		return nil, fmt.Errorf("planner agent: %w", err)
	}
	return ParsePlan(res.Report, in.MaxTasks)
}

func ParsePlan(data []byte, maxTasks int) (*contracts.ExecutionPlan, error) {
	raw, err := contracts.ExtractJSON(data)
	if err != nil {
		return nil, fmt.Errorf("invalid plan: %w", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()

	var report struct {
		contracts.ExecutionPlan
		Status  string `json:"status"`
		Summary string `json:"summary"`
	}
	if err := dec.Decode(&report); err != nil {
		return nil, fmt.Errorf("invalid plan: %w", err)
	}
	if report.Status == "failed" {
		return nil, fmt.Errorf("planner failed: %s", report.Summary)
	}
	plan := report.ExecutionPlan
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	if maxTasks > 0 && len(plan.Tasks) > maxTasks {
		return nil, fmt.Errorf("invalid plan: %d tasks, the limit is %d", len(plan.Tasks), maxTasks)
	}
	return &plan, nil
}

var skipDirs = map[string]bool{"node_modules": true, "vendor": true, "target": true, "dist": true, "build": true, "__pycache__": true}

const (
	maxContextEntries = 150
	maxReadmeLines    = 40
)

func CollectContext(projectPath string) (string, error) {
	var b strings.Builder
	b.WriteString("Files (two levels):\n")
	n := 0
	err := filepath.WalkDir(projectPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(projectPath, path)
		if rel == "." {
			return nil
		}

		rel = filepath.ToSlash(rel)
		name := d.Name()
		if d.IsDir() && (strings.HasPrefix(name, ".") || skipDirs[name]) {
			return filepath.SkipDir
		}
		depth := strings.Count(rel, "/") + 1
		if depth > 2 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if n++; n > maxContextEntries {
			b.WriteString("  … (truncated)\n")
			return filepath.SkipAll
		}
		if d.IsDir() {
			rel += "/"
		}
		b.WriteString("  " + rel + "\n")
		return nil
	})
	if err != nil {
		return "", err
	}
	for _, name := range []string{"README.md", "README", "readme.md", "README.rst"} {
		data, err := os.ReadFile(filepath.Join(projectPath, name))
		if err != nil {
			continue
		}
		lines := strings.SplitN(string(data), "\n", maxReadmeLines+1)
		if len(lines) > maxReadmeLines {
			lines = append(lines[:maxReadmeLines], "… (truncated)")
		}
		b.WriteString("\nStart of " + name + ":\n" + strings.Join(lines, "\n") + "\n")
		break
	}
	return b.String(), nil
}
