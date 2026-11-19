package studio

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"caffe/internal/service"
)

type fakePauser struct{ paused, resumed int }

func (p *fakePauser) Pause()  { p.paused++ }
func (p *fakePauser) Resume() { p.resumed++ }

func TestAttachHiddenWhenUnsupported(t *testing.T) {
	m, f := roomModel(t) // attachArg nil: the runtime cannot attach
	if v := plain(m.View().Content); strings.Contains(v, "[a] attach") {
		t.Error("attach offered by a runtime that cannot")
	}
	m, cmd := press(m, "a")
	if cmd != nil || len(f.calls) != 0 || !strings.Contains(plain(m.View().Content), "cannot attach") {
		t.Errorf("unsupported attach: cmd %v calls %v", cmd != nil, f.calls)
	}
}

func TestAttachHandsOverTheTerminalAndRefreshesOnReturn(t *testing.T) {
	m, f := roomModel(t)
	f.set(func(f *fakeSource) { f.attachArg = []string{"herdr", "agent", "attach", "c8eccebbd-reviewer"} })
	p := &fakePauser{}
	m.watcher = p
	m.live.snap.Agents[0].Name = "c8eccebbd-reviewer"
	if v := plain(m.View().Content); !strings.Contains(v, "[a] attach") {
		t.Fatal("attach not offered")
	}
	m, cmd := press(m, "a")
	ready, ok := cmd().(attachReadyMsg)
	if !ok || ready.err != nil || strings.Join(ready.cmd.Args, " ") != "herdr agent attach c8eccebbd-reviewer" {
		t.Fatalf("attach command: %+v", ready)
	}
	res, exec := m.Update(ready)
	m = res.(model)
	if exec == nil || p.paused != 1 {
		t.Fatalf("no hand-off: exec %v paused %d", exec != nil, p.paused)
	}
	// The user detaches: the watcher resumes and everything is re-read.
	m.live.pending = false
	res, after := m.Update(attachDoneMsg{agent: "c8eccebbd-reviewer"})
	m = res.(model)
	if p.resumed != 1 || !m.live.pending || after == nil {
		t.Errorf("after detach: resumed %d pending %v", p.resumed, m.live.pending)
	}
	if v := plain(m.View().Content); !strings.Contains(v, "detached from c8eccebbd-reviewer") {
		t.Error("no toast on return")
	}
	// A failing attach command is reported and still restores the watcher.
	res, _ = m.Update(attachDoneMsg{agent: "x", err: errors.New("exit status 1")})
	if m = res.(model); p.resumed != 2 || !strings.Contains(plain(m.View().Content), "attach to x: exit status 1") {
		t.Error("failed attach not reported")
	}
}

func TestAttachRefusesFinishedAndConflicts(t *testing.T) {
	m, f := roomModel(t)
	f.set(func(f *fakeSource) { f.attachArg = []string{"herdr"} })
	m.live.snap.Agents[0].Visual = service.VisualCompleted
	m.live.snap.Agents[0].Execution.Status = "completed"
	m, cmd := press(m, "a")
	if cmd != nil || !strings.Contains(plain(m.View().Content), "has finished") {
		t.Error("attached to a finished agent")
	}
	m, _ = roomModel(t)
	m.src.(*fakeSource).set(func(f *fakeSource) {
		f.attachArg = []string{"herdr"}
		f.cmdErr = fmt.Errorf("agent c8eccebb has stopped: %w", service.ErrConflict)
	})
	m, cmd = press(m, "a")
	res, refresh := m.Update(cmd())
	m = res.(model)
	if refresh == nil || !strings.Contains(plain(m.View().Content), "has stopped") {
		t.Error("conflict not reported with a refresh")
	}
}
