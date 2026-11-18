package runtime

import (
	"context"
	"fmt"
	"sync"
)

// Fake is an in-memory Runtime for tests. OnPrompt simulates the agent doing
// its work (typically writing a report file) and runs on its own goroutine;
// while it runs the agent reports "working".
type Fake struct {
	// OnPrompt is called for every submitted prompt. cwd is the workspace cwd.
	OnPrompt func(agent, kind, cwd, prompt string)
	// StartErr, when set, is returned by StartAgent (with a valid session if it
	// wraps ErrNotReady).
	StartErr error
	// PromptErr, when set, is returned by SendPrompt after delivering the prompt.
	PromptErr error
	KindsList []string

	mu      sync.Mutex
	seq     int
	ws      map[string]string // workspace id -> cwd
	agents  map[string]*fakeAgent
	Prompts []string
	// MaxConcurrent is the highest number of agents that were working at once.
	MaxConcurrent int
	working       int
}

type fakeAgent struct {
	kind, ws string
	status   AgentStatus
}

func NewFake() *Fake {
	return &Fake{ws: map[string]string{}, agents: map[string]*fakeAgent{}, KindsList: []string{"claude", "codex"}}
}

func (f *Fake) Capabilities(context.Context) (*Capabilities, error) {
	return &Capabilities{Version: "fake", Kinds: f.KindsList}, nil
}

func (f *Fake) CreateWorkspace(_ context.Context, cwd, _ string) (*Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	id := fmt.Sprintf("w%d", f.seq)
	f.ws[id] = cwd
	return &Workspace{ID: id, PaneID: id + ":p1"}, nil
}

func (f *Fake) StartAgent(_ context.Context, kind, paneID, name string, _ []string) (*AgentSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, dup := f.agents[name]; dup {
		return nil, fmt.Errorf("agent name %s already in use", name)
	}
	wsID := paneID[:len(paneID)-len(":p1")]
	if _, ok := f.ws[wsID]; !ok {
		return nil, ErrNotFound
	}
	a := &fakeAgent{kind: kind, ws: wsID, status: StatusIdle}
	if f.StartErr != nil {
		a.status = StatusBlocked
	}
	f.agents[name] = a
	return &AgentSession{Name: name, Kind: kind, PaneID: paneID, Status: a.status}, f.StartErr
}

func (f *Fake) SendPrompt(_ context.Context, agent, prompt string) error {
	f.mu.Lock()
	a, ok := f.agents[agent]
	if !ok {
		f.mu.Unlock()
		return ErrNotFound
	}
	if a.status == StatusBlocked {
		f.mu.Unlock()
		return ErrBlocked
	}
	f.Prompts = append(f.Prompts, prompt)
	a.status = StatusWorking
	f.working++
	if f.working > f.MaxConcurrent {
		f.MaxConcurrent = f.working
	}
	cwd, kind, hook := f.ws[a.ws], a.kind, f.OnPrompt
	f.mu.Unlock()

	go func() {
		if hook != nil {
			hook(agent, kind, cwd, prompt)
		}
		f.mu.Lock()
		f.working--
		if a.status == StatusWorking {
			a.status = StatusIdle
		}
		f.mu.Unlock()
	}()
	return f.PromptErr
}

func (f *Fake) GetAgentStatus(_ context.Context, agent string) (AgentStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.agents[agent]
	if !ok {
		return "", ErrNotFound
	}
	return a.status, nil
}

func (f *Fake) ReadAgentOutput(_ context.Context, agent string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.agents[agent]; !ok {
		return "", ErrNotFound
	}
	return "fake output of " + agent, nil
}

func (f *Fake) StopAgent(_ context.Context, workspaceID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.ws, workspaceID)
	for name, a := range f.agents {
		if a.ws == workspaceID {
			delete(f.agents, name)
		}
	}
	return nil
}

// SetStatus forces an agent's status (e.g. to unblock it like a human would).
func (f *Fake) SetStatus(agent string, s AgentStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.agents[agent]; ok {
		a.status = s
	}
}

// Live returns the names of agents that still exist.
func (f *Fake) Live() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for n := range f.agents {
		out = append(out, n)
	}
	return out
}
