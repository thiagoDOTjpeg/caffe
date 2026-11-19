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

var ErrUnavailable = errors.New("unavailable")

type OutputSource string

const (
	OutputLive     OutputSource = "live"
	OutputCaptured OutputSource = "captured"
	OutputNone     OutputSource = "none"
)

type AgentOutput struct {
	AgentID    string
	Source     OutputSource
	Text       string
	Truncated  bool
	ObservedAt time.Time
}

const maxCapturedBytes = 1 << 20

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

func (s *Service) capturedOutputPath(ctx context.Context, a *c.Agent) (string, error) {
	snap, err := s.store.RunSnapshot(ctx, a.RunID)
	if err != nil {
		return "", notFound(err)
	}
	var latest *c.Execution
	for _, e := range snap.Executions {
		if e.AgentID == a.ID {
			latest = e
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

func lastLines(text string, n int) (string, bool) {
	text = strings.TrimRight(text, "\n")
	all := strings.Split(text, "\n")
	if len(all) <= n {
		return text, false
	}
	return strings.Join(all[len(all)-n:], "\n"), true
}
