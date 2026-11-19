package studio

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rs/zerolog"
)

func testModel() model {
	m := newModel(nil, Project{Name: "demo", Root: "/p"}, &crashGuard{log: zerolog.Nop()})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return next.(model)
}

func press(m model, keys ...string) (model, tea.Cmd) {
	var cmd tea.Cmd
	for _, k := range keys {
		var next tea.Model
		next, cmd = m.Update(keyPress(k))
		m = next.(model)
	}
	return m, cmd
}

func quits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestRegistryIsValidAndEveryActionHasAHandler(t *testing.T) {
	r, h := actions()
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(h) != len(r.Actions()) {
		t.Errorf("%d handlers for %d actions", len(h), len(r.Actions()))
	}
	for _, a := range r.Actions() {
		if h[a.ID] == nil {
			t.Errorf("action %s has no handler", a.ID)
		}
	}
}

func TestQOnlyClosesQuitIsCtrlQ(t *testing.T) {
	m := testModel()
	if _, cmd := press(m, "q"); quits(cmd) {
		t.Error("q quit from a screen; it must only close views and overlays")
	}
	m, _ = press(m, "?")
	if m.overlay != overlayHelp {
		t.Fatal("? did not open help")
	}
	if m, _ = press(m, "q"); m.overlay != overlayNone {
		t.Error("q did not close the overlay")
	}
	if _, cmd := press(m, "ctrl+q"); !quits(cmd) {
		t.Error("ctrl+q did not quit")
	}
}

func TestHelpIsGeneratedFromTheRegistry(t *testing.T) {
	m, _ := press(testModel(), "?")
	body := m.View().Content
	for _, a := range m.registry.Help(m.scope()) {
		if !strings.Contains(body, a.Title) {
			t.Errorf("help lacks %q", a.Title)
		}
	}
	if m, _ = press(m, "?"); m.overlay != overlayNone {
		t.Error("? did not toggle help off")
	}
}

func TestPaletteRunsTheSelectedAction(t *testing.T) {
	m, _ := press(testModel(), ":", "a", "g", "e", "n")
	if m.overlay != overlayPalette || m.paletteQuery != "agen" {
		t.Fatalf("palette state: overlay=%d query=%q", m.overlay, m.paletteQuery)
	}

	m, _ = press(m, "backspace", "backspace", "backspace", "backspace", "q", "u")
	if m.overlay != overlayPalette || m.paletteQuery != "qu" {
		t.Fatalf("typing q in the palette: overlay=%d query=%q", m.overlay, m.paletteQuery)
	}
	m, _ = press(m, "backspace", "backspace", "a", "g", "e", "n", "enter")
	if m.overlay != overlayNone || m.screen != screenAgents {
		t.Errorf("enter: overlay=%d screen=%s", m.overlay, screenNames[m.screen])
	}

	m, _ = press(m, ":", "r", "u", "n", "s", "esc")
	if m.overlay != overlayNone || m.screen != screenAgents {
		t.Errorf("esc: overlay=%d screen=%s", m.overlay, screenNames[m.screen])
	}
}

func TestLeaderSequences(t *testing.T) {
	m, _ := press(testModel(), "space")
	if m.overlay != overlayLeader || !strings.Contains(m.View().Content, "go to screen") {
		t.Fatal("space did not open the leader menu with its groups")
	}
	m, _ = press(m, "g", "r")
	if m.overlay != overlayNone || m.screen != screenRuns {
		t.Errorf("space g r: overlay=%d screen=%s", m.overlay, screenNames[m.screen])
	}
	m, _ = press(m, "space", "v", "b")
	if m.layout().Sidebar.Visible() {
		t.Error("space v b did not hide the sidebar")
	}

	m, _ = press(m, "space", "z")
	if m.overlay != overlayNone {
		t.Error("unknown leader group kept the menu open")
	}
}
