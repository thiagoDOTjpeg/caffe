package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	c "caffe/internal/contracts"
	"caffe/internal/persistence"
)

func BuildReport(ctx context.Context, store *persistence.Store, runID string) (string, error) {
	run, err := store.GetRun(ctx, runID)
	if err != nil {
		return "", err
	}
	tasks, err := store.ListTasks(ctx, run.ID)
	if err != nil {
		return "", err
	}
	execs, err := store.ListExecutions(ctx, run.ID)
	if err != nil {
		return "", err
	}
	arts, err := store.ListArtifacts(ctx, run.ID)
	if err != nil {
		return "", err
	}
	events, err := store.ListEvents(ctx, run.ID)
	if err != nil {
		return "", err
	}
	agents, err := store.ListAgents(ctx, run.ID)
	if err != nil {
		return "", err
	}
	agentByID := map[string]*c.Agent{}
	for _, a := range agents {
		agentByID[a.ID] = a
	}
	byID := map[string]*c.Task{}
	for _, t := range tasks {
		byID[t.ID] = t
	}

	var b strings.Builder
	w := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }

	w("# Run %s", run.ID)
	w("")
	w("- **Request:** %s", oneLine(run.UserRequest))
	w("- **Status:** %s%s", run.Status, ifNonEmpty(" — ", run.Error))
	w("- **Project:** `%s` (base commit `%s`)", run.ProjectPath, run.Metadata["base_commit"])
	w("- **Created:** %s", run.CreatedAt.Format(time.RFC3339))
	if run.StartedAt != nil && run.CompletedAt != nil {
		w("- **Duration:** %s", run.CompletedAt.Sub(*run.StartedAt).Round(time.Second))
	}

	w("")
	w("## Tasks")
	w("")
	w("| Task | Role | Status | Attempts | Duration | Depends on |")
	w("|---|---|---|---|---|---|")
	for _, t := range tasks {
		var deps []string
		for _, d := range t.Dependencies {
			deps = append(deps, byID[d].Key)
		}
		dur := "-"
		if t.StartedAt != nil && t.CompletedAt != nil {
			dur = t.CompletedAt.Sub(*t.StartedAt).Round(time.Second).String()
		}
		w("| %s | %s | %s | %d/%d | %s | %s |", t.Key, t.Role, t.Status, t.Attempts, t.MaxAttempts, dur, strings.Join(deps, ", "))
	}

	for _, t := range tasks {
		w("")
		w("### %s — %s", t.Key, t.Title)
		w("")
		w("%s, **%s**%s", t.Role, t.Status, ifNonEmpty(": ", t.Error))
		if t.BranchName != "" {
			w("")
			w("- Branch: `%s` (not merged; merging is your decision)", t.BranchName)
			w("- Worktree: `%s`", t.WorktreePath)
		}
		if t.Status == c.TaskPending || t.Status == c.TaskReady {
			var waiting []string
			for _, d := range t.Dependencies {
				if byID[d].Status != c.TaskCompleted {
					waiting = append(waiting, byID[d].Key+" ("+string(byID[d].Status)+")")
				}
			}
			if len(waiting) > 0 {
				w("- Waiting on: %s", strings.Join(waiting, ", "))
			}
		}

		var reportPath string
		for _, a := range arts {
			if a.TaskID == t.ID && a.Type == c.ArtifactReport {
				reportPath = a.Path
			}
		}
		if rep := LoadReport(reportPath); rep != nil {
			w("")
			w("%s", rep.Summary)
			if rep.Approved != nil {
				w("")
				w("Reviewer recommendation: **%s**", map[bool]string{true: "approve", false: "reject"}[*rep.Approved])
			}
			for _, f := range rep.Findings {
				loc := f.File
				if f.Line > 0 {
					loc = fmt.Sprintf("%s:%d", f.File, f.Line)
				}
				w("- [%s] %s %s", f.Severity, ifNonEmpty("`", loc, "` —"), oneLine(f.Description))
			}
			if ClaimsUnverified(t.Role, execs, t.ID) {
				for _, r := range rep.Tests {
					w("- %s: `%s` → exit %d", c.LabelClaimed, r.Command, r.ExitCode)
				}
			}
			for _, l := range rep.Limitations {
				w("- Limitation: %s", oneLine(l))
			}
		}
		for _, e := range execs {
			if e.TaskID != t.ID {
				continue
			}
			dur := "-"
			if e.CompletedAt != nil {
				dur = e.CompletedAt.Sub(e.StartedAt).Round(time.Second).String()
			}
			if e.ExitCode != nil {
				w("- Command `%s` → **exit %d** (%s, %s)%s", e.Prompt, *e.ExitCode, dur, c.LabelObserved, ifNonEmpty(" — ", e.Error))
				continue
			}
			who := "agent"
			if a := agentByID[e.AgentID]; a != nil {
				who = fmt.Sprintf("%s agent `%s` (%s)", a.Role, a.Name, a.Kind)
			}
			w("- Execution `%s` attempt %d: %s by %s in %s%s", short(e.ID), e.Attempt, e.Status, who, dur, ifNonEmpty(" — ", oneLine(e.Error)))
		}
	}

	var warnings []string
	for _, e := range events {
		if e.Type == c.EventWarning {
			warnings = append(warnings, fmt.Sprint(e.Payload["message"]))
		}
	}
	if len(warnings) > 0 {
		w("")
		w("## Warnings")
		w("")
		for _, m := range warnings {
			w("- %s", oneLine(m))
		}
	}

	w("")
	w("## Artifacts")
	w("")
	for _, a := range arts {
		key := ""
		if t := byID[a.TaskID]; t != nil {
			key = t.Key + ": "
		}
		w("- %s `%s` — %s%s", a.Type, a.Path, key, a.Description)
	}
	return b.String(), nil
}

func ClaimsUnverified(role c.Role, execs []*c.Execution, taskID string) bool {
	if role != c.RoleValidator {
		return true
	}
	for _, e := range execs {
		if e.TaskID == taskID && e.ExitCode != nil {
			return false
		}
	}
	return true
}

func LoadReport(path string) *c.Report {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var rep c.Report
	if json.Unmarshal(data, &rep) != nil || rep.Summary == "" {
		return nil
	}
	return &rep
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func ifNonEmpty(prefix, s string, suffix ...string) string {
	if s == "" {
		return ""
	}
	return prefix + s + strings.Join(suffix, "")
}
