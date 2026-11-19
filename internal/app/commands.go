package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"caffe/internal/config"
	c "caffe/internal/contracts"
	"caffe/internal/planner"
	"caffe/internal/service"
	"caffe/internal/studio"
)

var ErrRunNotCompleted = errors.New("run did not complete")

func NewRootCommand() *cobra.Command {
	var dir string
	var verbose bool
	root := &cobra.Command{
		Use:   "caffe",
		Short: "Caffè — a local-first orchestrator for autonomous coding agents",
		Long: "Caffè — brew better software.\n\n" +
			"A local-first orchestrator for autonomous coding agents. Caffè plans a request into a task graph,\n" +
			"delegates the tasks to specialized agents running in an external agent runtime, isolates their\n" +
			"changes in Git worktrees and validates the results. It never merges: that decision stays with you.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVarP(&dir, "project", "C", ".", "project directory (or any directory below it)")
	root.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "also write debug logs to stderr")

	with := func(fn func(cmd *cobra.Command, a *App, args []string) error) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, args []string) error {
			a, err := Open(cmd.Context(), dir, verbose)
			if err != nil {
				return err
			}
			for _, id := range a.Interrupted {
				fmt.Fprintf(cmd.ErrOrStderr(), "note: run %s was interrupted (its orchestrator process died). `caffe inspect %s`, then `caffe resume %s` or `caffe cancel %s`.\n",
					id, id[:8], id[:8], id[:8])
			}
			return errors.Join(fn(cmd, a, args), a.Close())
		}
	}
	progress := func(out io.Writer) func(string) {
		return func(line string) { fmt.Fprintf(out, "[%s] %s\n", time.Now().Format("15:04:05"), line) }
	}
	finish := func(cmd *cobra.Command, run *c.Run, err error) error {
		if run != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "\nRun %s finished: %s%s\nDetails: caffe inspect %s\n", run.ID, run.Status, prefixed(" — ", run.Error), run.ID[:8])
		}
		if err == nil && run != nil && run.Status != c.RunCompleted {
			return ErrRunNotCompleted
		}
		return err
	}

	root.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "Initialize Caffè in the current Git repository",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			created, path, err := Init(cmd.Context(), dir)
			if err != nil {
				return err
			}
			if created {
				fmt.Fprintf(cmd.OutOrStdout(), "Initialized Caffè in %s/.caffe\nNext: `caffe agents` to see what can run here, then edit .caffe/config.yaml to choose agents and validation commands.\n", path)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Caffè is already initialized in %s; the configuration is valid\n", path)
			}
			return nil
		},
	})

	var planFile, runID string
	var detach bool
	runCmd := &cobra.Command{
		Use:   "run <request>",
		Short: "Plan and execute a request with multiple agents",
		Long: "Plan and execute a request. Ctrl-C cancels the run and stops its agents.\n" +
			"With --plan, a plan file (same JSON format the planner produces) is used instead of asking the planner agent.\n" +
			"With --run-id, the run gets that id (a UUID no run has), so a launcher knows it before the run starts.\n" +
			"With --detach, the run is started as a separate background process and the command returns at once\n" +
			"with its id; follow it with `caffe status` and stop it with `caffe cancel`.",
		Args: cobra.ExactArgs(1),
		RunE: with(func(cmd *cobra.Command, a *App, args []string) error {
			if detach {
				if runID != "" {
					return errors.New("--detach chooses the run id itself; it cannot be combined with --run-id")
				}
				id, err := a.Svc.StartRun(cmd.Context(), service.StartRunRequest{Request: args[0], PlanFile: planFile})
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Run %s started in the background.\nFollow it: caffe status · caffe inspect %s · stop it: caffe cancel %s\n", id, id[:8], id[:8])
				return nil
			}
			var preset *c.ExecutionPlan
			if planFile != "" {
				data, err := os.ReadFile(planFile)
				if err != nil {
					return err
				}
				if preset, err = planner.ParsePlan(data, 0); err != nil {
					return fmt.Errorf("%s: %w", planFile, err)
				}
			}
			a.Orch.Progress = progress(cmd.OutOrStdout())
			run, err := a.Orch.RunWithID(cmd.Context(), runID, args[0], preset)
			return finish(cmd, run, err)
		}),
	}
	runCmd.Flags().StringVar(&planFile, "plan", "", "use this plan JSON file instead of the planner agent")
	runCmd.Flags().StringVar(&runID, "run-id", "", "use this UUID as the run id instead of generating one")
	runCmd.Flags().BoolVar(&detach, "detach", false, "start the run as a background process and return its id")
	root.AddCommand(runCmd)

	var studioCmd func(*cobra.Command, []string) error
	root.AddCommand(&cobra.Command{
		Use:   "studio",
		Short: "Open Caffè Studio, the terminal UI (early preview)",
		Long: "Caffè Studio supervises runs, agents and their worktrees from the terminal.\n" +
			"Runs started from it are separate processes: quitting Studio does not stop them.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := FindRoot(dir); err != nil {
				abs, _ := filepath.Abs(dir)
				return studio.Run(cmd.Context(), nil, studio.Project{Root: abs}, studio.Options{NoProject: true, Log: zerolog.Nop()})
			}
			return studioCmd(cmd, args)
		},
	})
	studioCmd = with(func(cmd *cobra.Command, a *App, _ []string) error {
		dir := filepath.Join(a.Cfg.Project.Path, config.Dir, "studio")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		logFile, err := os.OpenFile(filepath.Join(dir, "studio.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer logFile.Close()
		return studio.Run(cmd.Context(), a.Svc,
			studio.Project{Name: a.Cfg.Project.Name, Root: a.Cfg.Project.Path},
			studio.Options{Dir: dir, Theme: a.Cfg.Studio.Theme, Log: zerolog.New(logFile).With().Timestamp().Logger()})
	})

	root.AddCommand(&cobra.Command{
		Use:   "resume <run-id>",
		Short: "Continue an interrupted run without repeating finished work",
		Args:  cobra.ExactArgs(1),
		RunE: with(func(cmd *cobra.Command, a *App, args []string) error {
			a.Orch.Progress = progress(cmd.OutOrStdout())
			run, err := a.Orch.Resume(cmd.Context(), args[0])
			return finish(cmd, run, err)
		}),
	})

	root.AddCommand(&cobra.Command{
		Use:   "cancel <run-id>",
		Short: "Cancel a run and stop its agents",
		Args:  cobra.ExactArgs(1),
		RunE: with(func(cmd *cobra.Command, a *App, args []string) error {
			run, err := a.Orch.Cancel(cmd.Context(), args[0])
			if run != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Run %s cancelled\n", run.ID)
			}
			return err
		}),
	})

	root.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show active runs and what each task is waiting for",
		Args:  cobra.NoArgs,
		RunE: with(func(cmd *cobra.Command, a *App, _ []string) error {
			runs, err := a.Svc.Runs(cmd.Context(), true)
			if err != nil {
				return err
			}
			if len(runs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No active runs.")
				return nil
			}
			for _, r := range runs {
				fmt.Fprintf(cmd.OutOrStdout(), "Run %s  %s  %s\n", r.ID, r.Status, oneLine(r.Request, 70))
				if err := printTasks(cmd.Context(), cmd.OutOrStdout(), a, r.ID); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout())
			}
			return nil
		}),
	})

	root.AddCommand(&cobra.Command{
		Use:   "runs",
		Short: "List all runs",
		Args:  cobra.NoArgs,
		RunE: with(func(cmd *cobra.Command, a *App, _ []string) error {
			runs, err := a.Svc.Runs(cmd.Context(), false)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "RUN\tSTATUS\tCREATED\tDURATION\tREQUEST")
			for _, r := range runs {
				dur := "-"
				if r.StartedAt != nil && r.CompletedAt != nil {
					dur = r.CompletedAt.Sub(*r.StartedAt).Round(time.Second).String()
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.ID[:8], r.Status, r.CreatedAt.Local().Format("2006-01-02 15:04"), dur, oneLine(r.Request, 60))
			}
			return w.Flush()
		}),
	})

	root.AddCommand(&cobra.Command{
		Use:   "tasks <run-id>",
		Short: "Show a run's task graph and statuses",
		Args:  cobra.ExactArgs(1),
		RunE: with(func(cmd *cobra.Command, a *App, args []string) error {
			return printTasks(cmd.Context(), cmd.OutOrStdout(), a, args[0])
		}),
	})

	var showEvents bool
	inspect := &cobra.Command{
		Use:   "inspect <run-id>",
		Short: "Show everything recorded about a run",
		Args:  cobra.ExactArgs(1),
		RunE: with(func(cmd *cobra.Command, a *App, args []string) error {
			snap, err := a.Svc.RunSnapshot(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			report, err := a.Svc.Report(cmd.Context(), snap.Run.ID)
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), report)
			if items, err := a.Svc.Integration(cmd.Context(), snap.Run.ID); err == nil {
				printDecisions(cmd.OutOrStdout(), items)
			}
			if !showEvents {
				return nil
			}
			events, err := a.Svc.Events(cmd.Context(), snap.Run.ID)
			if err != nil {
				return err
			}
			keys := map[string]string{}
			for _, t := range snap.Tasks {
				keys[t.ID] = t.Key
			}
			fmt.Fprintln(cmd.OutOrStdout(), "\n## Events")
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			for _, e := range events {
				var kv []string
				for _, k := range []string{"from", "reason", "error", "message", "command", "exit_code", "execution_id", "blocked"} {
					if v, ok := e.Payload[k]; ok && fmt.Sprint(v) != "" {
						kv = append(kv, fmt.Sprintf("%s=%v", k, oneLine(fmt.Sprint(v), 80)))
					}
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.CreatedAt.Local().Format("15:04:05.000"), e.Type, keys[e.TaskID], strings.Join(kv, " "))
			}
			return w.Flush()
		}),
	}
	inspect.Flags().BoolVar(&showEvents, "events", false, "also print the full event log")
	root.AddCommand(inspect)

	root.AddCommand(&cobra.Command{
		Use:   "agents",
		Short: "Show what the runtime can start and the agent sessions of active runs",
		Args:  cobra.NoArgs,
		RunE: with(func(cmd *cobra.Command, a *App, _ []string) error {
			out := cmd.OutOrStdout()
			caps, err := a.Svc.Capabilities(cmd.Context())
			if err != nil {
				fmt.Fprintf(out, "Runtime: NOT AVAILABLE (%v)\n", err)
			} else {
				fmt.Fprintf(out, "Runtime: herdr %s%s\nAvailable agent kinds: %s\n", caps.Version, prefixed(", session ", a.Cfg.Runtime.Session), strings.Join(caps.Kinds, ", "))
				if len(caps.Missing) > 0 {
					fmt.Fprintf(out, "Known to Herdr but not installed: %s\n", strings.Join(caps.Missing, ", "))
				}
				fmt.Fprintln(out, "Configured roles:")
				for _, role := range append([]c.Role{c.RolePlanner}, c.TaskRoles...) {
					kind := a.Cfg.Agent(role).Kind
					state := "ok"
					switch {
					case role == c.RoleValidator && len(a.Cfg.Validation.Commands) > 0:
						state = "unused: validation.commands are run by the orchestrator"
					case !caps.Supports(kind):
						state = "NOT AVAILABLE"
					}
					fmt.Fprintf(out, "  %-11s %-10s %s\n", role, kind, state)
				}
			}
			runs, err := a.Svc.Runs(cmd.Context(), true)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			n := 0
			for _, r := range runs {
				snap, err := a.Svc.RunSnapshot(cmd.Context(), r.ID)
				if err != nil {
					return err
				}
				var live []service.AgentView
				for _, ag := range snap.Agents {
					if ag.Status != "stopped" {
						live = append(live, ag)
					}
				}

				obs := a.Svc.AgentRuntimeStatus(cmd.Context(), live)
				for _, ag := range live {
					if n++; n == 1 {
						fmt.Fprintln(w, "\nAGENT\tKIND\tROLE\tRUN\tPANE\tRUNTIME STATUS")
					}
					o := obs[ag.ID]
					state := "gone"
					switch {
					case o.Err != nil:
						state = "unknown (" + o.Err.Error() + ")"
					case o.Found:
						state = string(o.Status)
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", ag.Name, ag.Kind, ag.Role, r.ID[:8], ag.PaneID, state)
				}
			}
			if n == 0 {
				fmt.Fprintln(w, "\nNo agent sessions in active runs.")
			}
			return w.Flush()
		}),
	})

	var failed bool
	clean := &cobra.Command{
		Use:   "clean",
		Short: "Remove worktrees of finished runs (branches are kept)",
		Long: "Removes the worktrees of completed runs and closes leftover agent workspaces of finished runs.\n" +
			"Branches are never deleted. Worktrees with uncommitted changes are never removed.\n" +
			"Worktrees of failed or cancelled runs are kept unless --failed is given.",
		Args: cobra.NoArgs,
		RunE: with(func(cmd *cobra.Command, a *App, _ []string) error {
			actions, err := a.Orch.Clean(cmd.Context(), failed)
			for _, line := range actions {
				fmt.Fprintln(cmd.OutOrStdout(), line)
			}
			if len(actions) == 0 && err == nil {
				fmt.Fprintln(cmd.OutOrStdout(), "Nothing to clean.")
			}
			return err
		}),
	}
	clean.Flags().BoolVar(&failed, "failed", false, "also remove worktrees of failed and cancelled runs")
	root.AddCommand(clean)

	root.AddCommand(&cobra.Command{
		Use:   "config",
		Short: "Show the effective, validated configuration",
		Long:  "Prints the configuration after defaults and validation. To change it, edit .caffe/config.yaml; every command validates it on startup.",
		Args:  cobra.NoArgs,
		RunE: with(func(cmd *cobra.Command, a *App, _ []string) error {
			data, err := yaml.Marshal(a.Cfg)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "# effective configuration (paths resolved)\n%s", data)
			return nil
		}),
	})
	root.AddCommand(integrateCmd(with))
	return root
}

func printTasks(ctx context.Context, out io.Writer, a *App, runID string) error {
	snap, err := a.Svc.RunSnapshot(ctx, runID)
	if err != nil {
		return err
	}
	if len(snap.Tasks) == 0 {
		fmt.Fprintf(out, "  (no tasks yet: run is %s)\n", snap.Run.Status)
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "  TASK\tROLE\tSTATUS\tTRY\tDEPENDS ON\tDETAIL")
	for _, t := range snap.Tasks {
		detail := t.Reason
		switch t.Status {
		case c.TaskCompleted:
			detail = t.BranchName
		case c.TaskBlocked:

			detail += " in its Herdr pane"
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\t%d/%d\t%s\t%s\n", t.Key, t.Role, t.Status, t.Attempts, t.MaxAttempts, strings.Join(t.DependsOn, ","), oneLine(detail, 90))
	}
	return w.Flush()
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}

func prefixed(prefix, s string) string {
	if s == "" {
		return ""
	}
	return prefix + s
}
