package ui

import (
	"strings"
	"testing"
)

func TestValidateRejectsAmbiguousBindings(t *testing.T) {
	cases := map[string]struct {
		actions []Action
		want    string
	}{
		"same key twice in a scope": {[]Action{
			{ID: "a", Title: "A", Scope: "tasks", Keys: []string{"x"}},
			{ID: "b", Title: "B", Scope: "tasks", Keys: []string{"x"}},
		}, `key "x" is bound to both a and b`},
		"screen key shadows a global one": {[]Action{
			{ID: "quit", Title: "Q", Scope: ScopeGlobal, Keys: []string{"ctrl+q"}},
			{ID: "local", Title: "L", Scope: "agents", Keys: []string{"ctrl+q"}},
		}, "shadows global quit"},
		"duplicate id": {[]Action{
			{ID: "a", Title: "A", Scope: ScopeGlobal},
			{ID: "a", Title: "B", Scope: "tasks"},
		}, `duplicate action id "a"`},
		"leader sequence twice": {[]Action{
			{ID: "a", Title: "A", Scope: ScopeGlobal, Leader: "v b"},
			{ID: "b", Title: "B", Scope: ScopeGlobal, Leader: "v b"},
		}, `leader "v b" is bound to both`},
		"leader in an unnamed group": {[]Action{
			{ID: "a", Title: "A", Scope: ScopeGlobal, Leader: "z q"},
		}, `leader group "z"`},
		"missing title": {[]Action{{ID: "a", Scope: ScopeGlobal}}, "no id or title"},
	}
	for name, tc := range cases {
		r := &Registry{}
		r.Group("v", "view")
		r.Add(tc.actions...)
		if err := r.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want error containing %q, got %v", name, tc.want, err)
		}
	}
	// The same key in two different screens is fine: only one applies at a time.
	r := &Registry{}
	r.Add(Action{ID: "a", Title: "A", Scope: "tasks", Keys: []string{"d"}}, Action{ID: "b", Title: "B", Scope: "agents", Keys: []string{"d"}})
	if err := r.Validate(); err != nil {
		t.Errorf("same key in different screens rejected: %v", err)
	}
}

func TestMatchPrefersTheScreen(t *testing.T) {
	r := &Registry{}
	r.Add(
		Action{ID: "global.x", Title: "G", Scope: ScopeGlobal, Keys: []string{"x"}},
		Action{ID: "tasks.d", Title: "D", Scope: "tasks", Keys: []string{"d"}},
	)
	if a, ok := r.Match("tasks", "d"); !ok || a.ID != "tasks.d" {
		t.Errorf("screen key: %v %v", a.ID, ok)
	}
	if a, ok := r.Match("tasks", "x"); !ok || a.ID != "global.x" {
		t.Errorf("global key from a screen: %v %v", a.ID, ok)
	}
	if _, ok := r.Match("agents", "d"); ok {
		t.Error("a key of another screen matched")
	}
}

func TestFilterRanksSubstringBeforeFuzzy(t *testing.T) {
	r := &Registry{}
	r.Add(
		Action{ID: "screen.agents", Title: "Go to Agents"},
		Action{ID: "layout.panel", Title: "Toggle the bottom panel"},
		Action{ID: "agent.attach", Title: "Attach to agent"},
		Action{ID: "app.quit", Title: "Quit Caffè Studio"},
	)
	ids := func(as []Action) string {
		var s []string
		for _, a := range as {
			s = append(s, a.ID)
		}
		return strings.Join(s, ",")
	}
	if got := ids(r.Filter("agent")); got != "screen.agents,agent.attach" {
		t.Errorf("substring: %s", got)
	}
	if got := ids(r.Filter("tgbp")); got != "layout.panel" { // T-o-G-gle ... B-ottom P-anel
		t.Errorf("fuzzy: %s", got)
	}
	if got := ids(r.Filter("QUIT")); got != "app.quit" {
		t.Errorf("case-insensitive: %s", got)
	}
	if len(r.Filter("")) != 4 || len(r.Filter("zzzz")) != 0 {
		t.Error("empty query must list all, no match must list none")
	}
}
