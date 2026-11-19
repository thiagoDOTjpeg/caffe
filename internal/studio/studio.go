package studio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"caffe/internal/service"
	"caffe/internal/studio/ui"
)

type Project struct {
	Name string
	Root string
}

type Options struct {
	Input  io.Reader
	Output io.Writer

	Dir string

	Theme  string
	Editor string
	Log    zerolog.Logger

	NoProject bool
}

func Run(ctx context.Context, svc *service.Service, p Project, opts Options) error {

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGHUP)
	defer stop()
	guard := &crashGuard{dir: opts.Dir, log: opts.Log}
	var src source
	if svc != nil {
		src = svc
	}
	m := newModel(src, p, guard)
	m.noProject = opts.NoProject
	m.ed.holder = "studio-" + uuid.NewString()
	m.editor = opts.Editor
	if svc != nil {
		m.terms = newTerminals(filepath.Join(opts.Dir, "sessions"))

		defer m.terms.mgr.StopAll()
	}
	if err := m.registry.Validate(); err != nil {
		return err
	}
	if svc != nil {
		w, err := svc.Watch(ctx, 0)
		if err != nil {
			m.live.watchErr = fmt.Errorf("watching for changes: %w", err)
		} else {
			m.changes, m.watcher = w.Changes(), w
		}
	}
	m.theme = ui.ThemeByName(opts.Theme, os.Getenv("NO_COLOR") != "")
	popts := []tea.ProgramOption{tea.WithContext(ctx)}
	if opts.Input != nil {
		popts = append(popts, tea.WithInput(opts.Input))
	}
	if opts.Output != nil {
		popts = append(popts, tea.WithOutput(opts.Output))
	}
	opts.Log.Info().Str("project", p.Root).Msg("studio started")
	final, err := tea.NewProgram(m, popts...).Run()
	if fm, ok := final.(model); ok && svc != nil {
		for ws, l := range fm.ed.leases {
			if rerr := svc.ReleaseLease(context.Background(), l); rerr != nil {
				fmt.Fprintf(os.Stderr, "caffè studio: the lease of %s stays: %v\n", ws, rerr)
			}
		}
	}
	switch {
	case err == nil:
		opts.Log.Info().Msg("studio quit")
		return nil
	case errors.Is(err, tea.ErrProgramPanic):
		if guard.file != "" {
			return fmt.Errorf("Caffè Studio crashed; the terminal was restored and the details are in %s", guard.file)
		}
		return fmt.Errorf("Caffè Studio crashed: %w", err)
	case ctx.Err() != nil && (errors.Is(err, tea.ErrProgramKilled) || errors.Is(err, context.Canceled)):
		opts.Log.Info().Msg("studio stopped by signal")
		return nil
	default:
		return err
	}
}

type crashGuard struct {
	dir  string
	log  zerolog.Logger
	file string
}

func (g *crashGuard) protect(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			g.record(r, debug.Stack())
			panic(r)
		}
	}()
	fn()
}

func (g *crashGuard) record(r any, stack []byte) {
	g.log.Error().Interface("panic", r).Msg("studio crashed")
	if g.dir == "" {
		return
	}
	if err := os.MkdirAll(g.dir, 0o755); err != nil {
		return
	}
	path := filepath.Join(g.dir, "crash-"+time.Now().UTC().Format("20060102T150405.000")+".log")
	body := fmt.Sprintf("Caffè Studio crash, %s\n\npanic: %v\n\n%s", time.Now().UTC().Format(time.RFC3339), r, stack)
	if os.WriteFile(path, []byte(body), 0o600) == nil {
		g.file = path
	}
}
