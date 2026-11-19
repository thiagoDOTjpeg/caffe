package studio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rs/zerolog"

	c "caffe/internal/contracts"
	"caffe/internal/service"
)

// One golden render per state of ux-specification §6. The "terminal too
// small" state is layout-50x12 (TestGoldenLayout).

func sized(m model) model {
	res, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return res.(model)
}

func TestGoldenStateNotAProject(t *testing.T) {
	m := newModel(nil, Project{Root: "/home/me/elsewhere"}, &crashGuard{log: zerolog.Nop()})
	m.noProject = true
	m = sized(m)
	golden(t, "state-no-project", m.View().Content)
	v := m.View().Content
	if !strings.Contains(v, "/home/me/elsewhere") || !strings.Contains(v, "caffe init") {
		t.Error("message lacks the directory or caffe init")
	}
	// Screen keys do nothing; q quits.
	if next, _ := press(m, "4"); next.screen != screenOverview {
		t.Error("a screen key worked without a project")
	}
	if _, cmd := press(m, "q"); !quits(cmd) {
		t.Error("q does not quit")
	}
}

func TestGoldenStateNoRuns(t *testing.T) {
	f := newFake()
	f.runs, f.snaps, f.events = nil, nil, nil
	m := liveModel(f)
	m = sized(drive(t, m, m.Init()))
	golden(t, "state-no-runs", m.View().Content)
	if v := m.View().Content; !strings.Contains(v, "No runs yet. Press n to start one, or caffe run") {
		t.Error("no-runs text missing")
	}
}

func TestGoldenStateLoading(t *testing.T) {
	m := sized(liveModel(newFake())) // Init not run: nothing loaded yet
	golden(t, "state-loading", m.View().Content)
	for _, s := range []string{"1", "2", "3"} {
		next, _ := press(m, s)
		if v := next.View().Content; !strings.Contains(v, "· loading…") {
			t.Errorf("screen %s: no loading title", s)
		}
	}
}

func TestGoldenStateDatabaseUnreadable(t *testing.T) {
	m := overviewModel(t)
	res, _ := m.Update(loadedMsg{at: testNow, err: errors.New("database is locked")})
	m = res.(model)
	golden(t, "state-database-unreadable", m.View().Content)
	v := m.View().Content
	for _, want := range []string{"✗ database", "stale since", "database is locked", "Add a hello file"} {
		if !strings.Contains(v, want) {
			t.Errorf("lacks %q", want)
		}
	}
}

func TestGoldenStateRuntimeUnreachable(t *testing.T) {
	m := overviewModel(t)
	res, _ := m.Update(healthMsg{err: errors.New("herdr: server not running")})
	m = res.(model)
	golden(t, "state-runtime-unreachable", m.View().Content)
	if v := m.View().Content; !strings.Contains(v, "✗ runtime: herdr: server not running") {
		t.Error("header lacks the adapter's error")
	}
	m, _ = press(m, "3")
	if v := m.View().Content; !strings.Contains(v, "● WORKING") {
		t.Error("Agent Room lost its stored state")
	}
}

func TestGoldenStateRunOwnerGone(t *testing.T) {
	m := overviewModel(t)
	m.live.snap.Run.Status, m.live.snap.Run.OwnerAlive = c.RunInterrupted, false
	golden(t, "state-interrupted", m.View().Content)
	if v := m.View().Content; !strings.Contains(v, "caffe resume 0112dd6a") || !strings.Contains(v, "⏸") {
		t.Error("no interrupted state or resume hint")
	}
}

func TestGoldenStateActionFailed(t *testing.T) {
	m := overviewModel(t)
	m = m.failed("cancel run 0112dd6a", fmt.Errorf("%w: run state changed", service.ErrNotFound))
	golden(t, "state-action-failed", m.View().Content)
}

func TestEventCursorGapReloadsTheFeed(t *testing.T) {
	m := overviewModel(t) // lastSeq 4
	if !gap(4, []c.Event{{Seq: 9}}) || gap(4, []c.Event{{Seq: 5}}) || gap(0, []c.Event{{Seq: 9}}) {
		t.Fatal("gap detection")
	}
	res, cmd := m.Update(loadedMsg{runs: m.live.runs, runID: m.live.runID, snap: m.live.snap, events: []c.Event{{Seq: 9, RunID: m.live.runID}}})
	m = res.(model)
	if m.live.loaded || len(m.live.feed) != 0 || cmd == nil {
		t.Fatalf("gap not handled: loaded %v feed %d", m.live.loaded, len(m.live.feed))
	}
	m = drive(t, m, cmd)
	if !m.live.loaded || m.live.lastSeq != 4 {
		t.Errorf("reload: loaded %v lastSeq %d", m.live.loaded, m.live.lastSeq)
	}
}

func TestRunWithoutAProjectQuitsCleanly(t *testing.T) {
	r, w := io.Pipe()
	go func() {
		time.Sleep(300 * time.Millisecond)
		w.Write([]byte("q"))
	}()
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), nil, Project{Root: "/nowhere"}, Options{Input: r, Output: out, NoProject: true, Log: zerolog.Nop()})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Studio did not exit")
	}
	expectRestored(t, out.String()) // the message itself is the state-no-project golden: a pipe reports no size to draw at
}
