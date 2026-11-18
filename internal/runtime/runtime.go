// Package runtime defines the boundary to the external agent execution
// runtime. The core depends only on this interface; Herdr is one adapter.
package runtime

import (
	"context"
	"errors"
)

// AgentStatus mirrors what the runtime can observe about an agent's terminal.
// None of these mean "the task is done": idle and done only mean the agent
// accepts input again.
type AgentStatus string

const (
	StatusIdle    AgentStatus = "idle"
	StatusWorking AgentStatus = "working"
	StatusBlocked AgentStatus = "blocked" // approval or question UI on screen; needs a human
	StatusDone    AgentStatus = "done"    // idle, not yet seen by the user
	StatusUnknown AgentStatus = "unknown"
)

// ReadyForInput reports whether a prompt can be submitted.
func (s AgentStatus) ReadyForInput() bool { return s == StatusIdle || s == StatusDone }

var (
	// ErrNotFound: the agent or workspace no longer exists in the runtime.
	ErrNotFound = errors.New("runtime: not found")
	// ErrNotReady: the agent was launched but is blocked before it can take a
	// prompt (typically a trust or login dialog). The session is still returned.
	ErrNotReady = errors.New("runtime: agent launched but not ready for prompts")
	// ErrBlocked: the prompt was rejected, nothing was sent.
	ErrBlocked = errors.New("runtime: agent is blocked, prompt not sent")
	// ErrUncertain: the operation may or may not have taken effect (timeout,
	// stalled prompt). Callers must inspect state before retrying.
	ErrUncertain = errors.New("runtime: outcome uncertain")
	// ErrUnavailable: the runtime itself cannot be reached.
	ErrUnavailable = errors.New("runtime: unavailable")
)

type Capabilities struct {
	Version string
	// Kinds the runtime can start AND whose executable is installed.
	Kinds []string
	// Missing are kinds the runtime knows but whose executable is not on PATH.
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
	PaneID string // root pane: an interactive shell in the workspace cwd
}

type AgentSession struct {
	Name       string // target for every later call
	Kind       string
	PaneID     string
	TerminalID string
	Status     AgentStatus
}

// Runtime is the set of operations the orchestrator needs. It is shaped after
// Herdr's real semantics: a workspace owns a root pane, an agent is started in
// a pane, and closing the workspace is the only way to stop an agent.
type Runtime interface {
	// Capabilities discovers what the installed runtime can do.
	Capabilities(ctx context.Context) (*Capabilities, error)
	// CreateWorkspace opens a workspace whose shell runs in cwd.
	CreateWorkspace(ctx context.Context, cwd, label string) (*Workspace, error)
	// StartAgent launches an agent in a pane. On ErrNotReady the returned
	// session is valid and the agent is waiting for a human.
	StartAgent(ctx context.Context, kind, paneID, name string, args []string) (*AgentSession, error)
	// SendPrompt submits text. ErrUncertain means it may have been delivered.
	SendPrompt(ctx context.Context, agent, prompt string) error
	GetAgentStatus(ctx context.Context, agent string) (AgentStatus, error)
	// ReadAgentOutput returns recent terminal text. Best effort.
	ReadAgentOutput(ctx context.Context, agent string) (string, error)
	// StopAgent closes the workspace hosting an agent. Stopping something that
	// is already gone is not an error.
	StopAgent(ctx context.Context, workspaceID string) error
}

// Attacher is an optional capability: a runtime that can connect a human's
// terminal to an agent's terminal (to answer a trust or permission dialog,
// or to watch it work). Callers discover it with a type assertion; a runtime
// without it simply does not offer attaching.
type Attacher interface {
	// AttachCommand returns the argv that attaches the caller's terminal to
	// the agent. The caller runs it with its terminal handed over to it.
	AttachCommand(agent string) ([]string, error)
}
