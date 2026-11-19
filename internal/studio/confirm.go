package studio

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"caffe/internal/studio/ui"
)

type confirmDialog struct {
	title   string
	body    []string
	action  string
	confirm handler
	onLeft  bool
}

func (m model) ask(title, action string, body []string, confirm handler) model {
	m.confirm = &confirmDialog{title: title, body: body, action: action, confirm: confirm, onLeft: true}
	m.overlay = overlayConfirm
	return m
}

func (m model) confirmKey(k string) (tea.Model, tea.Cmd) {
	d := *m.confirm
	switch k {
	case "left", "h", "right", "l", "tab", "shift+tab":
		d.onLeft = !d.onLeft
	case "enter", "space":
		m.overlay, m.confirm = overlayNone, nil
		if d.onLeft {
			return m, nil
		}
		return d.confirm(m)
	case "esc", "q", "n":
		m.overlay, m.confirm = overlayNone, nil
		return m, nil
	case "ctrl+q", "ctrl+c":
		return m.run("app.quit")
	}
	m.confirm = &d
	return m, nil
}

func (m model) confirmView(width, height int) string {
	d := m.confirm
	cancel, act := "[ Cancel ]", "[ "+d.action+" ]"
	if d.onLeft {
		cancel = m.theme.Render(ui.ToneActive, "▸"+cancel)
		act = " " + act
	} else {
		cancel = " " + cancel
		act = m.theme.Render(ui.ToneActive, "▸"+act)
	}
	inner := len([]rune(d.title)) + 4
	for _, l := range d.body {
		inner = max(inner, len([]rune(l))+2)
	}
	buttons := len([]rune("[ Cancel ]   [ "+d.action+" ]")) + 2
	inner = min(max(inner, buttons+2), max(width-4, 10))

	top := "┌ " + d.title + " " + strings.Repeat("─", max(inner-len([]rune(d.title))-2, 0)) + "┐"
	lines := []string{top}
	for _, l := range d.body {
		lines = append(lines, "│ "+fmt.Sprintf("%-*s", inner-1, clip(l, inner-1))+"│")
	}
	pad := max(inner-buttons, 0)
	lines = append(lines, "│"+strings.Repeat(" ", pad)+cancel+"  "+act+" │")
	lines = append(lines, "└"+strings.Repeat("─", inner)+"┘")

	left := strings.Repeat(" ", max((width-inner-2)/2, 0))
	var b strings.Builder
	b.WriteString(strings.Repeat("\n", max((height-len(lines))/2, 0)))
	for i, l := range lines {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(left + l)
	}
	return b.String()
}

var toastTTL = 5 * time.Second

type toast struct {
	id   int
	text string
	err  bool
}

type toastExpiredMsg struct{ id int }

func (m model) notify(text string) (model, tea.Cmd) {
	m.toastSeq++
	id := m.toastSeq
	m.toasts = append(m.toasts, toast{id: id, text: text})
	return m, tea.Tick(toastTTL, func(time.Time) tea.Msg { return toastExpiredMsg{id} })
}

func (m model) failed(what string, err error) model {
	m.toastSeq++
	m.toasts = append(m.toasts, toast{id: m.toastSeq, text: what + ": " + err.Error(), err: true})
	return m
}

func (m model) expireToast(id int) model {
	var keep []toast
	for _, t := range m.toasts {
		if t.id != id || t.err {
			keep = append(keep, t)
		}
	}
	m.toasts = keep
	return m
}

func (m model) dismissErrors() (model, bool) {
	var keep []toast
	for _, t := range m.toasts {
		if !t.err {
			keep = append(keep, t)
		}
	}
	had := len(keep) != len(m.toasts)
	m.toasts = keep
	return m, had
}

func (m model) toastLine() string {
	if len(m.toasts) == 0 {
		return ""
	}
	t := m.toasts[len(m.toasts)-1]
	more := ""
	if n := len(m.toasts) - 1; n > 0 {
		more = fmt.Sprintf("  (+%d more)", n)
	}
	if t.err {
		return " " + m.theme.Render(ui.ToneError, "✗ "+t.text) + more + "  · esc dismisses"
	}
	return " " + m.theme.Render(ui.ToneOK, "✓ "+t.text) + more
}
