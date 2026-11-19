package studio

import (
	"fmt"
	"path"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"caffe/internal/studio/ui"
)

const maxFinderResults = 200

type finder struct {
	query string
	sel   int
}

func (m model) openFinder() (model, tea.Cmd) {
	if m.src == nil {
		return m, nil
	}
	m.overlay, m.find = overlayFinder, finder{}
	m.screen, m.diff, m.inspect, m.report = screenWorkspace, nil, nil, nil
	return m.loadTree()
}

func fuzzy(p, query string) (score int, ok bool) {
	lp, q := strings.ToLower(p), []rune(strings.ToLower(query))
	if len(q) == 0 {
		return len(p), true
	}

	base := len([]rune(p)) - len([]rune(path.Base(p)))
	r := []rune(lp)
	i, last := len(q)-1, -1
	for j := len(r) - 1; j >= 0 && i >= 0; j-- {
		if r[j] != q[i] {
			continue
		}
		if last >= 0 {
			score += last - j - 1
		}
		if j < base {
			score += 2
		}
		last = j
		i--
	}
	return score*4 + len(p), i < 0
}

func (m model) finderMatches() []string {
	if m.tree.list == nil {
		return nil
	}
	type hit struct {
		p string
		s int
	}
	var hits []hit
	for _, p := range m.tree.list.Paths {
		if s, ok := fuzzy(p, m.find.query); ok {
			hits = append(hits, hit{p, s})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].s != hits[j].s {
			return hits[i].s < hits[j].s
		}
		return hits[i].p < hits[j].p
	})
	out := make([]string, 0, min(len(hits), maxFinderResults))
	for _, h := range hits[:min(len(hits), maxFinderResults)] {
		out = append(out, h.p)
	}
	return out
}

func (m model) finderKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.overlay = overlayNone
	case "ctrl+q", "ctrl+c":
		return m.run("app.quit")
	case "enter":
		matches := m.finderMatches()
		if m.find.sel < len(matches) {
			m.overlay = overlayNone
			return m.openFile(m.tree.ws, matches[m.find.sel])
		}
	case "up", "ctrl+k":
		m.find.sel = max(0, m.find.sel-1)
	case "down", "ctrl+j":
		m.find.sel = min(len(m.finderMatches())-1, m.find.sel+1)
	case "backspace":
		if r := []rune(m.find.query); len(r) > 0 {
			m.find.query, m.find.sel = string(r[:len(r)-1]), 0
		}
	default:
		if msg.Text != "" {
			m.find.query, m.find.sel = m.find.query+msg.Text, 0
		}
	}
	return m, nil
}

func (m model) finderView(height int) string {
	var b strings.Builder
	fmt.Fprintf(&b, " Find file · %s\n > %s▏\n\n", m.ws.label(), m.find.query)
	switch {
	case !m.tree.loaded && m.tree.list == nil:
		b.WriteString("  loading…")
		return b.String()
	case m.tree.err != nil:
		b.WriteString("  " + m.theme.Render(ui.ToneError, m.tree.err.Error()))
		return b.String()
	}
	matches := m.finderMatches()
	if len(matches) == 0 {
		b.WriteString("  no matching file")
	}
	room := max(height-5, 1)
	first := max(0, m.find.sel-room+1)
	for i := first; i < min(len(matches), first+room); i++ {
		marker := " "
		if i == m.find.sel {
			marker = "▸"
		}
		fmt.Fprintf(&b, " %s %s\n", marker, matches[i])
	}
	b.WriteString("\n  enter opens · esc closes")
	return b.String()
}
