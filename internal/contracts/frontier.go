package contracts

import "sort"

func Frontier(start string, deps func(string) []string, match func(string) bool) []string {
	found := map[string]bool{}
	seen := map[string]bool{start: true}
	var walk func(string)
	walk = func(id string) {
		for _, d := range deps(id) {
			if seen[d] {
				continue
			}
			seen[d] = true
			if match(d) {
				found[d] = true
				continue
			}
			walk(d)
		}
	}
	walk(start)

	reaches := func(from, to string) bool {
		visited := map[string]bool{}
		stack := append([]string(nil), deps(from)...)
		for len(stack) > 0 {
			id := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if id == to {
				return true
			}
			if !visited[id] {
				visited[id] = true
				stack = append(stack, deps(id)...)
			}
		}
		return false
	}
	var out []string
	for f := range found {
		shadowed := false
		for g := range found {
			if f != g && reaches(g, f) {
				shadowed = true
				break
			}
		}
		if !shadowed {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}
