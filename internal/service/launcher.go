package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	c "caffe/internal/contracts"
	"caffe/internal/persistence"
	"caffe/internal/runtime"
)

type StartRunRequest struct {
	Request  string
	PlanFile string
}

const launchTimeout = 10 * time.Second

func (s *Service) executable() (string, error) {
	if s.exe != "" {
		return s.exe, nil
	}
	return os.Executable()
}

func (s *Service) StartRun(ctx context.Context, req StartRunRequest) (string, error) {
	if strings.TrimSpace(req.Request) == "" {
		return "", errors.New("empty request")
	}
	id := uuid.NewString()
	args := []string{"run", "--run-id", id}
	if req.PlanFile != "" {
		abs, err := filepath.Abs(req.PlanFile)
		if err != nil {
			return "", err
		}
		args = append(args, "--plan", abs)
	}

	args = append(args, "--", req.Request)
	err := s.launch(ctx, id, args, func(ctx context.Context) (bool, error) {
		_, err := s.store.GetRun(ctx, id)
		if err == nil {
			return true, nil
		}
		return false, ignoreNotFound(err)
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *Service) ResumeRun(ctx context.Context, runID string) error {
	run, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return notFound(err)
	}
	if run.Status != c.RunInterrupted {
		return fmt.Errorf("run %s is %s; only interrupted runs can be resumed", run.ID, run.Status)
	}
	return s.launch(ctx, run.ID, []string{"resume", run.ID}, func(ctx context.Context) (bool, error) {
		r, err := s.store.GetRun(ctx, run.ID)
		if err != nil {
			return false, err
		}
		return r.Status != c.RunInterrupted, nil
	})
}

func ignoreNotFound(err error) error {
	if errors.Is(err, persistence.ErrNotFound) {
		return nil
	}
	return err
}

func (s *Service) launch(ctx context.Context, runID string, args []string, ready func(context.Context) (bool, error)) error {
	exe, err := s.executable()
	if err != nil {
		return fmt.Errorf("locate caffe executable: %w", err)
	}
	dir := filepath.Join(s.cfg.ArtifactDir(), runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	logPath := filepath.Join(dir, "console.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()

	cmd := exec.Command(exe, append([]string{"-C", s.cfg.Project.Path}, args...)...)
	cmd.Dir = s.cfg.Project.Path
	cmd.Stdin = nil
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = detachedAttr()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", exe, err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	deadline := time.NewTimer(launchTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		ok, err := ready(ctx)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		select {
		case werr := <-exited:

			if ok, err := ready(context.WithoutCancel(ctx)); err == nil && ok {
				return nil
			}
			return fmt.Errorf("caffe %s exited before the run started (%v): %s", args[0], werr, consoleTail(logPath))
		case <-deadline.C:
			return fmt.Errorf("caffe %s did not start the run within %s; it is still running (pid %d), see %s",
				args[0], launchTimeout, cmd.Process.Pid, logPath)
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

func consoleTail(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "(no output)"
	}
	text := strings.TrimSpace(string(data))
	if len(text) > 2000 {
		text = "…" + text[len(text)-2000:]
	}
	if text == "" {
		return "(no output)"
	}
	return runtime.Redact(strings.ToValidUTF8(text, ""))
}
