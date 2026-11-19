package studio

import (
	c "caffe/internal/contracts"
	"caffe/internal/service"
	"caffe/internal/studio/ui"
)

type badge struct {
	Symbol string
	Label  string
	Tone   ui.Tone
}

func (b badge) render(th ui.Theme) string { return th.Render(b.Tone, b.Symbol+" "+b.Label) }

var taskBadges = map[c.TaskStatus]badge{
	c.TaskPending:   {"◌", "pending", ui.ToneMuted},
	c.TaskReady:     {"○", "ready", ui.ToneNormal},
	c.TaskAssigned:  {"◔", "assigned", ui.ToneActive},
	c.TaskRunning:   {"●", "running", ui.ToneActive},
	c.TaskBlocked:   {"⚠", "blocked", ui.ToneWarn},
	c.TaskReviewing: {"↻", "reviewing", ui.ToneActive},
	c.TaskCompleted: {"✓", "completed", ui.ToneOK},
	c.TaskFailed:    {"✗", "failed", ui.ToneError},
	c.TaskCancelled: {"⊘", "cancelled", ui.ToneMuted},
}

var runBadges = map[c.RunStatus]badge{
	c.RunPending:     {"◌", "pending", ui.ToneMuted},
	c.RunPlanning:    {"◔", "planning", ui.ToneActive},
	c.RunExecuting:   {"●", "executing", ui.ToneActive},
	c.RunReviewing:   {"↻", "reviewing", ui.ToneActive},
	c.RunValidating:  {"◑", "validating", ui.ToneActive},
	c.RunCompleted:   {"✓", "completed", ui.ToneOK},
	c.RunFailed:      {"✗", "failed", ui.ToneError},
	c.RunCancelled:   {"⊘", "cancelled", ui.ToneMuted},
	c.RunInterrupted: {"⏸", "interrupted", ui.ToneWarn},
}

var agentBadges = map[service.VisualState]badge{
	service.VisualIdle:        {"○", "idle", ui.ToneMuted},
	service.VisualStarting:    {"◌", "starting", ui.ToneNormal},
	service.VisualWorking:     {"●", "working", ui.ToneActive},
	service.VisualWaiting:     {"◐", "waiting", ui.ToneNormal},
	service.VisualBlocked:     {"⚠", "blocked", ui.ToneWarn},
	service.VisualLost:        {"?", "lost", ui.ToneError},
	service.VisualInterrupted: {"⏸", "interrupted", ui.ToneWarn},
	service.VisualStopped:     {"■", "stopped", ui.ToneMuted},
	service.VisualCompleted:   {"✓", "completed", ui.ToneOK},
	service.VisualFailed:      {"✗", "failed", ui.ToneError},
	service.VisualCancelled:   {"⊘", "cancelled", ui.ToneMuted},
}

func taskBadge(s c.TaskStatus) badge { return lookup(taskBadges, s, string(s)) }
func runBadge(s c.RunStatus) badge   { return lookup(runBadges, s, string(s)) }
func agentBadge(s service.VisualState) badge {
	return lookup(agentBadges, s, string(s))
}

func lookup[K comparable](m map[K]badge, k K, name string) badge {
	if b, ok := m[k]; ok {
		return b
	}
	return badge{"·", name, ui.ToneNormal}
}
