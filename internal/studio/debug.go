package studio

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"caffe/internal/dap"
	"caffe/internal/service"
	"caffe/internal/studio/ui"
)

type varRow struct {
	v     dap.Variable
	depth int
}

type debugger struct {
	cfg      dap.Config
	bps      map[string][]int
	sess     *dap.Session
	ws       service.Workspace
	test     string
	state    string
	thread   int
	frames   []dap.Frame
	sel      int
	vars     []varRow
	vsel     int
	onVars   bool
	console  []string
	input    *string
	source   []string
	srcPath  string
	startErr string
}

type (
	dbgStartedMsg struct {
		sess *dap.Session
		bps  map[string][]dap.Breakpoint
		err  error
	}
	dbgEventMsg struct {
		sess *dap.Session
		ev   dap.Event
		done bool
	}
	dbgStackMsg struct {
		frames []dap.Frame
		vars   []dap.Variable
		err    error
	}
	dbgVarsMsg struct {
		frame int
		vars  []dap.Variable
		err   error
	}
	dbgExpandMsg struct {
		at   int
		vars []dap.Variable
		err  error
	}
	dbgSourceMsg struct {
		path  string
		lines []string
	}
	dbgEvalMsg struct{ text string }
)

var testFuncRe = regexp.MustCompile(`^func (Test\w+|Benchmark\w+|Fuzz\w+)\(`)

func enclosingTest(lines []string, row int) (string, bool) {
	for i := min(row, len(lines)-1); i >= 0; i-- {
		if m := testFuncRe.FindStringSubmatch(lines[i]); m != nil {
			return m[1], true
		}
	}
	return "", false
}

func (m model) withBreakpoints(fn func(map[string][]int)) model {
	next := map[string][]int{}
	for k, v := range m.dbg.bps {
		next[k] = append([]int(nil), v...)
	}
	fn(next)
	m.dbg.bps = next
	return m
}

func (m model) toggleBreakpoint() (model, tea.Cmd) {
	v := m.file
	if v == nil || v.file == nil {
		return m, nil
	}
	line := v.scroll + 1
	if m.editing() {
		r, _ := v.buf.Cursor()
		line = r + 1
	}
	abs := v.file.Path
	on := true
	m = m.withBreakpoints(func(b map[string][]int) {
		lines := b[abs]
		for i, l := range lines {
			if l == line {
				b[abs] = append(lines[:i], lines[i+1:]...)
				on = false
				return
			}
		}
		b[abs] = append(lines, line)
		sort.Ints(b[abs])
	})
	var cmd tea.Cmd
	if s := m.dbg.sess; s != nil {
		lines := m.dbg.bps[abs]
		cmd = func() tea.Msg {
			s.SetBreakpoints(context.Background(), abs, lines)
			return nil
		}
	}
	verb := "set"
	if !on {
		verb = "cleared"
	}
	m, toast := m.notify(fmt.Sprintf("breakpoint %s at %s:%d", verb, v.path, line))
	return m, tea.Batch(cmd, toast)
}

func (m model) hasBreakpoint(abs string, line int) bool {
	for _, l := range m.dbg.bps[abs] {
		if l == line {
			return true
		}
	}
	return false
}

func (m model) debugHere() (model, tea.Cmd) {
	v := m.file
	if v == nil || v.file == nil {
		return m, nil
	}
	row := v.scroll
	lines := strings.Split(v.file.Text, "\n")
	if m.editing() {
		row, _ = v.buf.Cursor()
		lines = v.buf.Lines()
	}
	name, ok := enclosingTest(lines, row)
	if !ok {
		return m.notify("no test function at or above this line")
	}
	return m.debugTest(m.ws.selected, v.path, name)
}

func (m model) debugTest(ws service.Workspace, rel, name string) (model, tea.Cmd) {
	if m.src == nil || m.dbg.cfg.Name == "" {
		return m, nil
	}
	if m.dbg.sess != nil {
		return m.failed("debug", fmt.Errorf("a debug session is running (%s); x stops it", m.dbg.test)), nil
	}
	id := ws.ID
	if id == "" {
		id = service.MainWorkspace
	}
	src, cfg, bps := m.src, m.dbg.cfg, m.dbg.bps
	m.dbg = debugger{cfg: cfg, bps: bps, ws: ws, test: name, state: "starting"}
	m.screen, m.diff, m.inspect, m.report = screenDebug, nil, nil, nil
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		abs, err := src.ResolvePath(ctx, id, rel)
		if err != nil {
			return dbgStartedMsg{err: err}
		}
		pkg := filepath.Dir(abs)
		s, err := dap.Start(ctx, cfg, pkg)
		if err != nil {
			return dbgStartedMsg{err: err}
		}
		set, err := s.Launch(ctx, dap.LaunchRequest{Mode: "test", Program: pkg, Cwd: pkg, Args: []string{"-test.run", "^" + name + "$"}}, bps)
		if err != nil {
			s.Disconnect(context.Background())
			return dbgStartedMsg{err: err}
		}
		return dbgStartedMsg{sess: s, bps: set}
	}
}

