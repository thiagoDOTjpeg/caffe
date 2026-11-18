package herdr

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"caffe/internal/runtime"
)

// Adapter implements runtime.Runtime on top of the Herdr CLI.
//
// Mapping (herdr 0.9.5):
//
//	CreateWorkspace -> herdr workspace create --cwd P --label L --no-focus
//	StartAgent      -> herdr agent start NAME --kind K --pane ID --timeout MS [-- args]
//	SendPrompt      -> herdr agent prompt NAME TEXT
//	GetAgentStatus  -> herdr agent get NAME
//	ReadAgentOutput -> herdr agent read NAME --source recent-unwrapped
//	StopAgent       -> herdr workspace close ID
type Adapter struct {
	cli          cli
	startTimeout time.Duration
}

var (
	_ runtime.Runtime  = (*Adapter)(nil)
	_ runtime.Attacher = (*Adapter)(nil)
)

// New builds an adapter. session selects a named Herdr session ("" = default).
func New(executable, session string, commandTimeout time.Duration, log zerolog.Logger) *Adapter {
	return &Adapter{
		cli:          cli{exe: executable, session: session, timeout: commandTimeout, log: log.With().Str("component", "herdr").Logger()},
		startTimeout: 45 * time.Second,
	}
}

// classify maps Herdr error codes onto the runtime's sentinel errors, keeping
// the original error in the chain.
func classify(err error) error {
	if err == nil {
		return nil
	}
	var he *Error
	if !errors.As(err, &he) {
		return err
	}
	var sentinel error
	switch he.Code {
	case "agent_not_found", "workspace_not_found", "pane_not_found", "not_found", "agent_not_running":
		sentinel = runtime.ErrNotFound
	case "agent_not_ready":
		sentinel = runtime.ErrNotReady
	case "agent_blocked":
		sentinel = runtime.ErrBlocked
	case "agent_prompt_stalled", "timeout":
		sentinel = runtime.ErrUncertain
	case "server_not_running":
		sentinel = runtime.ErrUnavailable
	default:
		switch {
		case errors.Is(he.Err, context.DeadlineExceeded):
			sentinel = runtime.ErrUncertain // we gave up waiting; Herdr may still have acted
		case errors.Is(he.Err, exec.ErrNotFound), errors.Is(he.Err, fs.ErrNotExist):
			sentinel = runtime.ErrUnavailable
		default:
			return err
		}
	}
	return fmt.Errorf("%w: %w", sentinel, err)
}

type agentInfo struct {
	Name       string `json:"name"`
	Agent      string `json:"agent"`
	Status     string `json:"agent_status"`
	PaneID     string `json:"pane_id"`
	TerminalID string `json:"terminal_id"`
}

func (a agentInfo) session(name string) *runtime.AgentSession {
	return &runtime.AgentSession{Name: name, Kind: a.Agent, PaneID: a.PaneID, TerminalID: a.TerminalID, Status: runtime.AgentStatus(a.Status)}
}

var (
	versionRE = regexp.MustCompile(`\d+\.\d+\.\d+\S*`)
	kindsRE   = regexp.MustCompile(`possible values: ([^\]]+)\]`)
)

// Capabilities asks the installed Herdr what it supports instead of assuming:
// the version, that a server is reachable, and the agent kinds `agent start`
// accepts. A kind only counts if its executable is on PATH, since Herdr
// launches agents by their canonical executable name.
func (a *Adapter) Capabilities(ctx context.Context) (*runtime.Capabilities, error) {
	out, err := a.cli.run(ctx, "version", []string{"--version"})
	if err != nil {
		return nil, classify(err)
	}
	caps := &runtime.Capabilities{Version: versionRE.FindString(string(out))}

	if err := a.cli.result(ctx, "workspace list", nil, []string{"workspace", "list"}); err != nil {
		return nil, classify(err)
	}
	help, err := a.cli.run(ctx, "agent start --help", []string{"agent", "start", "--help"})
	if err != nil {
		return nil, classify(err)
	}
	m := kindsRE.FindSubmatch(help)
	if m == nil {
		return nil, &Error{Op: "agent start --help", Message: "could not find the supported agent kinds in the help output; this Herdr version is not supported"}
	}
	for _, k := range strings.Split(string(m[1]), ",") {
		k = strings.TrimSpace(k)
		if _, err := exec.LookPath(k); err == nil {
			caps.Kinds = append(caps.Kinds, k)
		} else {
			caps.Missing = append(caps.Missing, k)
		}
	}
	return caps, nil
}

