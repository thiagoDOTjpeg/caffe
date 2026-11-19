package ui

import (
	"fmt"
	"sort"
	"strings"
)

type Scope string

const ScopeGlobal Scope = "global"

type Action struct {
	ID     string
	Title  string
	Scope  Scope
	Keys   []string
	Leader string
}

type Registry struct {
	actions []Action
	groups  map[string]string
}

func (r *Registry) Add(a ...Action) { r.actions = append(r.actions, a...) }

func (r *Registry) Group(key, title string) {
	if r.groups == nil {
		r.groups = map[string]string{}
	}
	r.groups[key] = title
}

func (r *Registry) Actions() []Action { return append([]Action(nil), r.actions...) }

func (r *Registry) Validate() error {
	var errs []string
	ids := map[string]bool{}
	bound := map[Scope]map[string]string{}
	leaders := map[string]string{}
	for _, a := range r.actions {
		if a.ID == "" || a.Title == "" {
			errs = append(errs, fmt.Sprintf("action %q has no id or title", a.ID))
		}
		if ids[a.ID] {
			errs = append(errs, fmt.Sprintf("duplicate action id %q", a.ID))
		}
		ids[a.ID] = true
		if bound[a.Scope] == nil {
			bound[a.Scope] = map[string]string{}
		}
		for _, k := range a.Keys {
			if other, dup := bound[a.Scope][k]; dup {
				errs = append(errs, fmt.Sprintf("key %q is bound to both %s and %s in scope %s", k, other, a.ID, a.Scope))
			}
			bound[a.Scope][k] = a.ID
		}
		if a.Leader != "" {
			if other, dup := leaders[a.Leader]; dup {
				errs = append(errs, fmt.Sprintf("leader %q is bound to both %s and %s", a.Leader, other, a.ID))
			}
			leaders[a.Leader] = a.ID
			if group, _, _ := strings.Cut(a.Leader, " "); r.groups[group] == "" {
				errs = append(errs, fmt.Sprintf("leader group %q of %s has no title", group, a.ID))
			}
		}
	}
	for scope, keys := range bound {
		if scope == ScopeGlobal {
			continue
		}
		for k, id := range keys {
			if global, ok := bound[ScopeGlobal][k]; ok {
				errs = append(errs, fmt.Sprintf("key %q of %s (scope %s) shadows global %s", k, id, scope, global))
			}
		}
	}
	sort.Strings(errs)
	if len(errs) > 0 {
		return fmt.Errorf("invalid key bindings: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (r *Registry) Match(scope Scope, key string) (Action, bool) {
	for _, want := range []Scope{scope, ScopeGlobal} {
		for _, a := range r.actions {
			if a.Scope == want && contains(a.Keys, key) {
				return a, true
			}
		}
	}
	return Action{}, false
}

func (r *Registry) MatchLeader(seq string) (Action, bool) {
	for _, a := range r.actions {
		if a.Leader == seq {
			return a, true
		}
	}
	return Action{}, false
}

func (r *Registry) Help(scope Scope) []Action {
	var own, global []Action
	for _, a := range r.actions {
		switch a.Scope {
		case scope:
			if scope != ScopeGlobal {
				own = append(own, a)
			}
		case ScopeGlobal:
			if len(a.Keys) > 0 {
				global = append(global, a)
			}
		}
	}
	return append(own, global...)
}

func (r *Registry) LeaderGroup(group string) (string, []Action) {
	var out []Action
	for _, a := range r.actions {
		if g, _, _ := strings.Cut(a.Leader, " "); g == group && a.Leader != "" {
			out = append(out, a)
		}
	}
	return r.groups[group], out
}

func (r *Registry) LeaderGroups() [][2]string {
	var out [][2]string
	for k, title := range r.groups {
		out = append(out, [2]string{k, title})
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

func (r *Registry) Filter(query string) []Action {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return r.Actions()
	}
	var exact, fuzzy []Action
	for _, a := range r.actions {
		hay := strings.ToLower(a.Title + " " + a.ID)
		switch {
		case strings.Contains(hay, q):
			exact = append(exact, a)
		case subsequence(q, hay):
			fuzzy = append(fuzzy, a)
		}
	}
	return append(exact, fuzzy...)
}

func subsequence(needle, hay string) bool {
	hr := []rune(hay)
	i := 0
	for _, c := range needle {
		for i < len(hr) && hr[i] != c {
			i++
		}
		if i == len(hr) {
			return false
		}
		i++
	}
	return true
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func KeyLabel(a Action) string {
	keys := append([]string(nil), a.Keys...)
	if a.Leader != "" {
		keys = append(keys, "space "+a.Leader)
	}
	return strings.Join(keys, ", ")
}