func waitDbg(s *dap.Session) tea.Cmd {
	return func() tea.Msg {
		select {
		case ev := <-s.Events():
			return dbgEventMsg{sess: s, ev: ev}
		case <-s.Done():
			return dbgEventMsg{sess: s, done: true}
		}
	}
}

func (m model) dbgLog(lines ...string) model {
	c := append(append([]string(nil), m.dbg.console...), lines...)
	if len(c) > 500 {
		c = c[len(c)-500:]
	}
	m.dbg.console = c
	return m
}

func (m model) fetchStack() tea.Cmd {
	s, thread := m.dbg.sess, m.dbg.thread
	return func() tea.Msg {
		ctx := context.Background()
		frames, err := s.StackTrace(ctx, thread)
		if err != nil || len(frames) == 0 {
			return dbgStackMsg{err: err}
		}
		vars, err := frameVars(ctx, s, frames[0].ID)
		return dbgStackMsg{frames: frames, vars: vars, err: err}
	}
}

func frameVars(ctx context.Context, s *dap.Session, frame int) ([]dap.Variable, error) {
	scopes, err := s.Scopes(ctx, frame)
	if err != nil || len(scopes) == 0 {
		return nil, err
	}
	return s.Variables(ctx, scopes[0].Ref)
}

func (m model) fetchSource() tea.Cmd {
	if m.dbg.sel >= len(m.dbg.frames) || m.src == nil {
		return nil
	}
	path := m.dbg.frames[m.dbg.sel].Path
	if path == m.dbg.srcPath {
		return nil
	}
	src, id := m.src, m.dbg.ws.ID
	if id == "" {
		id = service.MainWorkspace
	}
	return func() tea.Msg {
		f, err := src.ReadFile(context.Background(), id, path, maxFileRead)
		if err != nil {
			return dbgSourceMsg{path: path, lines: []string{"(" + err.Error() + ")"}}
		}
		return dbgSourceMsg{path: path, lines: strings.Split(expandTabs(f.Text), "\n")}
	}
}

func rows(vs []dap.Variable, depth int) []varRow {
	out := make([]varRow, len(vs))
	for i, v := range vs {
		out[i] = varRow{v: v, depth: depth}
	}
	return out
}

func (m model) updateDebug(msg tea.Msg) (model, tea.Cmd) {
	switch msg := msg.(type) {
	case dbgStartedMsg:
		if msg.err != nil {
			m.dbg.state, m.dbg.startErr = "failed", msg.err.Error()
			return m.failed("debug", msg.err), nil
		}
		m.dbg.sess, m.dbg.state = msg.sess, "running"
		m = m.dbgLog("debugging " + m.dbg.test)
		for file, bps := range msg.bps {
			for _, b := range bps {
				if !b.Verified {
					m = m.dbgLog(fmt.Sprintf("breakpoint %s:%d not set: %s", filepath.Base(file), b.Line, b.Message))
				}
			}
		}
		return m, waitDbg(msg.sess)
	case dbgEventMsg:
		if msg.sess != m.dbg.sess {
			return m, nil
		}
		if msg.done {
			m.dbg.sess, m.dbg.state = nil, "finished"
			return m.dbgLog("debug session ended"), nil
		}
		switch msg.ev.Kind {
		case "stopped":
			m.dbg.state, m.dbg.thread = "paused", msg.ev.ThreadID
			return m, tea.Batch(waitDbg(msg.sess), m.fetchStack())
		case "continued":
			m.dbg.state = "running"
		case "output":
			m = m.dbgLog(strings.Split(strings.TrimRight(msg.ev.Output, "\n"), "\n")...)
		case "terminated", "exited":
			m.dbg.state = "finished"
			s := msg.sess
			return m, tea.Batch(waitDbg(s), func() tea.Msg { s.Disconnect(context.Background()); return nil })
		}
		return m, waitDbg(msg.sess)
	case dbgStackMsg:
		if msg.err != nil {
			return m.dbgLog("✗ " + msg.err.Error()), nil
		}
		m.dbg.frames, m.dbg.sel, m.dbg.vars, m.dbg.vsel = msg.frames, 0, rows(msg.vars, 0), 0
		m.dbg.srcPath = ""
		return m, m.fetchSource()
	case dbgVarsMsg:
		if msg.err == nil && m.dbg.sel < len(m.dbg.frames) && m.dbg.frames[m.dbg.sel].ID == msg.frame {
			m.dbg.vars, m.dbg.vsel = rows(msg.vars, 0), 0
		}
	case dbgExpandMsg:
		if msg.err != nil || msg.at >= len(m.dbg.vars) {
			return m, nil
		}
		parent := m.dbg.vars[msg.at]
		kids := rows(msg.vars, parent.depth+1)
		next := append(append(append([]varRow(nil), m.dbg.vars[:msg.at+1]...), kids...), m.dbg.vars[msg.at+1:]...)
		m.dbg.vars = next
	case dbgSourceMsg:
		m.dbg.srcPath, m.dbg.source = msg.path, msg.lines
	case dbgEvalMsg:
		return m.dbgLog(msg.text), nil
	}
	return m, nil
}

