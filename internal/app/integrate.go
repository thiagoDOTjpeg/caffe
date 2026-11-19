package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"caffe/internal/service"
)

type withFn = func(func(*cobra.Command, *App, []string) error) func(*cobra.Command, []string) error

func integrateCmd(with withFn) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "integrate <run-id> [task-key approve|reject [note…] | task-key merge]",
		Short: "Decide what happens to the branches of a run, and merge the approved ones",
		Long: "Without a task, lists each developer branch of the run with its review, validation and decision.\n" +
			"approve/reject records a decision. merge shows the exact git command and merges into the branch\n" +
			"checked out in the main checkout, only if the task is approved, the checkout is clean and nothing\n" +
			"conflicts. Nothing is ever pushed.",
		Args: cobra.MinimumNArgs(1),
		RunE: with(func(cmd *cobra.Command, a *App, args []string) error {
			ctx, out := cmd.Context(), cmd.OutOrStdout()
			snap, err := a.Svc.RunSnapshot(ctx, args[0])
			if err != nil {
				return err
			}
			if len(args) == 1 {
				items, err := a.Svc.Integration(ctx, snap.Run.ID)
				if err != nil {
					return err
				}
				printIntegration(out, items)
				return nil
			}
			if len(args) < 3 {
				return errors.New("give a task key and approve, reject or merge")
			}
			taskID := ""
			for _, t := range snap.Tasks {
				if t.Key == args[1] {
					taskID = t.ID
				}
			}
			if taskID == "" {
				return fmt.Errorf("run %s has no task %q", snap.Run.ID[:8], args[1])
			}
			switch args[2] {
			case service.DecisionApprove, service.DecisionReject:
				if err := a.Svc.Decide(ctx, taskID, args[2], strings.Join(args[3:], " ")); err != nil {
					return err
				}
				fmt.Fprintf(out, "%s: %s recorded\n", args[1], args[2])
				return nil
			case "merge":
				p, err := a.Svc.PreviewMerge(ctx, taskID)
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "Merge %s into %s\n  %s\n", p.Branch, p.Target, p.Command)
				for _, c := range p.Conflicts {
					fmt.Fprintf(out, "  conflict: %s\n", c)
				}
				if !p.Ready() {
					return fmt.Errorf("refused: %s", strings.Join(p.Blockers, "; "))
				}
				if !yes && a.Cfg.Security.RequireConfirmationForMerge {
					fmt.Fprint(out, "Merge now? [y/N] ")
					line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
					if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
						fmt.Fprintln(out, "Not merged.")
						return nil
					}
				}
				if _, err := a.Svc.Merge(ctx, taskID); err != nil {
					return err
				}
				fmt.Fprintf(out, "Merged %s into %s (not pushed).\n", p.Branch, p.Target)
				return nil
			}
			return fmt.Errorf("unknown action %q: approve, reject or merge", args[2])
		}),
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "merge without asking (security.require_confirmation_for_merge)")
	return cmd
}

func printIntegration(out io.Writer, items []service.IntegrationItem) {
	if len(items) == 0 {
		fmt.Fprintln(out, "No developer branches in this run.")
		return
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "TASK\tBRANCH\tCHANGES\tREVIEW\tVALIDATION\tMANUAL\tDECISION")
	for _, it := range items {
		dec := "—"
		if it.Decision != nil {
			dec = it.Decision.Decision
			if it.Decision.Note != "" {
				dec += ": " + oneLine(it.Decision.Note, 40)
			}
		}
		if it.Merged && (it.Decision == nil || it.Decision.Decision != service.DecisionMerged) {
			dec += " (already in " + it.Target + ")"
		}
		stat := strings.TrimSpace(it.Stat)
		if stat == "" {
			stat = "no changes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n", it.Task.Key, it.Task.BranchName, stat, it.Review, it.Validation, it.Manual, dec)
	}
	w.Flush()
}

func printDecisions(out io.Writer, items []service.IntegrationItem) {
	var any bool
	for _, it := range items {
		any = any || len(it.History) > 0
	}
	if !any {
		return
	}
	fmt.Fprintln(out, "\n## Integration")
	for _, it := range items {
		for _, d := range it.History {
			note := ""
			if d.Note != "" {
				note = " — " + d.Note
			}
			fmt.Fprintf(out, "- %s %s: %s by %s%s\n", d.At.Local().Format("2006-01-02 15:04"), it.Task.Key, d.Decision, d.Author, note)
		}
	}
}
