package studio

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"caffe/internal/studio/ui"
)

type reportView struct {
	runID  string
	lines  []line
	err    error
	loaded bool
	scroll int
}

type line struct {
	text    string
	heading bool
}

type reportMsg struct {
	runID string
	md    string
	err   error
}

func (m model) openReport() (model, tea.Cmd) {
	id := m.live.runID
	if id == "" || m.src == nil {
		return m, nil
	}
	m.report, m.inspect = &reportView{runID: id}, nil
	src := m.src
	return m, func() tea.Msg {
		md, err := src.Report(context.Background(), id)
		return reportMsg{id, md, err}
	}
}

func (m model) updateReport(msg reportMsg) model {
	if m.report == nil || m.report.runID != msg.runID {
		return m
	}
	r := *m.report
	r.lines, r.err, r.loaded = markdownText(msg.md), msg.err, true
	m.report = &r
	return m
}

func (m model) reportScroll(delta int) model {
	r := *m.report
	r.scroll = min(max(r.scroll+delta, 0), max(len(r.lines)-1, 0))
	m.report = &r
	return m
}

func (m model) reportScreen(width, height int) string {
	r := m.report
	title := fmt.Sprintf(" Report · run %s", short(r.runID))
	if !r.loaded {
		title += " · loading…"
	}
	out := []string{title + "   (j/k scroll · g/G top/bottom · esc close)", ""}
	switch {
	case r.err != nil:
		out = append(out, " "+m.theme.Render(ui.ToneError, r.err.Error()))
	default:
		room := max(height-len(out), 1)
		scroll := min(r.scroll, max(len(r.lines)-room, 0))
		for _, l := range r.lines[scroll:min(scroll+room, len(r.lines))] {
			t := clip(l.text, width-1)
			if l.heading {
				t = m.theme.Render(ui.ToneActive, t)
			}
			out = append(out, t)
		}
	}
	return strings.Join(out, "\n")
}

func markdownText(md string) []line {
	var out []line
	var table [][]string
	flush := func() {
		if len(table) == 0 {
			return
		}
		ws := make([]int, len(table[0]))
		for _, row := range table {
			for i, c := range row {
				if i < len(ws) {
					ws[i] = max(ws[i], len([]rune(c)))
				}
			}
		}
		for _, row := range table {
			var b strings.Builder
			b.WriteString(" ")
			for i, c := range row {
				if i < len(ws) {
					fmt.Fprintf(&b, " %-*s ", ws[i], c)
				}
			}
			out = append(out, line{text: strings.TrimRight(b.String(), " ")})
		}
		table = nil
	}
	for _, raw := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		s := strings.TrimRight(raw, " ")
		if strings.HasPrefix(s, "|") {
			cells := strings.Split(strings.Trim(s, "|"), "|")
			sep := true
			for i := range cells {
				cells[i] = inline(strings.TrimSpace(cells[i]))
				if strings.Trim(cells[i], "-: ") != "" {
					sep = false
				}
			}
			if !sep {
				table = append(table, cells)
			}
			continue
		}
		flush()
		switch {
		case strings.HasPrefix(s, "# "):
			out = append(out, line{text: " " + strings.ToUpper(inline(s[2:])), heading: true})
		case strings.HasPrefix(s, "## "):
			out = append(out, line{text: " " + strings.ToUpper(inline(s[3:])), heading: true})
		case strings.HasPrefix(s, "### "):
			out = append(out, line{text: " " + inline(s[4:]), heading: true})
		case strings.HasPrefix(s, "- "):
			out = append(out, line{text: "  · " + inline(s[2:])})
		default:
			out = append(out, line{text: " " + inline(s)})
		}
	}
	flush()

	var dedup []line
	for _, l := range out {
		if strings.TrimSpace(l.text) == "" && len(dedup) > 0 && strings.TrimSpace(dedup[len(dedup)-1].text) == "" {
			continue
		}
		dedup = append(dedup, l)
	}
	return dedup
}

func inline(s string) string { return strings.NewReplacer("**", "", "`", "").Replace(s) }
