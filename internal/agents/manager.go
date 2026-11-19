package agents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"caffe/internal/contracts"
	"caffe/internal/runtime"
)

var (
	ErrNoReport = errors.New("agent stopped without writing its report")

	ErrAgentGone = errors.New("agent session no longer exists and left no report")
)

const (
	maxReportBytes     = 1 << 20
	workingGracePolls  = 10
	maxTransientErrors = 5

	DefaultIdleNudgeAfter  = 45 * time.Second
	DefaultIdleGiveUpAfter = 5 * time.Minute
)

type Session struct {
	Name        string
	Kind        string
	WorkspaceID string
	PaneID      string
	TerminalID  string
}

type Request struct {
	ExecutionID string
	Role        contracts.Role
	Kind        string
	Args        []string
	Cwd         string
	Prompt      string
	Dir         string

	Attach *Session

	OnSession func(Session)

	OnBlocked func(blocked bool)
}

type Result struct {
	Report     []byte
	ReportPath string
	OutputPath string
	Session    Session
}

type Manager struct {
	RT   runtime.Runtime
	Poll time.Duration
	Log  zerolog.Logger

	IdleNudgeAfter  time.Duration
	IdleGiveUpAfter time.Duration
}

func AgentName(executionID string, role contracts.Role) string {
	return "c" + strings.ReplaceAll(executionID, "-", "")[:8] + "-" + string(role)
}

func ReportPath(cwd, executionID string) string {
	return filepath.Join(cwd, ".caffe-report-"+strings.ReplaceAll(executionID, "-", "")[:8]+".json")
}

func (m *Manager) Execute(ctx context.Context, req Request) (*Result, error) {
	log := m.Log.With().Str("execution_id", req.ExecutionID).Str("role", string(req.Role)).Logger()
	reportPath := ReportPath(req.Cwd, req.ExecutionID)
	blocked := false
	setBlocked := func(b bool) {
		if b != blocked {
			blocked = b
			if req.OnBlocked != nil {
				req.OnBlocked(b)
			}
		}
	}

	var sess Session
	if req.Attach != nil {
		sess = *req.Attach
		log.Info().Str("agent", sess.Name).Msg("re-attaching to existing agent")
	} else {
		if err := os.MkdirAll(req.Dir, 0o755); err != nil {
			return nil, err
		}
		promptPath := filepath.Join(req.Dir, "prompt.md")
		if err := os.WriteFile(promptPath, []byte(req.Prompt), 0o600); err != nil {
			return nil, err
		}

		if err := os.Remove(reportPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		name := AgentName(req.ExecutionID, req.Role)
		ws, err := m.RT.CreateWorkspace(ctx, req.Cwd, name)
		if err != nil {
			return nil, fmt.Errorf("create workspace: %w", err)
		}
		sess = Session{Name: name, Kind: req.Kind, WorkspaceID: ws.ID, PaneID: ws.PaneID}
		if req.OnSession != nil {
			req.OnSession(sess)
		}
		as, err := m.RT.StartAgent(ctx, req.Kind, ws.PaneID, name, req.Args)
		if err != nil && !(errors.Is(err, runtime.ErrNotReady) && as != nil) {
			m.stop(sess, log)
			return nil, fmt.Errorf("start %s agent: %w", req.Kind, err)
		}
		sess.TerminalID = as.TerminalID
		if req.OnSession != nil {
			req.OnSession(sess)
		}
		log.Info().Str("agent", name).Str("kind", req.Kind).Str("workspace", ws.ID).Bool("ready", err == nil).Msg("agent started")
		if err := m.submit(ctx, sess.Name, promptPath, reportPath, setBlocked); err != nil {
			m.stop(sess, log)
			return nil, err
		}
	}

	report, err := m.await(ctx, sess.Name, reportPath, setBlocked, log)
	res := &Result{Session: sess, OutputPath: m.captureOutput(sess.Name, req.Dir)}
	m.stop(sess, log)
	if err != nil {
		return res, err
	}
	res.Report = report
	res.ReportPath = filepath.Join(req.Dir, "report.json")
	if err := os.MkdirAll(req.Dir, 0o755); err != nil {
		return res, err
	}
	if err := os.WriteFile(res.ReportPath, report, 0o644); err != nil {
		return res, err
	}
	if err := os.Remove(reportPath); err != nil {
		return res, fmt.Errorf("remove delivered report from working directory: %w", err)
	}
	return res, nil
}

func (m *Manager) submit(ctx context.Context, agent, promptPath, reportPath string, setBlocked func(bool)) error {
	text := fmt.Sprintf("Read the file %s and follow its instructions exactly. When you are done, write your JSON report to %s",
		promptPath, reportPath)
	marker := filepath.Base(reportPath)
	resent := false
	for {

		for {
			st, err := m.RT.GetAgentStatus(ctx, agent)
			if err != nil {
				return fmt.Errorf("agent not available for prompt: %w", err)
			}
			if st.ReadyForInput() {
				break
			}
			setBlocked(st == runtime.StatusBlocked)
			if err := sleep(ctx, m.Poll); err != nil {
				return err
			}
		}
		setBlocked(false)

		err := m.RT.SendPrompt(ctx, agent, text)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, runtime.ErrBlocked):
			continue
		case errors.Is(err, runtime.ErrUncertain):

			if st, serr := m.RT.GetAgentStatus(ctx, agent); serr == nil && st == runtime.StatusWorking {
				return nil
			}
			if out, rerr := m.RT.ReadAgentOutput(ctx, agent); rerr == nil && strings.Contains(out, marker) {
				return nil
			}
			if resent {
				return fmt.Errorf("prompt delivery could not be confirmed twice: %w", err)
			}
			resent = true
		default:
			return fmt.Errorf("send prompt: %w", err)
		}
	}
}

