package process

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"

	"caffe/internal/proctree"
	"caffe/internal/runtime"
)

type Command func(prompt string, args []string) []string

func prefixed(prefix []string, tail ...string) Command {
	return func(prompt string, args []string) []string {
		argv := append(append(append([]string(nil), prefix...), args...), tail...)
		return append(argv, prompt)
	}
}

var Builtin = map[string]Command{
	"claude":   prefixed([]string{"claude"}, "-p"),
	"codex":    prefixed([]string{"codex", "exec"}),
	"gemini":   prefixed([]string{"gemini"}, "-p"),
	"opencode": prefixed([]string{"opencode", "run"}),
}

const maxOutput = 256 << 10

type Adapter struct {
	dir   string
	kinds map[string]Command
}

var (
	_ runtime.Runtime  = (*Adapter)(nil)
	_ runtime.Attacher = (*Adapter)(nil)
)

func New(dir string, kinds map[string]Command) *Adapter {
	return &Adapter{dir: dir, kinds: kinds}
}

type launch struct {
	Cwd  string   `json:"cwd"`
	Kind string   `json:"kind"`
	Args []string `json:"args"`
}

func (a *Adapter) path(name string, file ...string) (string, error) {
	if name == "" || name != filepath.Base(name) || name == "." || name == ".." {
		return "", fmt.Errorf("process runtime: invalid agent name %q", name)
	}
	return filepath.Join(append([]string{a.dir, name}, file...)...), nil
}

func (a *Adapter) Capabilities(context.Context) (*runtime.Capabilities, error) {
	caps := &runtime.Capabilities{Version: "built-in"}
	for kind, cmd := range a.kinds {
		if _, err := exec.LookPath(cmd("", nil)[0]); err == nil {
			caps.Kinds = append(caps.Kinds, kind)
		} else {
			caps.Missing = append(caps.Missing, kind)
		}
	}
	sort.Strings(caps.Kinds)
	sort.Strings(caps.Missing)
	return caps, nil
}

func (a *Adapter) CreateWorkspace(_ context.Context, cwd, label string) (*runtime.Workspace, error) {
	dir, err := a.path(label)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(a.dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(dir, "launch.json"), launch{Cwd: cwd}); err != nil {
		return nil, err
	}
	return &runtime.Workspace{ID: label, PaneID: label}, nil
}

func (a *Adapter) StartAgent(_ context.Context, kind, paneID, name string, args []string) (*runtime.AgentSession, error) {
	cmd, ok := a.kinds[kind]
	if !ok {
		return nil, fmt.Errorf("process runtime cannot run agent kind %q headless", kind)
	}
	if _, err := exec.LookPath(cmd("", nil)[0]); err != nil {
		return nil, fmt.Errorf("%w: %w", runtime.ErrUnavailable, err)
	}
	p, err := a.path(paneID, "launch.json")
	if err != nil {
		return nil, err
	}
	var l launch
	if err := readJSON(p, &l); err != nil {
		return nil, fmt.Errorf("%w: %w", runtime.ErrNotFound, err)
	}
	l.Kind, l.Args = kind, args
	if err := writeJSON(p, l); err != nil {
		return nil, err
	}
	return &runtime.AgentSession{Name: name, Kind: kind, PaneID: paneID, Status: runtime.StatusIdle}, nil
}

func (a *Adapter) SendPrompt(_ context.Context, agent, prompt string) error {
	dir, err := a.path(agent)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, "pid")); err == nil {
		return errors.New("process runtime: the agent already received its prompt and takes no further input")
	}
	var l launch
	if err := readJSON(filepath.Join(dir, "launch.json"), &l); err != nil {
		return fmt.Errorf("%w: %w", runtime.ErrNotFound, err)
	}
	build, ok := a.kinds[l.Kind]
	if !ok {
		return fmt.Errorf("process runtime: agent %s was never started", agent)
	}
	argv := build(prompt, l.Args)
	out, err := os.OpenFile(filepath.Join(dir, "output.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(context.Background(), argv[0], argv[1:]...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = l.Cwd, out, out
	if _, err := proctree.Start(cmd); err != nil {
		out.Close()
		return fmt.Errorf("start %s: %w", l.Kind, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600); err != nil {
		proctree.KillGroup(cmd.Process.Pid)
		out.Close()
		return err
	}
	go func() {
		err := cmd.Wait()
		fmt.Fprintf(out, "\n[caffe] %s exited: %v\n", l.Kind, exitText(err))
		out.Close()
	}()
	return nil
}

func exitText(err error) string {
	if err == nil {
		return "exit status 0"
	}
	return err.Error()
}

func (a *Adapter) pid(agent string) (int, error) {
	dir, err := a.path(agent)
	if err != nil {
		return 0, err
	}
	if _, err := os.Stat(dir); err != nil {
		return 0, runtime.ErrNotFound
	}
	b, err := os.ReadFile(filepath.Join(dir, "pid"))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

func (a *Adapter) GetAgentStatus(_ context.Context, agent string) (runtime.AgentStatus, error) {
	pid, err := a.pid(agent)
	switch {
	case err != nil:
		return "", err
	case pid == 0:
		return runtime.StatusIdle, nil
	case proctree.Alive(pid):
		return runtime.StatusWorking, nil
	}
	return "", runtime.ErrNotFound
}

func (a *Adapter) ReadAgentOutput(_ context.Context, agent string) (string, error) {
	p, err := a.path(agent, "output.log")
	if err != nil {
		return "", err
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		if _, serr := os.Stat(filepath.Dir(p)); serr != nil {
			return "", runtime.ErrNotFound
		}
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() > maxOutput {
		if _, err := f.Seek(-maxOutput, io.SeekEnd); err != nil {
			return "", err
		}
	}
	b, err := io.ReadAll(f)
	return string(b), err
}

func (a *Adapter) StopAgent(_ context.Context, workspaceID string) error {
	pid, err := a.pid(workspaceID)
	if errors.Is(err, runtime.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if pid != 0 && proctree.Alive(pid) {
		proctree.KillGroup(pid)
	}
	dir, _ := a.path(workspaceID)
	_ = os.RemoveAll(dir)
	return nil
}

func (a *Adapter) AttachCommand(agent string) ([]string, error) {
	p, err := a.path(agent, "output.log")
	if err != nil {
		return nil, err
	}
	if goruntime.GOOS == "windows" {
		return []string{"powershell", "-NoProfile", "-Command", "Get-Content -Wait -Path '" + strings.ReplaceAll(p, "'", "''") + "'"}, nil
	}
	return []string{"tail", "-n", "+1", "-F", p}, nil
}

func writeJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
