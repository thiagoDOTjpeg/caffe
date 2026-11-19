package studio

import (
	"slices"
	"testing"

	"caffe/internal/config"
	c "caffe/internal/contracts"
	"caffe/internal/service"
	"caffe/internal/studio/ui"
)

func TestEveryStateIsDistinguishableWithoutColour(t *testing.T) {
	families := map[string][]badge{}
	for _, s := range []c.TaskStatus{c.TaskPending, c.TaskReady, c.TaskAssigned, c.TaskRunning, c.TaskBlocked,
		c.TaskReviewing, c.TaskCompleted, c.TaskFailed, c.TaskCancelled} {
		families["task"] = append(families["task"], checkBadge(t, taskBadges, s))
	}
	for _, s := range []c.RunStatus{c.RunPending, c.RunPlanning, c.RunExecuting, c.RunReviewing, c.RunValidating,
		c.RunCompleted, c.RunFailed, c.RunCancelled, c.RunInterrupted} {
		families["run"] = append(families["run"], checkBadge(t, runBadges, s))
	}
	for _, s := range []service.VisualState{service.VisualIdle, service.VisualStarting, service.VisualWorking,
		service.VisualWaiting, service.VisualBlocked, service.VisualLost, service.VisualInterrupted,
		service.VisualStopped, service.VisualCompleted, service.VisualFailed, service.VisualCancelled} {
		families["agent"] = append(families["agent"], checkBadge(t, agentBadges, s))
	}
	mono := ui.ThemeByName("mono", false)
	for name, badges := range families {
		seen := map[string]string{}
		rendered := map[string]bool{}
		for _, b := range badges {
			if other, dup := seen[b.Symbol]; dup {
				t.Errorf("%s: %q and %q share the symbol %s", name, other, b.Label, b.Symbol)
			}
			seen[b.Symbol] = b.Label
			out := b.render(mono)
			if rendered[out] {
				t.Errorf("%s: %q renders like another state in mono", name, b.Label)
			}
			rendered[out] = true
		}
	}

	if b := taskBadge("levitating"); b.Label != "levitating" || b.Symbol == "" {
		t.Errorf("unknown state = %+v", b)
	}
}

func checkBadge[K comparable](t *testing.T, m map[K]badge, k K) badge {
	t.Helper()
	b, ok := m[k]
	if !ok || b.Symbol == "" || b.Label == "" {
		t.Errorf("state %v has no complete badge: %+v", k, b)
	}
	return b
}

func TestThemeNamesMatchTheConfiguration(t *testing.T) {
	if !slices.Equal(ui.Themes, config.StudioThemes) {
		t.Errorf("ui themes %v, config accepts %v", ui.Themes, config.StudioThemes)
	}
}
