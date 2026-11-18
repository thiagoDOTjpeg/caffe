// Package ui holds Studio's presentation primitives: layout, theme and key
// bindings. It knows nothing about Caffè's domain; screens give it text.
package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Mode is how much of the layout fits (docs/studio/ux-specification.md §1.3).
type Mode int

const (
	ModeTooSmall Mode = iota // below the minimum: only a message
	ModeSingle               // header, main, status bar; sidebar and panel as overlays
	ModeCompact              // sidebar collapsed to keys; panel closed by default
	ModeFull                 // sidebar with labels; panel open by default
)

func (m Mode) String() string {
	return [...]string{"too-small", "single", "compact", "full"}[m]
}

const (
	MinWidth, MinHeight         = 60, 16
	CompactWidth, CompactHeight = 80, 24
	FullWidth, FullHeight       = 120, 30

	sidebarFull    = 22 // including its right border
	sidebarCompact = 5
	panelFull      = 8 // including its title line
	panelCompact   = 6
)

// Rect is a region of the terminal; W or H 0 means hidden.
type Rect struct{ X, Y, W, H int }

func (r Rect) Visible() bool { return r.W > 0 && r.H > 0 }

// Toggles are the user's overrides of the per-mode defaults; nil keeps the
// default. They only apply where the mode has room for the region.
type Toggles struct {
	Sidebar *bool
	Panel   *bool
}

// Layout is the result of Compute: where every region goes.
type Layout struct {
	Mode                                 Mode
	Width, Height                        int
	Header, Sidebar, Main, Panel, Status Rect
}

// Compute lays out a width×height terminal. Pure: no I/O, no state.
func Compute(width, height int, t Toggles) Layout {
	l := Layout{Width: width, Height: height}
	switch {
	case width < MinWidth || height < MinHeight:
		l.Mode = ModeTooSmall
		return l
	case width < CompactWidth || height < CompactHeight:
		l.Mode = ModeSingle
	case width < FullWidth || height < FullHeight:
		l.Mode = ModeCompact
	default:
		l.Mode = ModeFull
	}
	l.Header = Rect{0, 0, width, 1}
	l.Status = Rect{0, height - 1, width, 1}
	body := height - 2

	sidebarW, panelH := 0, 0
	switch l.Mode {
	case ModeCompact:
		sidebarW = pick(t.Sidebar, true, sidebarCompact)
		panelH = pick(t.Panel, false, panelCompact)
	case ModeFull:
		sidebarW = pick(t.Sidebar, true, sidebarFull)
		panelH = pick(t.Panel, true, panelFull)
	}
	if panelH > 0 {
		l.Panel = Rect{0, 1 + body - panelH, width, panelH}
	}
	middle := body - panelH
	if sidebarW > 0 {
		l.Sidebar = Rect{0, 1, sidebarW, middle}
	}
	l.Main = Rect{sidebarW, 1, width - sidebarW, middle}
	return l
}

// pick returns size when the region is shown (toggle, else the default).
func pick(toggle *bool, def bool, size int) int {
	show := def
	if toggle != nil {
		show = *toggle
	}
	if show {
		return size
	}
	return 0
}

// NavItem is one entry of the sidebar.
type NavItem struct {
	Key    string // "1".."9"
	Label  string
	Active bool
}

// Content is what the screens put in each region. Lines longer than their
// region are truncated; shorter ones are padded.
type Content struct {
	Header     string
	Nav        []NavItem
	Main       string
	PanelTitle string
	Panel      string
	Status     string
}

// Frame renders c into l with theme th. Every line is exactly l.Width cells
// wide and there are exactly l.Height lines: a terminal UI that overflows
// scrolls and tears. Styling is applied after fitting, so it never changes
// widths.
func Frame(l Layout, c Content, th Theme) string {
	if l.Mode == ModeTooSmall {
		msg := fmt.Sprintf("Caffè Studio needs at least %d×%d (now %d×%d)", MinWidth, MinHeight, l.Width, l.Height)
		if l.Width <= 0 || l.Height <= 0 {
			return ""
		}
		return lipgloss.Place(l.Width, l.Height, lipgloss.Center, lipgloss.Center, th.Render(ToneWarn, fit(msg, l.Width)))
	}
	rows := make([]string, 0, l.Height)
	rows = append(rows, th.Render(ToneAccent, fit(c.Header, l.Width)))

	side := sidebarLines(l, c.Nav)
	main := lines(c.Main, l.Main.H)
	for i := 0; i < l.Main.H; i++ {
		row := ""
		if l.Sidebar.Visible() {
			cell := fit(side[i].text, l.Sidebar.W-1)
			if side[i].active {
				cell = th.Render(ToneAccent, cell)
			}
			row = cell + th.Render(ToneMuted, "│")
		}
		rows = append(rows, row+fit(main[i], l.Main.W))
	}
	if l.Panel.Visible() {
		title := "─ " + c.PanelTitle + " "
		rows = append(rows, th.Render(ToneMuted, fit(title+strings.Repeat("─", max(0, l.Width-ansi.StringWidth(title))), l.Width)))
		for _, line := range lines(c.Panel, l.Panel.H-1) {
			rows = append(rows, fit(line, l.Width))
		}
	}
	rows = append(rows, th.Render(ToneMuted, fit(c.Status, l.Width)))
	return strings.Join(rows, "\n")
}

type sideLine struct {
	text   string
	active bool
}

func sidebarLines(l Layout, nav []NavItem) []sideLine {
	out := make([]sideLine, l.Sidebar.H)
	for i, n := range nav {
		if i >= len(out) {
			break
		}
		marker := " " // the ▸ marker shows the active item without colour
		if n.Active {
			marker = "▸"
		}
		text := marker + " " + n.Key + " " + n.Label
		if l.Mode == ModeCompact { // collapsed: marker, key, initial
			text = marker + n.Key + " " + firstRune(n.Label)
		}
		out[i] = sideLine{text: text, active: n.Active}
	}
	return out
}

// lines splits s into exactly n lines (cut or padded with empty lines).
func lines(s string, n int) []string {
	out := strings.Split(s, "\n")
	if s == "" {
		out = nil
	}
	for len(out) < n {
		out = append(out, "")
	}
	return out[:n]
}

// fit makes one line exactly w cells wide, ANSI- and wide-rune-aware.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if ansi.StringWidth(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", w-ansi.StringWidth(s))
}

func firstRune(s string) string {
	for _, r := range s {
		return string(r)
	}
	return ""
}