func (a *Adapter) CreateWorkspace(ctx context.Context, cwd, label string) (*runtime.Workspace, error) {
	var res struct {
		Workspace struct {
			ID string `json:"workspace_id"`
		} `json:"workspace"`
		RootPane struct {
			ID string `json:"pane_id"`
		} `json:"root_pane"`
	}
	err := a.cli.result(ctx, "workspace create", &res, []string{"workspace", "create", "--cwd", cwd, "--label", label, "--no-focus"})
	if err != nil {
		return nil, classify(err)
	}
	if res.Workspace.ID == "" || res.RootPane.ID == "" {
		return nil, &Error{Op: "workspace create", Message: "response has no workspace or root pane id"}
	}
	return &runtime.Workspace{ID: res.Workspace.ID, PaneID: res.RootPane.ID}, nil
}

func (a *Adapter) StartAgent(ctx context.Context, kind, paneID, name string, args []string) (*runtime.AgentSession, error) {
	argv := []string{"agent", "start", name, "--kind", kind, "--pane", paneID, "--timeout", strconv.FormatInt(a.startTimeout.Milliseconds(), 10)}
	if len(args) > 0 {
		argv = append(append(argv, "--"), args...)
	}
	// Herdr waits up to startTimeout itself; give the process a little longer.
	c := a.cli
	c.timeout = a.startTimeout + 10*time.Second
	var res struct {
		Agent agentInfo `json:"agent"`
	}
	err := classify(c.result(ctx, "agent start", &res, argv))
	if err == nil {
		return res.Agent.session(name), nil
	}
	// Blocked at startup, or we stopped waiting: the agent may well exist.
	// Report what is actually there rather than guessing.
	if errors.Is(err, runtime.ErrNotReady) || errors.Is(err, runtime.ErrUncertain) {
		var got struct {
			Agent agentInfo `json:"agent"`
		}
		if gerr := a.cli.result(ctx, "agent get", &got, []string{"agent", "get", name}); gerr == nil {
			s := got.Agent.session(name)
			if s.Status.ReadyForInput() {
				return s, nil
			}
			return s, fmt.Errorf("%w: %w", runtime.ErrNotReady, err)
		}
	}
	return nil, err
}

func (a *Adapter) SendPrompt(ctx context.Context, agent, prompt string) error {
	return classify(a.cli.result(ctx, "agent prompt", nil, []string{"agent", "prompt", agent, prompt}, 3))
}

func (a *Adapter) GetAgentStatus(ctx context.Context, agent string) (runtime.AgentStatus, error) {
	var res struct {
		Agent agentInfo `json:"agent"`
	}
	if err := a.cli.result(ctx, "agent get", &res, []string{"agent", "get", agent}); err != nil {
		return "", classify(err)
	}
	switch s := runtime.AgentStatus(res.Agent.Status); s {
	case runtime.StatusIdle, runtime.StatusWorking, runtime.StatusBlocked, runtime.StatusDone, runtime.StatusUnknown:
		return s, nil
	default:
		return runtime.StatusUnknown, nil // a status this adapter predates proves nothing
	}
}

func (a *Adapter) ReadAgentOutput(ctx context.Context, agent string) (string, error) {
	out, err := a.cli.run(ctx, "agent read", []string{"agent", "read", agent, "--source", "recent-unwrapped"})
	if err != nil {
		return "", classify(err)
	}
	return string(out), nil
}

func (a *Adapter) StopAgent(ctx context.Context, workspaceID string) error {
	err := classify(a.cli.result(ctx, "workspace close", nil, []string{"workspace", "close", workspaceID}))
	if errors.Is(err, runtime.ErrNotFound) {
		return nil
	}
	return err
}

// AttachCommand implements runtime.Attacher: `herdr [--session S] agent attach NAME`.
func (a *Adapter) AttachCommand(agent string) ([]string, error) {
	if agent == "" {
		return nil, errors.New("attach: empty agent name")
	}
	argv := []string{a.cli.exe}
	if a.cli.session != "" {
		argv = append(argv, "--session", a.cli.session)
	}
	return append(argv, "agent", "attach", agent), nil
}
