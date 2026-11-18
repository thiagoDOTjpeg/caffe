package contracts

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ExecutionPlan is the structured output of the planner. It is untrusted input
// until Validate passes.
type ExecutionPlan struct {
	Goal  string     `json:"goal"`
	Tasks []PlanTask `json:"tasks"`
}

type PlanTask struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	Role               Role     `json:"role"`
	Dependencies       []string `json:"dependencies"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	Complexity         string   `json:"complexity,omitempty"`
}

// Task ids end up in branch names and directory names, so they are restricted
// to a slug rather than escaped.
var taskKeyRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

const maxPlanTasks = 50

// Validate rejects structurally invalid plans. It reports every problem found.
func (p *ExecutionPlan) Validate() error {
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }

	if len(p.Tasks) == 0 {
		add("plan has no tasks")
	}
	if len(p.Tasks) > maxPlanTasks {
		add("plan has %d tasks, limit is %d", len(p.Tasks), maxPlanTasks)
	}
	ids := map[string]bool{}
	for i, t := range p.Tasks {
		name := fmt.Sprintf("task[%d]", i)
		switch {
		case t.ID == "":
			add("%s: missing id", name)
		case !taskKeyRE.MatchString(t.ID):
			add("%s: id %q must match %s", name, t.ID, taskKeyRE)
		case ids[t.ID]:
			add("%s: duplicate id %q", name, t.ID)
		default:
			name = fmt.Sprintf("task %q", t.ID)
		}
		ids[t.ID] = true
		if !t.Role.ValidForTask() {
			add("%s: invalid role %q", name, t.Role)
		}
		if strings.TrimSpace(t.Title) == "" {
			add("%s: empty title", name)
		}
		if strings.TrimSpace(t.Description) == "" {
			add("%s: empty description", name)
		}
		if len(t.AcceptanceCriteria) == 0 {
			add("%s: no acceptance criteria", name)
		}
		for _, c := range t.AcceptanceCriteria {
			if strings.TrimSpace(c) == "" {
				add("%s: empty acceptance criterion", name)
			}
		}
	}
	for _, t := range p.Tasks {
		seen := map[string]bool{}
		for _, d := range t.Dependencies {
			switch {
			case d == t.ID:
				add("task %q depends on itself", t.ID)
			case !ids[d]:
				add("task %q depends on unknown task %q", t.ID, d)
			case seen[d]:
				add("task %q lists dependency %q twice", t.ID, d)
			}
			seen[d] = true
		}
	}
	if len(errs) == 0 {
		if cyc := p.cycle(); len(cyc) > 0 {
			add("cyclic dependencies among: %s", strings.Join(cyc, ", "))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid plan: %s", strings.Join(errs, "; "))
	}
	return nil
}

// cycle returns the tasks left over after Kahn's algorithm, i.e. those on or
// behind a cycle. Empty means the plan is a DAG.
func (p *ExecutionPlan) cycle() []string {
	indeg := map[string]int{}
	dependents := map[string][]string{}
	for _, t := range p.Tasks {
		indeg[t.ID] = len(t.Dependencies)
		for _, d := range t.Dependencies {
			dependents[d] = append(dependents[d], t.ID)
		}
	}
	var queue []string
	for id, n := range indeg {
		if n == 0 {
			queue = append(queue, id)
		}
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		delete(indeg, id)
		for _, dep := range dependents[id] {
			if indeg[dep]--; indeg[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}
	var left []string
	for id := range indeg {
		left = append(left, id)
	}
	sort.Strings(left)
	return left
}