func (m *Manager) await(ctx context.Context, agent, reportPath string, setBlocked func(bool), log zerolog.Logger) ([]byte, error) {
	nudgeAfter, giveUpAfter := m.IdleNudgeAfter, m.IdleGiveUpAfter
	if nudgeAfter == 0 {
		nudgeAfter = DefaultIdleNudgeAfter
	}
	if giveUpAfter == 0 {
		giveUpAfter = DefaultIdleGiveUpAfter
	}
	var withReport, transient int
	var idleSince time.Time
	nudged := false
	for {
		st, err := m.RT.GetAgentStatus(ctx, agent)
		report, rerr := readReport(reportPath)
		switch {
		case ctx.Err() != nil:
			return nil, ctx.Err()
		case errors.Is(err, runtime.ErrNotFound):
			if rerr == nil {
				return report, nil
			}
			return nil, ErrAgentGone
		case err != nil:
			if transient++; transient > maxTransientErrors {
				return nil, fmt.Errorf("agent status: %w", err)
			}
			log.Warn().Err(err).Msg("agent status check failed, retrying")
		default:
			transient = 0
			setBlocked(st == runtime.StatusBlocked)
			if rerr == nil {

				if withReport++; st != runtime.StatusWorking || withReport > workingGracePolls {
					return report, nil
				}
			} else if st.ReadyForInput() {
				if idleSince.IsZero() {
					idleSince = time.Now()
				}
				switch idleFor := time.Since(idleSince); {
				case !nudged && idleFor >= nudgeAfter:
					nudged, idleSince = true, time.Time{}
					log.Warn().Str("agent", agent).Msg("agent idle without report, reminding once")
					msg := "You stopped without delivering your result. Write your JSON report to " + reportPath + " now, as instructed."
					if err := m.RT.SendPrompt(ctx, agent, msg); err != nil && !errors.Is(err, runtime.ErrUncertain) {
						return nil, fmt.Errorf("remind agent: %w", err)
					}
				case nudged && idleFor >= giveUpAfter:
					if !errors.Is(rerr, os.ErrNotExist) {
						return nil, fmt.Errorf("%w: %v", ErrNoReport, rerr)
					}
					return nil, ErrNoReport
				}
			} else {
				idleSince = time.Time{}
			}
		}
		if err := sleep(ctx, m.Poll); err != nil {
			return nil, err
		}
	}
}

func readReport(path string) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("report %s is not a regular file", path)
	}
	if fi.Size() > maxReportBytes {
		return nil, fmt.Errorf("report %s is larger than %d bytes", path, maxReportBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return contracts.ExtractJSON(data)
}

func (m *Manager) captureOutput(agent, dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := m.RT.ReadAgentOutput(ctx, agent)
	if err != nil || strings.TrimSpace(out) == "" {
		return ""
	}
	path := filepath.Join(dir, "output.txt")
	if os.MkdirAll(dir, 0o755) != nil || os.WriteFile(path, []byte(runtime.Redact(out)), 0o600) != nil {
		return ""
	}
	return path
}

func (m *Manager) stop(s Session, log zerolog.Logger) {
	if s.WorkspaceID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := m.RT.StopAgent(ctx, s.WorkspaceID); err != nil {
		log.Warn().Err(err).Str("agent", s.Name).Str("workspace", s.WorkspaceID).Msg("could not close agent workspace")
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