func (m model) dbgControl(what string) (model, tea.Cmd) {
	s := m.dbg.sess
	if s == nil {
		return m, nil
	}
	if what == "stop" {
		return m, func() tea.Msg { s.Disconnect(context.Background()); return nil }
	}
	if m.dbg.state != "paused" {
		return m.notify("the program is running; it can be stepped when it stops")
	}
	thread := m.dbg.thread
	m.dbg.state = "running"
	return m, func() tea.Msg {
		ctx := context.Background()
		var err error
		switch what {
		case "continue":
			err = s.Continue(ctx, thread)
		case "next":
			err = s.Next(ctx, thread)
		case "in":
			err = s.StepIn(ctx, thread)
		case "out":
			err = s.StepOut(ctx, thread)
		}
		if err != nil {
			return dbgEvalMsg{"✗ " + err.Error()}
		}
		return nil
	}
}

func (m model) dbgMove(d int) (model, tea.Cmd) {
	if m.dbg.onVars {
		m.dbg.vsel = max(0, min(m.dbg.vsel+d, len(m.dbg.vars)-1))
		return m, nil
	}
	if len(m.dbg.frames) == 0 {
		return m, nil
	}
	m.dbg.sel = max(0, min(m.dbg.sel+d, len(m.dbg.frames)-1))
	s, frame := m.dbg.sess, m.dbg.frames[m.dbg.sel].ID
	var vars tea.Cmd
	if s != nil {
		vars = func() tea.Msg {
			vs, err := frameVars(context.Background(), s, frame)
			return dbgVarsMsg{frame: frame, vars: vs, err: err}
		}
	}
	return m, tea.Batch(vars, m.fetchSource())
}

func (m model) dbgExpand() (model, tea.Cmd) {
	if !m.dbg.onVars || m.dbg.vsel >= len(m.dbg.vars) || m.dbg.sess == nil {
		return m, nil
	}
	at, row := m.dbg.vsel, m.dbg.vars[m.dbg.vsel]
	if row.v.Ref == 0 {
		return m, nil
	}
	if at+1 < len(m.dbg.vars) && m.dbg.vars[at+1].depth > row.depth {
		end := at + 1
		for end < len(m.dbg.vars) && m.dbg.vars[end].depth > row.depth {
			end++
		}
		m.dbg.vars = append(append([]varRow(nil), m.dbg.vars[:at+1]...), m.dbg.vars[end:]...)
		return m, nil
	}
	s := m.dbg.sess
	return m, func() tea.Msg {
		vs, err := s.Variables(context.Background(), row.v.Ref)
		return dbgExpandMsg{at: at, vars: vs, err: err}
	}
}

