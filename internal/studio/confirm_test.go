package studio

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

type confirmed struct{ n int }

func dialogModel(t *testing.T, hits *confirmed) model {
	m := testModel()
	return m.ask("Cancel run 0112dd6a?", "Stop the run",
		[]string{"1 agent will be stopped (c8eccebbd-reviewer).", "Completed work and worktrees are kept."},
		func(m model) (model, tea.Cmd) { hits.n++; return m, nil })
}

func TestConfirmDefaultIsCancel(t *testing.T) {
	var hits confirmed
	m := dialogModel(t, &hits)
	if m, _ = press(m, "enter"); hits.n != 0 || m.overlay != overlayNone {
		t.Fatalf("enter on the default confirmed (%d) or left the dialog open", hits.n)
	}
	for _, k := range []string{"esc", "q", "n"} {
		m = dialogModel(t, &hits)
		if m, _ = press(m, "l", k); hits.n != 0 || m.overlay != overlayNone {
			t.Errorf("%s confirmed or did not close", k)
		}
	}
}

func TestConfirmRunsOnlyOnExplicitChoice(t *testing.T) {
	var hits confirmed
	m := dialogModel(t, &hits)
	m, _ = press(m, "l", "enter")
	if hits.n != 1 || m.overlay != overlayNone || m.confirm != nil {
		t.Fatalf("hits %d overlay %d", hits.n, m.overlay)
	}

	m = dialogModel(t, &hits)
	if m, _ = press(m, "4"); m.screen != screenOverview || m.overlay != overlayConfirm {
		t.Error("a key leaked through the dialog")
	}
}

func TestConfirmTextNamesTargetAndConsequence(t *testing.T) {
	var hits confirmed
	m := dialogModel(t, &hits)
	golden(t, "confirm-120x40", m.View().Content)
	v := m.View().Content
	for _, want := range []string{"Cancel run 0112dd6a?", "1 agent will be stopped", "worktrees are kept", "▸[ Cancel ]", "[ Stop the run ]", "CONFIRM"} {
		if !strings.Contains(v, want) {
			t.Errorf("dialog lacks %q", want)
		}
	}
	m, _ = press(m, "l")
	if v := m.View().Content; !strings.Contains(v, "▸[ Stop the run ]") {
		t.Error("selection does not move")
	}
}

func TestToastsExpireAndErrorsStay(t *testing.T) {
	m := testModel()
	m, cmd := m.notify("run 0112dd6a cancelled")
	if cmd == nil || !strings.Contains(m.View().Content, "✓ run 0112dd6a cancelled") {
		t.Fatal("info toast not shown with an expiry")
	}
	m = m.failed("cancel run", errors.New("state changed underneath"))
	if v := plain(m.View().Content); !strings.Contains(v, "✗ cancel run: state changed underneath  (+1 more)  · esc dismisses") {
		t.Errorf("error toast:\n%s", v)
	}
	res, _ := m.Update(toastExpiredMsg{id: 1})
	m = res.(model)
	res, _ = m.Update(toastExpiredMsg{id: 2})
	if m = res.(model); len(m.toasts) != 1 || !m.toasts[0].err {
		t.Fatalf("toasts %+v", m.toasts)
	}
	m.inspect = &inspector{id: "x"}
	m, _ = press(m, "esc")
	if len(m.toasts) != 0 || m.inspect == nil {
		t.Errorf("esc: toasts %d inspector %v", len(m.toasts), m.inspect != nil)
	}
}

func TestToastExpiresAfterTTL(t *testing.T) {
	old := toastTTL
	toastTTL = time.Millisecond
	t.Cleanup(func() { toastTTL = old })
	m, cmd := testModel().notify("saved")
	m = drive(t, m, cmd)
	if len(m.toasts) != 0 {
		t.Error("toast did not expire")
	}
}

var ansi = regexp.MustCompile("\x1b\\[[0-9;]*m")

func plain(s string) string { return ansi.ReplaceAllString(s, "") }
