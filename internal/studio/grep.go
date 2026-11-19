package studio

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"caffe/internal/service"
	"caffe/internal/studio/ui"
)

// Content search (WS-08): type, enter searches; enter again opens the hit.

const maxSearchHits = 500

type grep struct {
	query   string
	ran     string // the query the results are for
	ws      string
	res     *service.SearchResult
	err     error
	running bool
	sel     int
}

type grepMsg struct {
	ws, query string
	res       *service.SearchResult
	err       error
}

func (m model) openGrep() (model, tea.Cmd) {
	if m.src == nil {
		return m, nil
	}
	if m.grep.ws != m.wsID() {
		m.grep = grep{ws: m.wsID()}
	}
	m.overlay = overlayGrep
	return m, nil
}

func (m model) updateGrep(msg grepMsg) model {
	if msg.ws != m.grep.ws || msg.query != m.grep.ran {
		return m // superseded
	}
	m.grep.res, m.grep.err, m.grep.running, m.grep.sel = msg.res, msg.err, false, 0
	return m
}

func (m model) grepKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	g := &m.grep
	switch msg.String() {
	case "esc":
		m.overlay = overlayNone
	case "ctrl+q", "ctrl+c":
		return m.run("app.quit")
	case "enter":
		if g.query != g.ran || g.res == nil {
			if strings.TrimSpace(g.query) == "" {
				return m, nil
			}
			g.ran, g.running, g.err = g.query, true, nil
			src, ws, q := m.src, g.ws, g.query
			return m, func() tea.Msg {
				r, err := src.Search(context.Background(), ws, q, maxSearchHits)
				return grepMsg{ws, q, r, err}
			}
		}
		if g.sel < len(g.res.Hits) {
			h := g.res.Hits[g.sel]
			m.overlay = overlayNone
			m.screen, m.diff, m.inspect, m.report = screenWorkspace, nil, nil, nil
			m, read := m.openFile(g.ws, h.Path)
			m.file.scroll = max(h.Line-3, 0) // a little context above the hit
			m, tree := m.loadTree()
			return m, tea.Batch(read, tree)
		}
	case "up", "ctrl+k":
		g.sel = max(0, g.sel-1)
	case "down", "ctrl+j":
		if g.res != nil {
			g.sel = min(len(g.res.Hits)-1, g.sel+1)
		}
	case "backspace":
		if r := []rune(g.query); len(r) > 0 {
			g.query = string(r[:len(r)-1])
		}
	default:
		if msg.Text != "" {
			g.query += msg.Text
		}
	}
	return m, nil
}

func (m model) grepView(width, height int) string {
	g := m.grep
	var b strings.Builder
	fmt.Fprintf(&b, " Search · %s\n > %s▏\n\n", m.ws.label(), g.query)
	switch {
	case g.running:
		b.WriteString("  searching…")
		return b.String()
	case g.err != nil:
		b.WriteString("  " + m.theme.Render(ui.ToneError, g.err.Error()))
		return b.String()
	case g.res == nil:
		b.WriteString("  enter searches (literal text; case-sensitive only with capitals)")
		return b.String()
	case len(g.res.Hits) == 0:
		fmt.Fprintf(&b, "  no match for %q", g.ran)
		return b.String()
	}
	more := ""
	if g.res.Truncated {
		more = fmt.Sprintf(", first %d", maxSearchHits)
	}
	fmt.Fprintf(&b, "%s\n", m.theme.Render(ui.ToneMuted, fmt.Sprintf("  %d hits%s · %s", len(g.res.Hits), more, g.res.Engine)))
	room := max(height-6, 1)
	first := max(0, g.sel-room+1)
	for i := first; i < min(len(g.res.Hits), first+room); i++ {
		h := g.res.Hits[i]
		marker := " "
		if i == g.sel {
			marker = "▸"
		}
		loc := fmt.Sprintf("%s:%d", h.Path, h.Line)
		fmt.Fprintf(&b, " %s %s  %s\n", marker, m.theme.Render(ui.ToneAccent, loc), clip(strings.TrimSpace(h.Text), max(width-len(loc)-6, 1)))
	}
	b.WriteString("\n  enter opens · edit the text and enter to search again · esc closes")
	return b.String()
}