func (m model) dbgInputKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := *m.dbg.input
	done, cancel := lineInput(&s, msg)
	switch {
	case cancel:
		m.dbg.input = nil
	case done:
		m.dbg.input = nil
		sess := m.dbg.sess
		if sess == nil || strings.TrimSpace(s) == "" {
			return m, nil
		}
		frame := 0
		if m.dbg.sel < len(m.dbg.frames) {
			frame = m.dbg.frames[m.dbg.sel].ID
		}
		m = m.dbgLog("> " + s)
		return m, func() tea.Msg {
			out, err := sess.Evaluate(context.Background(), s, frame)
			if err != nil {
				return dbgEvalMsg{"✗ " + err.Error()}
			}
			return dbgEvalMsg{out}
		}
	default:
		m.dbg.input = &s
	}
	return m, nil
}

func (m model) debugScreen(width, height int) string {
	d := m.dbg
	if d.state == "" {
		return " Debug\n\n  No debug session. In a Go test file, T debugs the test around the line and b toggles a\n  breakpoint; in Terminals, D on a \"--- FAIL: TestX\" line debugs that test."
	}
	status := d.state
	if d.state == "paused" && len(d.frames) > 0 {
		f := d.frames[0]
		status = fmt.Sprintf("paused at %s:%d", filepath.Base(f.Path), f.Line)
	}
	if d.startErr != "" {
		status += ": " + d.startErr
	}
	out := []string{m.theme.Render(ui.ToneAccent, clip(fmt.Sprintf(" Debug · %s · %s · %s", d.cfg.Name, d.test, status), width-1))}
	lw := min(40, width/3)
	var left []string
	title := func(s string, on bool) string {
		if on {
			return m.theme.Render(ui.ToneAccent, " "+s)
		}
		return m.theme.Render(ui.ToneMuted, " "+s)
	}
	left = append(left, title("CALL STACK", !d.onVars))
	for i, f := range d.frames {
		marker := " "
		if i == d.sel {
			marker = "▸"
		}
		left = append(left, clip(fmt.Sprintf(" %s %s %s:%d", marker, f.Name, filepath.Base(f.Path), f.Line), lw))
	}
	left = append(left, "", title("VARIABLES", d.onVars))
	for i, r := range d.vars {
		marker := " "
		if i == d.vsel && d.onVars {
			marker = "▸"
		}
		more := ""
		if r.v.Ref > 0 {
			more = " +"
		}
		left = append(left, clip(fmt.Sprintf(" %s %s%s %s = %s%s", marker, strings.Repeat("  ", r.depth), r.v.Name, r.v.Type, r.v.Value, more), lw))
	}
	left = append(left, "", title("BREAKPOINTS", false))
	var files []string
	for f := range d.bps {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		for _, l := range d.bps[f] {
			left = append(left, clip(fmt.Sprintf("   ◆ %s:%d", filepath.Base(f), l), lw))
		}
	}
	consoleH := min(8, max(height/4, 3))
	body := max(height-consoleH-3, 3)
	var right []string
	cur := 0
	if d.sel < len(d.frames) {
		cur = d.frames[d.sel].Line
		right = append(right, m.theme.Render(ui.ToneMuted, " "+d.frames[d.sel].Path))
	}
	if len(d.source) > 0 && cur > 0 {
		first := max(0, min(cur-1-body/2, len(d.source)-body+1))
		for i := first; i < min(len(d.source), first+body-1); i++ {
			mark := "  "
			switch {
			case i+1 == cur:
				mark = m.theme.Render(ui.ToneActive, "▶ ")
			case m.hasBreakpoint(d.srcPath, i+1):
				mark = m.theme.Render(ui.ToneError, "◆ ")
			}
			right = append(right, clip(fmt.Sprintf(" %s%4d │ %s", mark, i+1, d.source[i]), width-lw-2))
		}
	}
	for i := range body {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out = append(out, ui.Fit(l, lw)+m.theme.Render(ui.ToneMuted, "│")+r)
	}
	out = append(out, m.theme.Render(ui.ToneMuted, " ─ console "+strings.Repeat("─", max(width-12, 0))))
	con := d.console
	if len(con) > consoleH-1 {
		con = con[len(con)-consoleH+1:]
	}
	for _, l := range con {
		out = append(out, " "+clip(plainText(l), width-2))
	}
	if d.input != nil {
		out = append(out, " eval: "+*d.input+m.theme.Render(ui.ToneActive, "▏"))
	}
	out = append(out, m.theme.Render(ui.ToneMuted, " c/F5 continue · n/F10 next · s/F11 into · o out · x stop · v stack/vars · enter expand · e eval"))
	return strings.Join(out, "\n")
}
