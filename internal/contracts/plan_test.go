package contracts

import (
	"strings"
	"testing"
)

func task(id string, role Role, deps ...string) PlanTask {
	return PlanTask{ID: id, Title: id, Description: id, Role: role, Dependencies: deps, AcceptanceCriteria: []string{"x"}}
}

func TestDeveloperChains(t *testing.T) {
	cases := []struct {
		name  string
		tasks []PlanTask
		bad   string
	}{
		{"chain", []PlanTask{task("a", RoleDeveloper), task("b", RoleDeveloper, "a"), task("v", RoleValidator, "b")}, ""},
		{"parallel without a common dependent", []PlanTask{task("a", RoleDeveloper), task("b", RoleDeveloper)}, ""},
		{"chain seen through a reviewer", []PlanTask{task("a", RoleDeveloper), task("r", RoleReviewer, "a"), task("b", RoleDeveloper, "a"), task("c", RoleDeveloper, "r", "b")}, ""},
		{"diamond", []PlanTask{task("a", RoleDeveloper), task("b", RoleDeveloper), task("c", RoleDeveloper, "a", "b")}, `"c" builds on the unrelated developer tasks a, b`},
		{"validator over two branches", []PlanTask{task("a", RoleDeveloper), task("ra", RoleReviewer, "a"), task("b", RoleDeveloper), task("v", RoleValidator, "ra", "b")}, `"v" builds on the unrelated developer tasks a, b`},
		{"researcher may read both", []PlanTask{task("a", RoleDeveloper), task("b", RoleDeveloper), task("x", RoleResearcher, "a", "b")}, ""},
	}
	for _, tc := range cases {
		err := (&ExecutionPlan{Goal: "g", Tasks: tc.tasks}).Validate()
		switch {
		case tc.bad == "" && err != nil:
			t.Errorf("%s: unexpected error %v", tc.name, err)
		case tc.bad != "" && (err == nil || !strings.Contains(err.Error(), tc.bad)):
			t.Errorf("%s: want error containing %q, got %v", tc.name, tc.bad, err)
		}
	}
}

func TestFrontierPicksTheLatestDeveloperNotTheClosest(t *testing.T) {
	deps := map[string][]string{"c": {"r", "y"}, "r": {"a"}, "y": {"z"}, "z": {"b"}, "b": {"a"}}
	dev := map[string]bool{"a": true, "b": true}
	got := Frontier("c", func(id string) []string { return deps[id] }, func(id string) bool { return dev[id] })
	if len(got) != 1 || got[0] != "b" {
		t.Fatalf("frontier = %v, want [b]", got)
	}
}
