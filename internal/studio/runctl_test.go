package studio

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	c "caffe/internal/contracts"
	"caffe/internal/service"
)

func TestCancelAsksNamesAgentsAndRefreshes(t *testing.T) {
	m := overviewModel(t)
	f := m.src.(*fakeSource)
	m.live.snap.Agents[0].Name = "c8eccebbd-reviewer"
	m, _ = press(m, "c")
	if m.overlay != overlayConfirm {
		t.Fatal("c does not ask")
	}
	v := plain(m.View().Content)
	for _, want := range []string{"Cancel run 0112dd6a?", "1 agent will be stopped (c8eccebbd-reviewer).", "Completed work and worktrees are kept.", "▸[ Cancel ]"} {
		if !strings.Contains(v, want) {
			t.Errorf("dialog lacks %q", want)
		}
	}
	m, _ = press(m, "enter") // the default is Cancel: nothing happens
	if len(f.calls) != 0 {
		t.Fatalf("cancelled on the default button: %v", f.calls)
	}
	m, _ = press(m, "c", "l")
	m, cmd := press(m, "enter")
	if a, ok := m.busy("0112dd6a-1"); !ok || a != "cancelling" {
		t.Fatalf("not in flight: %q %v", a, ok)
	}
	if v := plain(m.View().Content); !strings.Contains(v, "(cancelling…)") {
		t.Error("header does not show the action in flight")
	}
	// Pressing again while in flight is refused, without a second call.
	again, _ := press(m, "c", "l", "enter")
	if !strings.Contains(plain(again.View().Content), "already cancelling") {
		t.Error("a second cancel was not refused")
	}
	m = drive(t, m, cmd)
	if len(f.calls) != 1 || f.calls[0] != "cancel 0112dd6a-1" {
		t.Fatalf("calls %v", f.calls)
	}
	if _, ok := m.busy("0112dd6a-1"); ok || m.live.snap.Run.Status != c.RunCancelled {
		t.Errorf("after cancel: busy %v status %s", ok, m.live.snap.Run.Status)
	}
	if !strings.Contains(plain(m.View().Content), "✓ run 0112dd6a cancelled") {
		t.Error("no success toast")
	}
}

func TestConflictRefreshesAndInforms(t *testing.T) {
	m := overviewModel(t)
	f := m.src.(*fakeSource)
	f.set(func(f *fakeSource) {
		f.cmdErr = fmt.Errorf("run 0112dd6a changed meanwhile: %w", service.ErrConflict)
		f.snaps["0112dd6a-1"].Run.Status = c.RunCompleted // someone else finished it
	})
	m, _ = press(m, "c", "l")
	m, cmd := press(m, "enter")
	m = drive(t, m, cmd)
	v := plain(m.View().Content)
	if !strings.Contains(v, "changed meanwhile: state changed; showing the current state") {
		t.Errorf("conflict not explained:\n%s", v)
	}
	if m.live.snap.Run.Status != c.RunCompleted {
		t.Error("state not refreshed after the conflict")
	}
	if len(f.calls) != 1 {
		t.Errorf("retried: %v", f.calls)
	}
}

func TestNewRunFormStartsAndSelectsTheRun(t *testing.T) {
	m := overviewModel(t)
	f := m.src.(*fakeSource)
	m, _ = press(m, "n")
	if m.overlay != overlayNewRun {
		t.Fatal("n does not open the form")
	}
	m, _ = press(m, "enter") // empty request: nothing starts
	if m.overlay != overlayNewRun || len(f.started) != 0 {
		t.Fatal("started an empty request")
	}
	for _, r := range "Add docs" {
		m, _ = press(m, string(r))
	}
	m, _ = press(m, "tab")
	for _, r := range "plan.json" {
		m, _ = press(m, string(r))
	}
	golden(t, "new-run-120x40", m.View().Content)
	m, cmd := press(m, "enter")
	m = drive(t, m, cmd)
	if len(f.started) != 1 || f.started[0].Request != "Add docs" || f.started[0].PlanFile != "plan.json" {
		t.Fatalf("started %+v", f.started)
	}
	if m.live.runID != "newrun00-1" || m.live.snap == nil || m.live.snap.Run.ID != "newrun00-1" {
		t.Errorf("new run not selected: %q", m.live.runID)
	}
	if v := plain(m.View().Content); !strings.Contains(v, "keeps running if Studio exits") {
		t.Error("no toast about the detached run")
	}
}

func TestResumeOnlyInterruptedFromRunsScreen(t *testing.T) {
	m, f := runsModel(t)
	m, _ = press(m, "4", "R") // cursor on the executing run
	if len(f.calls) != 0 || !strings.Contains(plain(m.View().Content), "only interrupted runs can be resumed") {
		t.Fatalf("resumed a live run: %v", f.calls)
	}
	m, _ = press(m, "esc", "G") // interrupted run
	m, cmd := press(m, "R")
	if a, _ := m.busy("9c41aa02-3"); a != "resuming" {
		t.Fatalf("in flight: %q", a)
	}
	if v := plain(m.View().Content); !strings.Contains(v, "⋯ resuming") {
		t.Error("runs list does not show the resume in flight")
	}
	m = drive(t, m, cmd)
	if len(f.calls) != 1 || f.calls[0] != "resume 9c41aa02-3" {
		t.Errorf("calls %v", f.calls)
	}
}

func TestCleanAsksAndSummarises(t *testing.T) {
	m, f := runsModel(t)
	m, _ = press(m, "4", "x")
	if v := plain(m.View().Content); !strings.Contains(v, "branches are kept") || !strings.Contains(v, "uncommitted changes") {
		t.Errorf("clean dialog:\n%s", v)
	}
	m, _ = press(m, "l")
	m, cmd := press(m, "enter")
	m = drive(t, m, cmd)
	if len(f.calls) != 1 || f.calls[0] != "clean false" {
		t.Fatalf("calls %v", f.calls)
	}
	if v := plain(m.View().Content); !strings.Contains(v, "clean: 1 worktrees removed, 1 kept") {
		t.Errorf("no summary toast:\n%s", v)
	}
}

func TestRunControlsFromLeaderAndErrors(t *testing.T) {
	m := overviewModel(t)
	f := m.src.(*fakeSource)
	f.set(func(f *fakeSource) { f.cmdErr = errors.New("herdr: server not running") })
	m, _ = press(m, "space", "r", "x", "l")
	m, cmd := press(m, "enter")
	m = drive(t, m, cmd)
	if v := plain(m.View().Content); !strings.Contains(v, "✗ clean: herdr: server not running") {
		t.Errorf("error toast:\n%s", v)
	}
	if m, _ = press(m, "esc", "space", "r", "n"); m.overlay != overlayNewRun {
		t.Error("space r n does not open the form")
	}
}
