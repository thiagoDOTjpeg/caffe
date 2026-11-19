package studio

import (
	"errors"
	"strings"
	"testing"
	"time"

	c "caffe/internal/contracts"
	"caffe/internal/runtime"
	"caffe/internal/service"
)

func roomModel(t *testing.T) (model, *fakeSource) {
	m := overviewModel(t)
	f := m.src.(*fakeSource)
	a := &m.live.snap.Agents[0]
	a.Status, a.Visual = "running", service.VisualStarting
	a.Execution = &service.ExecutionView{Execution: c.Execution{ID: "ex1", Status: c.ExecRunning, StartedAt: testNow.Add(-12 * time.Second)}}
	f.set(func(f *fakeSource) {
		f.output = map[string]*service.AgentOutput{"c8eccebbd-77": {Source: service.OutputLive, Text: "● Reading changes.diff\n● Checking the worktree", ObservedAt: testNow.Add(-time.Second)}}
		f.rtObs = map[string]service.RuntimeObservation{"c8eccebbd-77": {Known: true, Found: true, Status: runtime.StatusWorking}}
	})
	m, _ = press(m, "3")
	return m, f
}

func tick(t *testing.T, m model, n int) model {
	t.Helper()
	_, cmd := m.Update(liveTickMsg{n})
	res, _ := m.Update(liveTickMsg{n})
	return drive(t, res.(model), cmd)
}

func TestOutputPaneLiveLabelled(t *testing.T) {
	m, f := roomModel(t)
	m = tick(t, m, 1)
	if f.outCalls != 0 {
		t.Fatal("output read while the pane is closed")
	}
	m, _ = press(m, "o")
	m = tick(t, m, 1)
	if f.outCalls != 1 {
		t.Fatalf("output calls %d", f.outCalls)
	}
	golden(t, "agents-output-120x40", m.View().Content)
	v := plain(m.View().Content)
	for _, want := range []string{"─ output · c8eccebbd (live, refreshed 1s ago)", "● Checking the worktree"} {
		if !strings.Contains(v, want) {
			t.Errorf("pane lacks %q:\n%s", want, v)
		}
	}
	if m, _ = press(m, "o"); m.output.on {
		t.Error("o does not close the pane")
	}
}

func TestOutputPausedWhenHidden(t *testing.T) {
	m, f := roomModel(t)
	m, _ = press(m, "o", "1")
	m = tick(t, m, 1)
	m.inspect = &inspector{id: "x"}
	m.screen = screenAgents
	m = tick(t, m, 1)
	if f.outCalls != 0 {
		t.Errorf("output read while hidden: %d calls", f.outCalls)
	}
	m.inspect = nil
	tick(t, m, 1)
	if f.outCalls != 1 {
		t.Errorf("not resumed when shown again: %d calls", f.outCalls)
	}
}

func TestOutputSources(t *testing.T) {
	m, f := roomModel(t)
	m, _ = press(m, "o")
	f.set(func(f *fakeSource) {
		f.output["c8eccebbd-77"] = &service.AgentOutput{Source: service.OutputCaptured, Text: "done"}
	})
	if v := plain(tick(t, m, 1).View().Content); !strings.Contains(v, "(captured at end)") {
		t.Error("captured output not labelled")
	}
	f.set(func(f *fakeSource) { delete(f.output, "c8eccebbd-77") })
	if v := plain(tick(t, m, 1).View().Content); !strings.Contains(v, "(no output saved)") {
		t.Error("missing output not labelled")
	}
	f.set(func(f *fakeSource) { f.outErr = errors.New("unavailable: herdr: server not running") })
	if v := plain(tick(t, m, 1).View().Content); !strings.Contains(v, "(live output unavailable)") || !strings.Contains(v, "server not running") {
		t.Error("runtime error not shown")
	}
}

func TestRuntimeStatusDecoratesCards(t *testing.T) {
	m, _ := roomModel(t)
	if v := plain(m.View().Content); !strings.Contains(v, "◌ STARTING") {
		t.Fatalf("stored state first:\n%s", v)
	}
	m = tick(t, m, 2)
	if v := plain(m.View().Content); !strings.Contains(v, "● WORKING") {
		t.Errorf("runtime status not applied:\n%s", v)
	}

	m.live.snap.Agents[0].Execution.Status = c.ExecCompleted
	if got := m.withRuntime(m.live.snap.Agents)[0].Visual; got != service.VisualCompleted {
		t.Errorf("runtime overrode the database: %s", got)
	}

	m.live.snap.Agents[0].Execution.Status = c.ExecRunning
	m.now = func() time.Time { return testNow.Add(time.Minute) }
	if got := m.withRuntime(m.live.snap.Agents)[0].Visual; got != service.VisualStarting {
		t.Errorf("stale runtime status used: %s", got)
	}
}

func TestRuntimeStatusOnlyForAgentsOnScreen(t *testing.T) {
	m, _ := roomModel(t)
	m, _ = press(m, "4")
	if got := m.agentsOnScreen(); got != nil {
		t.Errorf("agents on the Runs screen: %d", len(got))
	}
	m, _ = press(m, "1")
	if len(m.agentsOnScreen()) != 1 {
		t.Error("Overview agents not polled")
	}
	m.live.snap.Agents[0].Visual = service.VisualCompleted
	if got := m.agentsOnScreen(); got != nil {
		t.Error("finished agents polled")
	}
}
