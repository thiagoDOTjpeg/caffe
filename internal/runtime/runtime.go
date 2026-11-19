package runtime

import (
	"context"
	"errors"
)

type AgentStatus string

const (
	StatusIdle    AgentStatus = "idle"
	StatusWorking AgentStatus = "working"
	StatusBlocked AgentStatus = "blocked"
	StatusDone    AgentStatus = "done"
	StatusUnknown AgentStatus = "unknown"
)

func (s AgentStatus) ReadyForInput() bool { return s == StatusIdle || s == StatusDone }

var (
	ErrNotFound = errors.New("runtime: not found")

	ErrNotReady = errors.New("runtime: agent launched but not ready for prompts")

	ErrBlocked = errors.New("runtime: agent is blocked, prompt not sent")

	ErrUncertain = errors.New("runtime: outcome uncertain")

	ErrUnavailable = errors.New("runtime: unavailable")
)

type Capabilities struct {
	Version string

	Kinds []string

	Missing []string
}

func (c *Capabilities) Supports(kind string) bool {
	for _, k := range c.Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

type Workspace struct {
	ID     string
	PaneID string
}

type AgentSession struct {
	Name       string
	Kind       string
	PaneID     string
	TerminalID string
	Status     AgentStatus
}

type Runtime interface {
	Capabilities(ctx context.Context) (*Capabilities, error)

	CreateWorkspace(ctx context.Context, cwd, label string) (*Workspace, error)

	StartAgent(ctx context.Context, kind, paneID, name string, args []string) (*AgentSession, error)

	SendPrompt(ctx context.Context, agent, prompt string) error
	GetAgentStatus(ctx context.Context, agent string) (AgentStatus, error)

	ReadAgentOutput(ctx context.Context, agent string) (string, error)

	StopAgent(ctx context.Context, workspaceID string) error
}

type Attacher interface {
	AttachCommand(agent string) ([]string, error)
}
