package runtime

import (
	"context"
	"fmt"
	"sync"
)

type Fake struct {
	OnPrompt func(agent, kind, cwd, prompt string)

	StartErr error

	PromptErr error
	KindsList []string

	Inert bool

	mu      sync.Mutex
	seq     int
	ws      map[string]string
	agents  map[string]*fakeAgent
	Prompts []string

	MaxConcurrent int
	working       int
}

type fakeAgent struct {
	kind, ws string
	status   AgentStatus
	finished bool
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
	if f.Inert {
		f.mu.Unlock()
		return f.PromptErr
	}
	a.status, a.finished = StatusWorking, false
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
		a.finished = true
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
	st := a.status
	if a.finished && st == StatusWorking {
		a.status, a.finished = StatusIdle, false
	}
	return st, nil
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

func (f *Fake) SetStatus(agent string, s AgentStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.agents[agent]; ok {
		a.status = s
	}
}

func (f *Fake) Live() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for n := range f.agents {
		out = append(out, n)
	}
	return out
}
