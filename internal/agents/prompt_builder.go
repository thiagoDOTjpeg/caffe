package agents

import (
	"bytes"
	"fmt"
	"sync"
	"text/template"

	"caffe/internal/contracts"
	"caffe/prompts"
)

type PromptData struct {
	UserRequest string
	ProjectPath string
	Cwd         string
	ReportPath  string

	TaskKey     string
	Title       string
	Description string
	Criteria    []string
	Upstream    []Upstream

	Branch      string
	Interrupted bool
	PriorError  string
	Findings    []contracts.Finding

	DiffPath   string
	BaseCommit string

	Context  string
	MaxTasks int
}

type Upstream struct {
	Key        string
	Role       contracts.Role
	Summary    string
	ReportPath string
}

var parseTemplates = sync.OnceValues(func() (*template.Template, error) {
	return template.New("prompts").Option("missingkey=error").ParseFS(prompts.FS, "*.md")
})

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
