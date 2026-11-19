package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	c "caffe/internal/contracts"
	"caffe/internal/git"
	"caffe/internal/persistence"
)

const (
	DecisionApprove = persistence.DecisionApprove
	DecisionReject  = persistence.DecisionReject
	DecisionMerged  = persistence.DecisionMerged
)

type Decision struct {
	TaskID   string
	Decision string
	Note     string
	Author   string
	At       time.Time
}

type IntegrationItem struct {
	Task       c.Task
	Target     string
	Stat       string
	Review     string
	Validation string
	Manual     int
	Merged     bool
	Decision   *Decision
	History    []Decision
}

type MergePreview struct {
	RunID, TaskID, TaskKey string
	Branch, Target         string
	Command                string
	Conflicts              []string
	Blockers               []string
}

func (p *MergePreview) Ready() bool { return len(p.Blockers) == 0 }

func toDecision(d *persistence.Decision) Decision {
	return Decision{TaskID: d.TaskID, Decision: d.Decision, Note: d.Note, Author: d.Author, At: d.CreatedAt}
}

func (s *Service) target(ctx context.Context) (string, error) {
	if s.git == nil {
		return "", fmt.Errorf("integration: %w", ErrUnsupported)
	}
	b, err := s.git.Branch(ctx, s.git.Repo())
	if err != nil {
		return "", err
	}
	if b == "HEAD" {
		return "", errors.New("the main checkout is on a detached HEAD; check out the branch to integrate into")
	}
	return b, nil
}

func dependsOn(tasks []*c.Task, from, on string) bool {
	byID := map[string]*c.Task{}
	for _, t := range tasks {
		byID[t.ID] = t
	}
	seen := map[string]bool{}
	var walk func(id string) bool
	walk = func(id string) bool {
		if seen[id] {
			return false
		}
		seen[id] = true
		t := byID[id]
		if t == nil {
			return false
		}
		for _, d := range t.Dependencies {
			if d == on || walk(d) {
				return true
			}
		}
		return false
	}
	return walk(from)
}

func verdict(tasks []*c.Task, dev *c.Task, role c.Role, ok, bad string) string {
	out := "none"
	for _, t := range tasks {
		if t.Role != role || !dependsOn(tasks, t.ID, dev.ID) {
			continue
		}
		switch t.Status {
		case c.TaskCompleted:
			out = ok
		case c.TaskFailed, c.TaskCancelled:
			return bad
		default:
			out = string(t.Status)
		}
	}
	return out
}

func (s *Service) Integration(ctx context.Context, runID string) ([]IntegrationItem, error) {
	r, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return nil, notFound(err)
	}
	target, err := s.target(ctx)
	if err != nil {
		return nil, err
	}
	tasks, err := s.store.ListTasks(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	decisions, err := s.store.ListDecisions(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	var out []IntegrationItem
	for _, t := range tasks {
		if t.Role != c.RoleDeveloper || t.BranchName == "" {
			continue
		}
		it := IntegrationItem{Task: *t, Target: target,
			Review:     verdict(tasks, t, c.RoleReviewer, "approved", "rejected"),
			Validation: verdict(tasks, t, c.RoleValidator, "passed", "failed")}
		for _, d := range decisions {
			if d.TaskID == t.ID {
				dd := toDecision(d)
				it.History = append(it.History, dd)
				it.Decision = &dd
			}
		}
		it.Stat, _ = s.git.ShortStat(ctx, target, t.BranchName)
		subjects, _ := s.git.Subjects(ctx, target, t.BranchName)
		for _, sub := range subjects {
			if strings.HasPrefix(sub, "manual:") {
				it.Manual++
			}
		}
		it.Merged = s.git.IsAncestor(ctx, t.BranchName, target)
		out = append(out, it)
	}
	return out, nil
}

func (s *Service) devTask(ctx context.Context, taskID string) (*c.Task, error) {
	t, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return nil, notFound(err)
	}
	if t.Role != c.RoleDeveloper || t.BranchName == "" {
		return nil, fmt.Errorf("task %s has no branch to integrate", t.Key)
	}
	return t, nil
}

func (s *Service) Decide(ctx context.Context, taskID, decision, note string) error {
	if decision != DecisionApprove && decision != DecisionReject {
		return fmt.Errorf("decision must be %s or %s", DecisionApprove, DecisionReject)
	}
	if s.w == nil || s.git == nil {
		return fmt.Errorf("decide: %w", ErrUnsupported)
	}
	t, err := s.devTask(ctx, taskID)
	if err != nil {
		return err
	}
	return s.w.AddDecision(ctx, &persistence.Decision{RunID: t.RunID, TaskID: t.ID, Decision: decision,
		Note: strings.TrimSpace(note), Author: s.git.Identity(ctx)})
}

func (s *Service) PreviewMerge(ctx context.Context, taskID string) (*MergePreview, error) {
	t, err := s.devTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	target, err := s.target(ctx)
	if err != nil {
		return nil, err
	}
	p := &MergePreview{RunID: t.RunID, TaskID: t.ID, TaskKey: t.Key, Branch: t.BranchName, Target: target,
		Command: "git " + strings.Join(git.MergeArgs(t.BranchName), " ")}
	block := func(format string, a ...any) { p.Blockers = append(p.Blockers, fmt.Sprintf(format, a...)) }
	r, err := s.store.GetRun(ctx, t.RunID)
	if err != nil {
		return nil, notFound(err)
	}
	if !r.Status.Terminal() {
		block("run %s is still %s; its branches can still change", short8(r.ID), r.Status)
	}
	if t.Status != c.TaskCompleted {
		block("task %s is %s, not completed", t.Key, t.Status)
	}
	decisions, err := s.store.ListDecisions(ctx, t.RunID)
	if err != nil {
		return nil, err
	}
	last := ""
	for _, d := range decisions {
		if d.TaskID == t.ID {
			last = d.Decision
		}
	}
	switch last {
	case DecisionApprove:
	case DecisionMerged:
		block("already merged")
	case "":
		block("not approved yet")
	default:
		block("the latest decision is %s", last)
	}
	if s.git.IsAncestor(ctx, t.BranchName, target) && last != DecisionMerged {
		block("%s is already contained in %s", t.BranchName, target)
	}
	if dirty, err := s.git.IsDirty(ctx, s.git.Repo()); err != nil {
		return nil, err
	} else if dirty {
		block("the main checkout has uncommitted changes; commit or stash them first")
	}
	if p.Conflicts, err = s.git.MergeTree(ctx, target, t.BranchName); err != nil {
		return nil, err
	}
	if len(p.Conflicts) > 0 {
		block("merging conflicts in %d file(s)", len(p.Conflicts))
	}
	return p, nil
}

func (s *Service) Merge(ctx context.Context, taskID string) (*MergePreview, error) {
	if s.w == nil {
		return nil, fmt.Errorf("merge: %w", ErrUnsupported)
	}
	p, err := s.PreviewMerge(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if !p.Ready() {
		return p, fmt.Errorf("merge of %s refused: %s: %w", p.TaskKey, strings.Join(p.Blockers, "; "), ErrConflict)
	}
	if err := s.git.Merge(ctx, p.Branch); err != nil {
		return p, err
	}
	return p, s.w.AddDecision(context.WithoutCancel(ctx), &persistence.Decision{RunID: p.RunID, TaskID: p.TaskID,
		Decision: DecisionMerged, Note: "into " + p.Target, Author: s.git.Identity(ctx)})
}
