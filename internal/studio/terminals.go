package studio

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	goruntime "runtime"
	"strings"

	tea "charm.land/bubbletea/v2"

	"caffe/internal/service"
	"caffe/internal/session"
	"caffe/internal/studio/ui"
)

type terminals struct {
	mgr   *session.Manager
	notes chan int
	ws    map[int]service.Workspace
	sel   int

	typing bool
	input  string

	focus  bool
	cursor int
	scroll int
}

func newTerminals(logDir string) terminals {
	t := terminals{mgr: session.NewManager(logDir), notes: make(chan int, 1), ws: map[int]service.Workspace{}}
	t.mgr.Notify = func(int) {
		select {
		case t.notes <- 0:
		default:
		}
	}
	return t
}

type sessionMsg struct{}

type termStartedMsg struct {
	s    *session.Session
	ws   service.Workspace
	rest [][]string
	err  error
}

type termEndedMsg struct {
	s    *session.Session
	ws   service.Workspace
	rest [][]string
}

type termLinkMsg struct {
	ws   service.Workspace
	path string
	line int
	err  error
}

type shellReadyMsg struct {
	cmd *exec.Cmd
	err error
}

type shellDoneMsg struct{ err error }

func waitSession(ch chan int) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg { <-ch; return sessionMsg{} }
}

func (m model) updateTerminals(msg tea.Msg) (model, tea.Cmd) {
	switch msg := msg.(type) {
	case sessionMsg:
		return m, waitSession(m.terms.notes)
	case termStartedMsg:
		if msg.err != nil {
			return m.failed("run command", msg.err), nil
		}
		m.terms.ws[msg.s.Info().ID] = msg.ws
		m.terms.sel, m.terms.focus, m.terms.scroll = msg.s.Info().ID, false, 0
		if len(msg.rest) == 0 {
			return m, nil
		}
		return m, func() tea.Msg { <-msg.s.Done(); return termEndedMsg{msg.s, msg.ws, msg.rest} }
	case termEndedMsg:
		if i := msg.s.Info(); i.State != session.Exited || i.Code != 0 {
			return m, nil
		}
		return m, m.startIn(msg.ws, "validation", msg.rest)
	case termLinkMsg:
		if msg.err != nil {
			return m.failed("open link", msg.err), nil
		}
		m.ws.selected = msg.ws
		return m.openAt(m.wsID(), msg.path, msg.line)
	case shellReadyMsg:
		if msg.err != nil {
			return m.failed("shell", msg.err), nil
		}
		if m.watcher != nil {
			m.watcher.Pause()
		}
		return m, tea.ExecProcess(msg.cmd, func(err error) tea.Msg { return shellDoneMsg{err} })
	case shellDoneMsg:
		if m.watcher != nil {
			m.watcher.Resume()
		}
		if msg.err != nil {
			m = m.failed("shell", msg.err)
		}
		return m.refreshNow()
	}
	return m, nil
}

func (m model) startIn(ws service.Workspace, kind string, argvs [][]string) tea.Cmd {
	if m.src == nil || m.terms.mgr == nil || len(argvs) == 0 {
		return nil
	}
	src, mgr, label := m.src, m.terms.mgr, wsLabel(ws)
	id := ws.ID
	if id == "" {
		id = service.MainWorkspace
	}
	return func() tea.Msg {
		dir, err := src.ResolvePath(context.Background(), id, ".")
		if err != nil {
			return termStartedMsg{err: err}
		}
		return termStartedMsg{s: mgr.Start(kind, label, dir, argvs[0]), ws: ws, rest: argvs[1:]}
	}
}

func wsLabel(w service.Workspace) string { return workspaces{selected: w}.label() }

func (m model) selected() *session.Session {
	if m.terms.mgr == nil {
		return nil
	}
	if s := m.terms.mgr.Get(m.terms.sel); s != nil {
		return s
	}
	if all := m.terms.mgr.Sessions(); len(all) > 0 {
		return all[len(all)-1]
	}
	return nil
}

func (m model) termMove(d int) model {
	if m.terms.focus {
		if s := m.selected(); s != nil {
			m.terms.cursor = max(0, min(m.terms.cursor+d, len(s.Lines())-1))
		}
		return m
	}
	all := m.terms.mgr.Sessions()
	if len(all) == 0 {
		return m
	}
	i := len(all) - 1
	if s := m.selected(); s != nil {
		for j, x := range all {
			if x == s {
				i = j
			}
		}
	}
	i = max(0, min(i+d, len(all)-1))
	m.terms.sel, m.terms.scroll = all[i].Info().ID, 0
	return m
}

