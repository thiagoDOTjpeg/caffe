package studio

import (
	"fmt"
	"sort"
	"strings"

	"caffe/internal/service"
)

// taskView is how the Tasks screen shows the run (ux-specification §2.2).
type taskView int

const (
	viewBoard taskView = iota
	viewGraph
	viewList
)

// graphMax is the size above which the graph view falls back to the list.
const graphMax = 30

// layers places each task at the length of the longest dependency path from
// a root, and orders each layer so a task sits next to its first dependency
// (a barycenter pass). Plans are validated acyclic; a dependency on an
// unknown key is ignored. Plan order breaks ties.
func layers(tasks []service.TaskView) [][]service.TaskView {
	byKey := map[string]service.TaskView{}
	order := map[string]int{}
	for i, t := range tasks {
		byKey[t.Key], order[t.Key] = t, i
	}
	depth := map[string]int{}
	var level func(k string, seen int) int
	level = func(k string, seen int) int {
		if d, ok := depth[k]; ok {
			return d
		}
		d := 0
		if seen <= len(tasks) { // guard: a cycle would otherwise recurse forever
			for _, dep := range byKey[k].DependsOn {
				if _, ok := byKey[dep]; ok {
					d = max(d, level(dep, seen+1)+1)
				}
			}
		}
		depth[k] = d
		return d
	}
	var out [][]service.TaskView
	for _, t := range tasks {
		d := level(t.Key, 0)
		for len(out) <= d {
			out = append(out, nil)
		}
		out[d] = append(out[d], t)
	}
	// Barycenter sweeps: down (by dependencies), up (by dependents), down.
	row := map[string]int{}
	dependents := map[string][]string{}
	for _, t := range tasks {
		for _, d := range t.DependsOn {
			dependents[d] = append(dependents[d], t.Key)
		}
	}
	place := func(i int, neighbours func(service.TaskView) []string, layerOf int) {
		key := func(t service.TaskView) float64 {
			best := 1 << 30
			for _, n := range neighbours(t) {
				if r, ok := row[n]; ok && depth[n] == layerOf {
					best = min(best, r)
				}
			}
			return float64(best) + float64(order[t.Key])/float64(len(tasks)+1)
		}
		sort.SliceStable(out[i], func(a, b int) bool { return key(out[i][a]) < key(out[i][b]) })
		for r, t := range out[i] {
			row[t.Key] = r
		}
	}
	deps := func(t service.TaskView) []string { return t.DependsOn }
	succ := func(t service.TaskView) []string { return dependents[t.Key] }
	for i := range out {
		place(i, deps, i-1)
	}
	for i := len(out) - 2; i >= 0; i-- {
		place(i, succ, i+1)
	}
	for i := 1; i < len(out); i++ {
		place(i, deps, i-1)
	}
	return out
}

// graphLines draws the layers left to right. An edge between neighbours on
// the same row is drawn as an arrow; any other dependency is listed under
// the task ("⤷ a, b"), so every edge is shown without crossing lines.
func graphLines(tasks []service.TaskView) []string {
	ls := layers(tasks)
	colW := 0
	for _, t := range tasks {
		colW = max(colW, len([]rune(t.Key))+2)
	}
	const arrow, gap = " ──▶ ", "     "
	rows := 0
	for _, l := range ls {
		rows = max(rows, len(l))
	}
	cell := func(layer, row int) (service.TaskView, bool) {
		if row < len(ls[layer]) {
			return ls[layer][row], true
		}
		return service.TaskView{}, false
	}
	var out []string
	for r := range rows {
		var top, sub strings.Builder
		hasSub := false
		for i := range ls {
			t, ok := cell(i, r)
			prev, pok := service.TaskView{}, false
			if i > 0 {
				prev, pok = cell(i-1, r)
			}
			var extra []string
			linked := false
			for _, d := range t.DependsOn {
				if pok && d == prev.Key {
					linked = true
				} else {
					extra = append(extra, d)
				}
			}
			if i > 0 {
				if ok && linked {
					top.WriteString(arrow)
				} else {
					top.WriteString(gap)
				}
				sub.WriteString(gap)
			}
			text, below := "", ""
			if ok {
				text = taskBadge(t.Status).Symbol + " " + t.Key
				if len(extra) > 0 {
					below, hasSub = "⤷ "+strings.Join(extra, ", "), true
				}
			}
			top.WriteString(fmt.Sprintf("%-*s", colW, text))
			sub.WriteString(fmt.Sprintf("%-*s", colW, below))
		}
		out = append(out, " "+strings.TrimRight(top.String(), " "))
		if hasSub {
			out = append(out, " "+strings.TrimRight(sub.String(), " "))
		}
	}
	return out
}

// listLines is the list view: one task per line with its dependencies.
func listLines(tasks []service.TaskView, width int) []string {
	out := []string{fmt.Sprintf("   %-13s %-14s %-11s %s", "STATUS", "TASK", "ROLE", "DEPENDS ON")}
	for _, t := range tasks {
		b := taskBadge(t.Status)
		out = append(out, fmt.Sprintf("   %-13s %-14s %-11s %s", b.Symbol+" "+b.Label, clip(t.Key, 14), t.Role,
			clip(strings.Join(t.DependsOn, ", "), max(width-46, 10))))
	}
	return out
}
