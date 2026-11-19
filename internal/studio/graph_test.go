package studio

import (
	"fmt"
	"strings"
	"testing"

	c "caffe/internal/contracts"
	"caffe/internal/service"
)

func plan(specs ...string) []service.TaskView {
	var out []service.TaskView
	for _, s := range specs {
		key, deps, _ := strings.Cut(s, ":")
		t := service.TaskView{Task: c.Task{ID: key, Key: key, Status: c.TaskPending}}
		if deps != "" {
			t.DependsOn = strings.Split(deps, ",")
		}
		out = append(out, t)
	}
	return out
}

func layerKeys(ls [][]service.TaskView) string {
	var parts []string
	for _, l := range ls {
		var keys []string
		for _, t := range l {
			keys = append(keys, t.Key)
		}
		parts = append(parts, strings.Join(keys, ","))
	}
	return strings.Join(parts, " | ")
}

func TestLayers(t *testing.T) {
	for name, tc := range map[string]struct {
		tasks []service.TaskView
		want  string
	}{
		"diamond":    {plan("a", "b:a", "c:a", "d:b,c"), "a | b,c | d"},
		"fan-out":    {plan("root", "x:root", "y:root", "z:root"), "root | x,y,z"},
		"fan-in":     {plan("x", "y", "z", "join:x,y,z"), "x,y,z | join"},
		"chain":      {plan("a", "b:a", "c:b", "d:c", "e:d"), "a | b | c | d | e"},
		"longest":    {plan("a", "b:a", "c:b", "d:a,c"), "a | b | c | d"},
		"unknown":    {plan("a:ghost"), "a"},
		"barycenter": {plan("r1", "r2", "x:r2", "y:r1"), "r1,r2 | y,x"},
	} {
		if got := layerKeys(layers(tc.tasks)); got != tc.want {
			t.Errorf("%s: %s, want %s", name, got, tc.want)
		}
	}
}

func TestLayersSurvivesACycle(t *testing.T) {

	if ls := layers(plan("a:b", "b:a")); len(ls) == 0 {
		t.Error("no layers")
	}
}

func TestGraphLinesShowEveryEdge(t *testing.T) {
	got := strings.Join(graphLines(plan("research", "docs", "impl:research", "review:impl,docs", "validate:review")), "\n")
	want := strings.Join([]string{
		" ◌ research ──▶ ◌ impl     ──▶ ◌ review   ──▶ ◌ validate",
		"                               ⤷ docs",
		" ◌ docs",
	}, "\n")
	if got != want {
		t.Errorf("graph:\n%s\nwant:\n%s", got, want)
	}
}

func TestGraphFallsBackToListAboveLimit(t *testing.T) {
	m := boardModel(t)
	var many []string
	for i := range graphMax + 1 {
		many = append(many, fmt.Sprintf("t%02d", i))
	}
	m.live.snap.Tasks = plan(many...)
	m, _ = press(m, "g")
	v := m.View().Content
	if !strings.Contains(v, "graph shown up to 30 tasks") || !strings.Contains(v, "DEPENDS ON") {
		t.Error("no list fallback above the limit")
	}
}

func TestGoldenTaskGraphAndList(t *testing.T) {
	m := boardModel(t)
	m, _ = press(m, "g")
	golden(t, "tasks-graph-120x40", m.View().Content)
	m, _ = press(m, "L")
	golden(t, "tasks-list-120x40", m.View().Content)
	if m, _ = press(m, "b"); m.tasks.view != viewBoard {
		t.Error("b does not go back to the board")
	}
}
