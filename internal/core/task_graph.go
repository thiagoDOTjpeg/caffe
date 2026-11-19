package core

import (
	"fmt"

	c "caffe/internal/contracts"
)

type Graph struct {
	tasks []*c.Task
	byID  map[string]*c.Task
}

func NewGraph(tasks []*c.Task) (*Graph, error) {
	g := &Graph{tasks: tasks, byID: make(map[string]*c.Task, len(tasks))}
	for _, t := range tasks {
		g.byID[t.ID] = t
	}
	for _, t := range tasks {
		for _, d := range t.Dependencies {
			if g.byID[d] == nil {
				return nil, fmt.Errorf("task %s depends on unknown task %s", t.Key, d)
			}
		}
	}

	state := map[string]int{}
	var visit func(t *c.Task) error
	visit = func(t *c.Task) error {
		switch state[t.ID] {
		case 1:
			return fmt.Errorf("dependency cycle through task %s", t.Key)
		case 2:
			return nil
		}
		state[t.ID] = 1
		for _, d := range t.Dependencies {
			if err := visit(g.byID[d]); err != nil {
				return err
			}
		}
		state[t.ID] = 2
		return nil
	}
	for _, t := range tasks {
		if err := visit(t); err != nil {
			return nil, err
		}
	}
	return g, nil
}

func (g *Graph) Tasks() []*c.Task { return g.tasks }

func (g *Graph) Deps(t *c.Task) []*c.Task {
	out := make([]*c.Task, 0, len(t.Dependencies))
	for _, d := range t.Dependencies {
		out = append(out, g.byID[d])
	}
	return out
}

func (g *Graph) Promotable() []*c.Task {
	var out []*c.Task
	for _, t := range g.tasks {
		if t.Status != c.TaskPending {
			continue
		}
		ok := true
		for _, d := range g.Deps(t) {
			ok = ok && d.Status == c.TaskCompleted
		}
		if ok {
			out = append(out, t)
		}
	}
	return out
}

func (g *Graph) Doomed() map[*c.Task]*c.Task {
	out := map[*c.Task]*c.Task{}
	for _, t := range g.tasks {
		if t.Status != c.TaskPending && t.Status != c.TaskReady {
			continue
		}
		for _, d := range g.Deps(t) {
			if d.Status == c.TaskFailed || d.Status == c.TaskCancelled {
				out[t] = d
				break
			}
		}
	}
	return out
}

func (g *Graph) Waiting(t *c.Task) []*c.Task {
	var out []*c.Task
	for _, d := range g.Deps(t) {
		if d.Status != c.TaskCompleted {
			out = append(out, d)
		}
	}
	return out
}

func (g *Graph) Nearest(t *c.Task, role c.Role) *c.Task {
	f := c.Frontier(t.ID,
		func(id string) []string { return g.byID[id].Dependencies },
		func(id string) bool { return g.byID[id].Role == role })
	if len(f) == 0 {
		return nil
	}
	return g.byID[f[0]]
}
