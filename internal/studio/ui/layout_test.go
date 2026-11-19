package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func on() *bool  { v := true; return &v }
func off() *bool { v := false; return &v }

func TestComputeBreakpoints(t *testing.T) {
	cases := []struct {
		w, h          int
		t             Toggles
		mode          Mode
		sidebar, main int
		panel         int
	}{
		{59, 40, Toggles{}, ModeTooSmall, 0, 0, 0},
		{100, 15, Toggles{}, ModeTooSmall, 0, 0, 0},
		{60, 16, Toggles{}, ModeSingle, 0, 60, 0},
		{79, 40, Toggles{}, ModeSingle, 0, 79, 0},
		{200, 23, Toggles{}, ModeSingle, 0, 200, 0},
		{80, 24, Toggles{}, ModeCompact, 5, 75, 0},
		{119, 50, Toggles{}, ModeCompact, 5, 114, 0},
		{80, 24, Toggles{Panel: on()}, ModeCompact, 5, 75, 6},
		{80, 24, Toggles{Sidebar: off()}, ModeCompact, 0, 80, 0},
		{120, 30, Toggles{}, ModeFull, 22, 98, 8},
		{200, 60, Toggles{Panel: off()}, ModeFull, 22, 178, 0},
		{200, 60, Toggles{Sidebar: off()}, ModeFull, 0, 200, 8},
		{70, 20, Toggles{Sidebar: on(), Panel: on()}, ModeSingle, 0, 70, 0},
	}
	for _, c := range cases {
		l := Compute(c.w, c.h, c.t)
		if l.Mode != c.mode || l.Sidebar.W != c.sidebar || l.Main.W != c.main || l.Panel.H != c.panel {
			t.Errorf("%dx%d %+v: mode=%s sidebar=%d main=%d panel=%d; want %s %d %d %d",
				c.w, c.h, c.t, l.Mode, l.Sidebar.W, l.Main.W, l.Panel.H, c.mode, c.sidebar, c.main, c.panel)
		}
		if l.Mode == ModeTooSmall {
			continue
		}

		if got := l.Header.H + l.Main.H + l.Panel.H + l.Status.H; got != c.h {
			t.Errorf("%dx%d: rows add up to %d", c.w, c.h, got)
		}
		if l.Sidebar.Visible() && l.Sidebar.H != l.Main.H {
			t.Errorf("%dx%d: sidebar and main differ in height", c.w, c.h)
		}
	}
}

func TestFrameAlwaysFillsTheTerminalExactly(t *testing.T) {
	content := Content{
		Header:     "caffè ─ a project with a deliberately very long name that will not fit anywhere at all",
		Nav:        []NavItem{{"1", "Overview", true}, {"2", "Tasks", false}, {"3", "Agents — très long label", false}},
		Main:       strings.Repeat("main line with wide runes 漢字 and accents çàü\n", 80),
		PanelTitle: "events",
		Panel:      "event one\nevent two",
		Status:     strings.Repeat("status ", 60),
	}
	for w := 1; w <= 230; w += 7 {
		for h := 1; h <= 75; h += 4 {
			for i, tg := range []Toggles{{}, {Sidebar: off(), Panel: on()}} {
				l := Compute(w, h, tg)
				out := Frame(l, content, ThemeByName(Themes[(w+h+i)%len(Themes)], false))
				if l.Mode == ModeTooSmall && out == "" {
					continue
				}
				rows := strings.Split(out, "\n")
				if len(rows) != h {
					t.Fatalf("%dx%d (%s): %d lines", w, h, l.Mode, len(rows))
				}
				for i, r := range rows {
					if got := ansi.StringWidth(r); got != w {
						t.Fatalf("%dx%d (%s): line %d is %d wide: %q", w, h, l.Mode, i, got, r)
					}
				}
			}
		}
	}
}

func TestTooSmallSaysWhy(t *testing.T) {
	out := Frame(Compute(50, 12, Toggles{}), Content{}, ThemeByName("mono", false))
	if !strings.Contains(out, "needs at least 60×16 (now 50×12)") {
		t.Errorf("message missing:\n%s", out)
	}
}
