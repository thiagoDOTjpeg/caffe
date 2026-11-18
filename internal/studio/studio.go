// Package studio is Caffè Studio, the terminal UI (docs/studio/). It is a
// presentation layer only: everything it shows or does goes through
// internal/service. It must not import persistence, core, runtime or git;
// boundary_test.go enforces that.
package studio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rs/zerolog"

	"caffe/internal/service"
	"caffe/internal/studio/ui"
)

// Project identifies the project Studio is opened on.
type Project struct {
	Name string
	Root string
}

// Options configures a Studio session. Zero values mean a real terminal.
type Options struct {
	Input  io.Reader // nil: the terminal
	Output io.Writer // nil: the terminal
	// Dir holds Studio's own files (crash logs); usually .caffe/studio.
	Dir string
	// Theme is studio.theme from the configuration; NO_COLOR in the
	// environment forces the mono theme.
	Theme string
	Log   zerolog.Logger
}

// Run starts Studio and blocks until the user quits or ctx is cancelled
// (SIGINT/SIGTERM). The terminal is restored on every exit path, including a
// panic: Bubble Tea recovers it and restores the terminal, after the crash
// guard has written the panic and stack to Dir. Runs started from Studio are
// separate processes and are not affected by Studio exiting.
func Run(ctx context.Context, svc *service.Service, p Project, opts Options) error {
	guard := &crashGuard{dir: opts.Dir, log: opts.Log}
	m := newModel(svc, p, guard)
	if err := m.registry.Validate(); err != nil {
		return err // a programming error: refuse to start with ambiguous keys
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
	_, err := tea.NewProgram(m, popts...).Run()
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
		return nil // asked to stop: not a failure
	default:
		return err
	}
}

// crashGuard writes a crash log for a panic and re-raises it, so that Bubble
// Tea still restores the terminal and Run reports where the details are.
type crashGuard struct {
	dir  string
	log  zerolog.Logger
	file string // set once a crash was written
}

// protect runs fn; if fn panics, the panic is logged and re-raised.
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
