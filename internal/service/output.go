package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	c "caffe/internal/contracts"
	"caffe/internal/runtime"
)

// ErrUnavailable means the runtime could not be asked (not running, not
// reachable). Stored state is still available.
var ErrUnavailable = errors.New("unavailable")

// OutputSource says where agent output came from. Views must show it: live
// output can change, captured output is the agent's last screen.
type OutputSource string

const (
	OutputLive     OutputSource = "live"     // read from the runtime now
	OutputCaptured OutputSource = "captured" // saved when the execution ended
	OutputNone     OutputSource = "none"     // the agent is gone and nothing was saved
)

// AgentOutput is the recent terminal output of an agent, redacted.
type AgentOutput struct {
	AgentID    string
	Source     OutputSource
	Text       string
	Truncated  bool // more lines existed than were returned
	ObservedAt time.Time
}

// maxCapturedBytes bounds how much of a captured output file is read.
const maxCapturedBytes = 1 << 20

// AgentOutput returns the last `lines` lines of an agent's terminal: live from
// the runtime while the agent exists there, otherwise what the engine
// captured when its latest execution ended.
func (s *Service) AgentOutput(ctx context.Context, agentID string, lines int) (*AgentOutput, error) {
	if lines <= 0 {
		return nil, fmt.Errorf("AgentOutput: lines must be positive, got %d", lines)
	}
	a, err := s.store.GetAgent(ctx, agentID)
	if err != nil {
		return nil, notFound(err)
	}
	out := &AgentOutput{AgentID: a.ID, ObservedAt: s.now()}
	if a.Status != "stopped" {
		text, err := s.rt.ReadAgentOutput(ctx, a.Name)
		switch {
		case err == nil:
			out.Source = OutputLive
			out.Text, out.Truncated = lastLines(runtime.Redact(text), lines)
			return out, nil
		case !errors.Is(err, runtime.ErrNotFound):
			return nil, fmt.Errorf("%w: live output of agent %s: %w", ErrUnavailable, a.Name, err)
		}
		// Gone from the runtime although not recorded as stopped: fall back.
	}
	path, err := s.capturedOutputPath(ctx, a)
	if err != nil {
		return nil, err
	}
	if path == "" {
		out.Source = OutputNone
		return out, nil
	}
	text, err := readTail(path, maxCapturedBytes)
	if err != nil {
		return nil, err
	}
	out.Source = OutputCaptured
	out.Text, out.Truncated = lastLines(runtime.Redact(text), lines)
	return out, nil
}

// capturedOutputPath finds the output artifact of the agent's latest
// execution: a log artifact of that execution's task, in the execution's
// own artifact directory. "" when there is none (planning agents, or the
// output could not be read when the execution ended).
func (s *Service) capturedOutputPath(ctx context.Context, a *c.Agent) (string, error) {
	snap, err := s.store.RunSnapshot(ctx, a.RunID)
	if err != nil {
		return "", notFound(err)
	}
	var latest *c.Execution
	for _, e := range snap.Executions {
		if e.AgentID == a.ID {
			latest = e // oldest first
		}
	}
	if latest == nil {
		return "", nil
	}
	dirName := strings.ReplaceAll(latest.ID, "-", "")
	if len(dirName) > 8 {
		dirName = dirName[:8]
	}
	for _, art := range snap.Artifacts {
		if art.Type == c.ArtifactLog && art.TaskID == latest.TaskID && filepath.Base(filepath.Dir(art.Path)) == dirName {
			if !within(art.Path, s.cfg.ArtifactDir()) {
				return "", fmt.Errorf("output artifact of agent %s points outside the artifact directory: %s", a.Name, art.Path)
			}
			return art.Path, nil
		}
	}
	return "", nil
}

// readTail reads at most max bytes from the end of a file.
func readTail(path string, max int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	if fi.Size() > max {
		if _, err := f.Seek(-max, io.SeekEnd); err != nil {
			return "", err
		}
	}
	buf, err := io.ReadAll(io.LimitReader(f, max))
	if err != nil {
		return "", err
	}
	return strings.ToValidUTF8(string(buf), ""), nil
}

// lastLines keeps the last n lines of text.
func lastLines(text string, n int) (string, bool) {
	text = strings.TrimRight(text, "\n")
	all := strings.Split(text, "\n")
	if len(all) <= n {
		return text, false
	}
	return strings.Join(all[len(all)-n:], "\n"), true
}