func (m model) termScroll(d int) model {
	if m.terms.focus {
		return m.termMove(d)
	}
	if s := m.selected(); s != nil {
		m.terms.scroll = max(0, min(m.terms.scroll-d, len(s.Lines())-1))
	}
	return m
}

func (m model) termFocus() model {
	s := m.selected()
	if s == nil {
		return m
	}
	lines := s.Lines()
	m.terms.focus, m.terms.cursor = true, max(len(lines)-1-m.terms.scroll, 0)
	for i := len(lines) - 1 - m.terms.scroll; i >= 0; i-- {
		if _, ok := findLink(lines[i]); ok {
			m.terms.cursor = i
			break
		}
	}
	return m
}

func (m model) termOpenLink() (model, tea.Cmd) {
	s := m.selected()
	if s == nil || m.src == nil {
		return m, nil
	}
	lines := s.Lines()
	if m.terms.cursor >= len(lines) {
		return m, nil
	}
	l, ok := findLink(lines[m.terms.cursor])
	if !ok {
		return m, nil
	}
	src, ws := m.src, m.terms.ws[s.Info().ID]
	id := ws.ID
	if id == "" {
		id = service.MainWorkspace
	}
	return m, func() tea.Msg {
		p, err := resolveLink(context.Background(), src, id, l)
		return termLinkMsg{ws: ws, path: p, line: l.line, err: err}
	}
}

func (m model) termNew() (model, tea.Cmd) {
	if m.terms.mgr == nil || m.src == nil {
		return m, nil
	}
	m.screen, m.inspect, m.report, m.diff = screenTerminals, nil, nil, nil
	m.terms.typing, m.terms.input = true, ""
	return m, nil
}

func (m model) termInputKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.terms.typing = false
	case "ctrl+q", "ctrl+c":
		return m.run("app.quit")
	case "backspace":
		if r := []rune(m.terms.input); len(r) > 0 {
			m.terms.input = string(r[:len(r)-1])
		}
	case "enter":

		argv := strings.Fields(m.terms.input)
		if len(argv) == 0 {
			return m, nil
		}
		m.terms.typing = false
		return m, m.startIn(m.ws.selected, "command", [][]string{argv})
	default:
		m.terms.input += msg.Text
	}
	return m, nil
}

func (m model) termValidate() (model, tea.Cmd) {
	if m.terms.mgr == nil || m.src == nil {
		return m, nil
	}
	var argvs [][]string
	for _, line := range m.src.ValidationCommands() {
		argvs = append(argvs, strings.Fields(line))
	}
	if len(argvs) == 0 {
		return m.notify("no validation.commands in .caffe/config.yaml")
	}
	m.screen, m.inspect, m.report, m.diff = screenTerminals, nil, nil, nil
	return m, m.startIn(m.ws.selected, "validation", argvs)
}

func (m model) termStop() (model, tea.Cmd) {
	s := m.selected()
	if s == nil || s.Info().State != session.Running {
		return m, nil
	}
	return m, func() tea.Msg { s.Stop(); return nil }
}

func (m model) termClose() (model, tea.Cmd) {
	s := m.selected()
	if s == nil {
		return m, nil
	}
	mgr, id := m.terms.mgr, s.Info().ID
	if s.Info().State != session.Running {
		mgr.Remove(id)
		m.terms.focus = false
		return m, nil
	}
	return m.ask(fmt.Sprintf("Stop and close session #%d?", id), "Stop and close",
		[]string{strings.Join(s.Info().Argv, " ") + " is still running; it and everything it started are stopped."},
		func(m model) (model, tea.Cmd) {
			m.terms.focus = false
			return m, func() tea.Msg { s.Stop(); mgr.Remove(id); return nil }
		}), nil
}

func shellCmd(env func(string) string, goos, dir string) *exec.Cmd {
	sh := env("SHELL")
	if goos == "windows" {
		sh = env("COMSPEC")
		if sh == "" {
			sh = "cmd.exe"
		}
	} else if sh == "" {
		sh = "sh"
	}
	cmd := exec.Command(sh)
	cmd.Dir = dir
	return cmd
}

func (m model) openShell() (model, tea.Cmd) {
	if m.src == nil {
		return m, nil
	}
	src, id := m.src, m.wsID()
	return m, func() tea.Msg {
		dir, err := src.ResolvePath(context.Background(), id, ".")
		if err != nil {
			return shellReadyMsg{err: err}
		}
		return shellReadyMsg{cmd: shellCmd(os.Getenv, goruntime.GOOS, dir)}
	}
}

