package studio

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"caffe/internal/studio/ui"
)

// confirmDialog asks before an action that cannot be undone
// (ux-specification §5). The title names the target, the body the
// consequence; "Cancel" is selected until the user moves off it.
type confirmDialog struct {
	title   string   // "Cancel run 0112dd6a?"
	body    []string // what will happen
	action  string   // label of the confirming button: "Stop the run"
	confirm handler  // runs only on an explicit choice of action
	onLeft  bool     // true: Cancel selected
}

// ask opens a confirm dialog with Cancel selected.
func (m model) ask(title, action string, body []string, confirm handler) model {
	m.confirm = &confirmDialog{title: title, body: body, action: action, confirm: confirm, onLeft: true}
	m.overlay = overlayConfirm
	return m
}

// confirmKey handles keys while the dialog is open: the dialog captures
// them all, so a stray key never reaches the screen behind it.
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

// confirmView draws the dialog box centred in a width×height region.
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
	buttons := len([]rune("[ Cancel ]   [ "+d.action+" ]")) + 2 // markers and the trailing space
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

// toastTTL is how long an information toast stays; errors stay until
// dismissed (ux-specification §6).
var toastTTL = 5 * time.Second

type toast struct {
	id   int
	text string
	err  bool
}

type toastExpiredMsg struct{ id int }

// notify shows an information toast that expires.
func (m model) notify(text string) (model, tea.Cmd) {
	m.toastSeq++
	id := m.toastSeq
	m.toasts = append(m.toasts, toast{id: id, text: text})
	return m, tea.Tick(toastTTL, func(time.Time) tea.Msg { return toastExpiredMsg{id} })
}

// failed shows an error toast; it stays until dismissed with esc.
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

// dismissErrors removes error toasts; it reports whether there were any.
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

// toastLine is the newest toast for the status bar, or "".
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
