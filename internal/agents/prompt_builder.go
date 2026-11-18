package agents

import (
	"bytes"
	"fmt"
	"sync"
	"text/template"

	"caffe/internal/contracts"
	"caffe/prompts"
)

// PromptData is everything a role prompt can reference.
type PromptData struct {
	UserRequest string
	ProjectPath string
	Cwd         string
	ReportPath  string

	// task roles
	TaskKey     string
	Title       string
	Description string
	Criteria    []string
	Upstream    []Upstream

	// developer
	Branch      string
	Interrupted bool   // a previous attempt left this worktree in an unknown state
	PriorError  string // why the previous attempt stopped, when known
	Findings    []contracts.Finding

	// reviewer
	DiffPath   string
	BaseCommit string

	// planner
	Context  string
	MaxTasks int
}

// Upstream is the result of a dependency, passed on as untrusted information.
type Upstream struct {
	Key        string
	Role       contracts.Role
	Summary    string
	ReportPath string
}

var parseTemplates = sync.OnceValues(func() (*template.Template, error) {
	return template.New("prompts").Option("missingkey=error").ParseFS(prompts.FS, "*.md")
})

// Render builds the prompt for a role.
func Render(role contracts.Role, data PromptData) (string, error) {
	t, err := parseTemplates()
	if err != nil {
		return "", fmt.Errorf("prompt templates: %w", err)
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, string(role)+".md", data); err != nil {
		return "", fmt.Errorf("render %s prompt: %w", role, err)
	}
	return buf.String(), nil
}