func (m model) quit() (model, tea.Cmd) {
	if u := m.unsaved(); len(u) > 0 {
		return m.failed("quit", fmt.Errorf("unsaved buffers: %s; save them (ctrl+s) or leave the editor (esc)", strings.Join(u, ", "))), nil
	}
	if m.terms.mgr == nil {
		return m, tea.Quit
	}
	n := m.terms.mgr.Running()
	if n == 0 {
		return m, tea.Quit
	}
	return m.ask("Quit Caffè Studio?", "Stop them and quit", []string{
		fmt.Sprintf("%d terminal session(s) still running; quitting stops them and everything they started.", n),
		"Runs are separate processes and keep running.",
	}, func(m model) (model, tea.Cmd) { return m, tea.Quit }), nil
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b[^\[]|[\x00-\x08\x0b-\x1f\x7f]`)

func plainText(s string) string {
	return ansiRe.ReplaceAllString(strings.ReplaceAll(s, "\t", "    "), "")
}

func sessionState(i session.Info) string {
	switch i.State {
	case session.Exited:
		if i.Err != nil {
			return "error"
		}
		return fmt.Sprintf("exit %d", i.Code)
	default:
		return string(i.State)
	}
}

func (m model) stateTone(i session.Info) ui.Tone {
	switch {
	case i.State == session.Running:
		return ui.ToneActive
	case i.State == session.Exited && i.Code == 0 && i.Err == nil:
		return ui.ToneOK
	case i.State == session.Stopped:
		return ui.ToneMuted
	}
	return ui.ToneError
}

func (m model) terminalsScreen(width, height int) string {
	var b strings.Builder
	b.WriteString(" Terminals\n")
	if m.terms.typing {
		fmt.Fprintf(&b, " $ %s%s  %s\n", m.terms.input, m.theme.Render(ui.ToneActive, "▏"),
			m.theme.Render(ui.ToneMuted, "in "+m.ws.label()+" · enter runs (no shell) · esc cancels"))
	}
	var all []*session.Session
	if m.terms.mgr != nil {
		all = m.terms.mgr.Sessions()
	}
	if len(all) == 0 {
		if !m.terms.typing {
			b.WriteString("\n  No sessions. n runs a command in the selected workspace, v its validation commands, s opens a shell there.")
		}
		return b.String()
	}
	sel := m.selected()
	now := m.now()
	fmt.Fprintf(&b, "  %-3s %-10s %-24s %-26s %-9s %s\n", "#", "KIND", "WORKSPACE", "COMMAND", "STATE", "TIME")
	rows := max(min(len(all), height/3), 1)
	first := 0
	for i, s := range all {
		if s == sel {
			first = max(0, min(i-rows+1, len(all)-rows))
		}
	}
	for _, s := range all[first:min(len(all), first+rows)] {
		i := s.Info()
		marker := " "
		if s == sel {
			marker = "▸"
		}
		end := now
		if !i.Ended.IsZero() {
			end = i.Ended
		}
		fmt.Fprintf(&b, " %s%-3d %-10s %-24s %-26s %s %s\n", marker, i.ID, i.Kind, clip(i.Workspace, 24),
			clip(strings.Join(i.Argv, " "), 26), m.theme.Render(m.stateTone(i), fmt.Sprintf("%-9s", sessionState(i))), compact(end.Sub(i.Started)))
	}
	b.WriteString(m.theme.Render(ui.ToneMuted, " n new · v validation · s shell · c stop · x close · enter output, links") + "\n")
	info := sel.Info()
	title := fmt.Sprintf(" ─ #%d %s ── %s ", info.ID, clip(strings.Join(info.Argv, " "), max(width/2, 10)), sessionState(info))
	b.WriteString(title + strings.Repeat("─", max(width-len([]rune(title)), 0)) + "\n")
	if info.Err != nil {
		b.WriteString(" " + m.theme.Render(ui.ToneError, "✗ "+info.Err.Error()) + "\n")
	}
	room := max(height-strings.Count(b.String(), "\n")-1, 1)
	lines := sel.Lines()
	end := len(lines) - m.terms.scroll
	if m.terms.focus {
		end = max(min(len(lines), m.terms.cursor+room/2+1), min(room, len(lines)))
	}
	start := max(0, end-room)
	for i := start; i < end; i++ {
		text := clip(plainText(lines[i]), max(width-3, 1))
		if m.terms.focus && i == m.terms.cursor {
			if l, ok := findLink(lines[i]); ok {
				text += m.theme.Render(ui.ToneAccent, fmt.Sprintf("  ← enter opens %s:%d", l.path, l.line))
			}
			text = m.theme.Render(ui.ToneActive, "▸") + text
		} else {
			text = " " + text
		}
		b.WriteString(text + "\n")
	}
	if info.State == session.Running && len(lines) == 0 {
		b.WriteString(m.theme.Render(ui.ToneMuted, "  (no output yet)"))
	}
	return strings.TrimRight(b.String(), "\n")
}
