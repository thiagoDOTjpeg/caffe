package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/rs/zerolog"

	"caffe/internal/agents"
	"caffe/internal/config"
	c "caffe/internal/contracts"
	"caffe/internal/core"
	"caffe/internal/git"
	"caffe/internal/persistence"
	"caffe/internal/planner"
	"caffe/internal/runtime/herdr"
	"caffe/internal/service"
)

type App struct {
	Cfg *config.Config

	Svc *service.Service

	Orch *core.Orchestrator

	Interrupted []string

	store   *persistence.Store
	roStore *persistence.Store
	logFile *os.File
}

func FindRoot(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(config.Path(d)); err == nil {
			return d, nil
		}
		if d == filepath.Dir(d) {
			return "", fmt.Errorf("no Caffè project found in %s or any parent directory (run `caffe init`)", dir)
		}
	}
}

func newLogger(root string, verbose bool) (zerolog.Logger, *os.File, error) {
	if err := os.MkdirAll(filepath.Join(root, config.Dir), 0o755); err != nil {
		return zerolog.Logger{}, nil, err
	}
	f, err := os.OpenFile(filepath.Join(root, config.Dir, "caffe.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return zerolog.Logger{}, nil, err
	}
	var w io.Writer = f
	level := zerolog.InfoLevel
	if verbose {
		w = zerolog.MultiLevelWriter(f, zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: "15:04:05"})
		level = zerolog.DebugLevel
	}
	return zerolog.New(w).Level(level).With().Timestamp().Logger(), f, nil
}

func Open(ctx context.Context, dir string, verbose bool) (*App, error) {
	root, err := FindRoot(dir)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	log, logFile, err := newLogger(root, verbose)
	if err != nil {
		return nil, err
	}
	store, err := persistence.Open(ctx, cfg.Persistence.Path)
	if err != nil {
		return nil, errors.Join(err, logFile.Close())
	}
	gm, err := git.New(ctx, cfg.Project.Path, cfg.Git.WorktreeDirectory)
	if err != nil {
		return nil, errors.Join(err, store.Close(), logFile.Close())
	}
	rt := herdr.New(cfg.Runtime.Executable, cfg.Runtime.Session, cfg.Runtime.CommandTimeout, log)
	am := &agents.Manager{RT: rt, Poll: cfg.Runtime.PollInterval, Log: log,
		IdleNudgeAfter: cfg.Runtime.IdleNudgeAfter, IdleGiveUpAfter: cfg.Runtime.IdleGiveUpAfter}
	orch := &core.Orchestrator{Cfg: cfg, Store: store, RT: rt, Git: gm, Agents: am, Log: log}
	spec := cfg.Agent(c.RolePlanner)
	orch.Planner = &planner.AgentProvider{Agents: am, Kind: spec.Kind, Args: spec.Args, OnSession: orch.PlannerSession, OnBlocked: orch.PlannerBlocked}

	ro, err := persistence.OpenReadOnly(ctx, cfg.Persistence.Path)
	if err != nil {
		return nil, errors.Join(err, store.Close(), logFile.Close())
	}
	a := &App{Cfg: cfg, Svc: service.New(cfg, ro, rt, gm).WithEngine(orch).WithLeases(store), Orch: orch, store: store, roStore: ro, logFile: logFile}
	if a.Interrupted, err = orch.Recover(ctx); err != nil {
		return nil, errors.Join(err, a.Close())
	}
	return a, nil
}

func (a *App) Close() error {
	return errors.Join(a.roStore.Close(), a.store.Close(), a.logFile.Close())
}

func Init(ctx context.Context, dir string) (created bool, root string, err error) {
	if root, err = filepath.Abs(dir); err != nil {
		return false, "", err
	}
	cfg := config.Default(filepath.Base(root))
	if _, statErr := os.Stat(config.Path(root)); errors.Is(statErr, os.ErrNotExist) {
		gm, err := git.New(ctx, root, filepath.Join(root, cfg.Git.WorktreeDirectory))
		if err != nil {
			return false, root, err
		}
		if err := config.Write(root, cfg); err != nil {
			return false, root, err
		}

		if err := gm.ExcludeLocally(ctx, ".caffe/", ".caffe-report-*.json"); err != nil {
			return false, root, err
		}
		created = true
	} else if statErr != nil {
		return false, root, statErr
	}

	a, err := Open(ctx, root, false)
	if err != nil {
		return created, root, err
	}
	return created, root, a.Close()
}
