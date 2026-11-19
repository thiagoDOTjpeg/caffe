package studio

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	c "caffe/internal/contracts"
	"caffe/internal/service"
	"caffe/internal/studio/edit"
	"caffe/internal/studio/ui"
)

var leaseEvery = 5 * time.Second

type editing struct {
	holder  string
	leases  map[string]*service.Lease
	ticking bool
	insert  bool
	search  *string
	commit  *string
}

type leaseMsg struct {
	ws    string
	lease *service.Lease
	err   error
	next  string
}

type (
	leaseTickMsg  struct{}
	leaseRenewMsg struct{ lost map[string]error }
	savedMsg      struct {
		ws, path string
		file     *service.FileContent
		err      error
		text     string
	}
	manualMsg struct {
		ws, ok string
		err    error
		reload bool
	}
)

func (m model) leaseFor(ws string) *service.Lease {
	if m.ed.leases == nil {
		return nil
	}
	return m.ed.leases[ws]
}

func (m model) withLeases(fn func(map[string]*service.Lease)) model {
	next := map[string]*service.Lease{}
	for k, v := range m.ed.leases {
		next[k] = v
	}
	fn(next)
	m.ed.leases = next
	return m
}

func (m model) requireLease(next string) (model, tea.Cmd) {
	v := m.file
	if v == nil || !v.loaded || v.err != nil {
		return m, nil
	}
	if v.ws == service.MainWorkspace {
		if next == "editor" {
			return m.runEditor()
		}
		return m.failed("edit", errors.New("the main checkout is read-only in Studio; Caffè never writes there")), nil
	}
	if m.leaseFor(v.ws) != nil {
		return m.afterLease(next)
	}
	if m.src == nil {
		return m, nil
	}
	src, ws, holder := m.src, v.ws, m.ed.holder
	return m, func() tea.Msg {
		l, err := src.AcquireLease(context.Background(), ws, holder)
		return leaseMsg{ws: ws, lease: l, err: err, next: next}
	}
}

func (m model) afterLease(next string) (model, tea.Cmd) {
	if next == "editor" {
		return m.runEditor()
	}
	return m.startBuffer()
}

func (m model) startBuffer() (model, tea.Cmd) {
	v := *m.file
	switch {
	case v.file.Binary:
		return m.failed("edit", errors.New("read-only: binary file")), nil
	case v.file.Truncated:
		return m.failed("edit", errors.New("read-only: file too large to edit here; e opens $EDITOR")), nil
	}
	if v.buf == nil {
		v.buf = edit.New(v.file.Text)
		v.buf.MoveTo(v.scroll, 0, false)
	}
	m.file = &v
	m.ed.insert, m.ed.search = false, nil
	return m, nil
}

func (m model) updateEditing(msg tea.Msg) (model, tea.Cmd) {
	switch msg := msg.(type) {
	case leaseMsg:
		if msg.err != nil {
			return m.failed("edit", msg.err), nil
		}
		m = m.withLeases(func(l map[string]*service.Lease) { l[msg.ws] = msg.lease })
		var tick tea.Cmd
		if !m.ed.ticking {
			m.ed.ticking, tick = true, tea.Tick(leaseEvery, func(time.Time) tea.Msg { return leaseTickMsg{} })
		}
		if m.file == nil || m.file.ws != msg.ws {
			return m, tick
		}
		m, cmd := m.afterLease(msg.next)
		return m, tea.Batch(tick, cmd)
	case leaseTickMsg:
		if len(m.ed.leases) == 0 || m.src == nil {
			m.ed.ticking = false
			return m, nil
		}
		src, leases := m.src, m.ed.leases
		return m, func() tea.Msg {
			lost := map[string]error{}
			for ws, l := range leases {
				if err := src.RenewLease(context.Background(), l); err != nil {
					lost[ws] = err
				}
			}
			return leaseRenewMsg{lost}
		}
	case leaseRenewMsg:
		for ws, err := range msg.lost {
			if errors.Is(err, service.ErrLeaseHeld) {
				m = m.withLeases(func(l map[string]*service.Lease) { delete(l, ws) })
			}
			m = m.failed("lease", err)
		}
		return m, tea.Tick(leaseEvery, func(time.Time) tea.Msg { return leaseTickMsg{} })
	case savedMsg:
		return m.saved(msg)
	case manualMsg:
		if msg.err != nil {
			return m.failed("manual changes", msg.err), nil
		}
		if msg.reload {
			m = m.dropBuffers(msg.ws)
		}
		m, cmd := m.notify(msg.ok)
		m.tree.loaded = false
		m, tree := m.loadTree()
		return m, tea.Batch(cmd, tree)
	}
	return m, nil
}

func (m model) dropBuffers(ws string) model {
	var keep []*viewer
	for _, t := range m.tabs {
		if t.ws != ws {
			keep = append(keep, t)
		}
	}
	m.tabs = keep
	if m.file != nil && m.file.ws == ws {
		m.file, m.split, m.tree.focused = nil, nil, true
		if len(keep) > 0 {
			m.file = keep[len(keep)-1]
		}
	}
	if m.split != nil && m.split.ws == ws {
		m.split = nil
	}
	return m
}

func (m model) editing() bool {
	return m.screen == screenWorkspace && m.file != nil && m.file.buf != nil && !m.tree.focused && m.diff == nil && m.inspect == nil && m.report == nil
}

func (m model) follow() model {
	v := *m.file
	row, _ := v.buf.Cursor()
	room := max(m.layout().Main.H-4, 1)
	switch {
	case row < v.scroll:
		v.scroll = row
	case row >= v.scroll+room:
		v.scroll = row - room + 1
	}
	m.file = &v
	return m
}

func (m model) bufOp(fn func(b *edit.Buffer)) model {
	if !m.editing() {
		return m
	}
	fn(m.file.buf)
	return m.follow()
}

func (m model) enterInsert(fn func(b *edit.Buffer)) model {
	if !m.editing() {
		return m
	}
	m.file.buf.BeginGroup()
	if fn != nil {
		fn(m.file.buf)
	}
	m.ed.insert = true
	return m.follow()
}

func (m model) insertKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	b := m.file.buf
	switch msg.String() {
	case "esc":
		b.EndGroup()
		b.Move(0, -1, false)
		m.ed.insert = false
	case "ctrl+q", "ctrl+c":
		return m.run("app.quit")
	case "ctrl+s":
		b.EndGroup()
		m.ed.insert = false
		return m.save(false)
	case "enter":
		b.Newline()
	case "backspace":
		b.Backspace()
	case "delete":
		b.DeleteChar()
	case "tab":
		b.Insert("\t")
	case "left":
		b.Move(0, -1, true)
	case "right":
		b.Move(0, 1, true)
	case "up":
		b.Move(-1, 0, true)
	case "down":
		b.Move(1, 0, true)
	case "home":
		b.LineStart()
	case "end":
		b.LineEnd(true)
	default:
		if msg.Text != "" {
			b.Insert(msg.Text)
		}
	}
	return m.follow(), nil
}

func lineInput(field *string, msg tea.KeyPressMsg) (done, cancel bool) {
	switch msg.String() {
	case "esc":
		return false, true
	case "enter":
		return true, false
	case "backspace":
		if r := []rune(*field); len(r) > 0 {
			*field = string(r[:len(r)-1])
		}
	default:
		*field += msg.Text
	}
	return false, false
}

func (m model) searchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	q := *m.ed.search
	done, cancel := lineInput(&q, msg)
	switch {
	case cancel:
		m.ed.search = nil
	case done:
		m.ed.search = nil
		if !m.file.buf.Find(q, true) {
			return m.notify(fmt.Sprintf("%q not found", q))
		}
		return m.follow(), nil
	default:
		m.ed.search = &q
	}
	return m, nil
}

func (m model) findNext(forward bool) (model, tea.Cmd) {
	if !m.editing() {
		return m, nil
	}
	if !m.file.buf.Find("", forward) {
		return m.notify("no match")
	}
	return m.follow(), nil
}

func (m model) leaveEditor() (model, tea.Cmd) {
	if !m.editing() {
		return m, nil
	}
	drop := func(m model) (model, tea.Cmd) {
		v := *m.file
		v.buf = nil
		m.file = &v
		m.ed.insert, m.ed.search = false, nil
		return m, m.readFile(v.ws, v.path)
	}
	if !m.file.buf.Dirty() {
		return drop(m)
	}
	return m.ask("Discard unsaved changes to "+m.file.path+"?", "Discard", []string{"The file on disk stays as it was last saved."}, drop), nil
}

func (m model) save(overwrite bool) (model, tea.Cmd) {
	if !m.editing() || m.src == nil {
		return m, nil
	}
	v := m.file
	l := m.leaseFor(v.ws)
	if l == nil {
		return m.failed("save", errors.New("the worktree is no longer leased; i takes it again")), nil
	}
	src, ws, path, text := m.src, v.ws, v.path, v.buf.Text()
	req := service.SaveRequest{Lease: l, Path: path, Text: text, LoadedMod: v.file.ModTime, LoadedSize: v.file.Size, Overwrite: overwrite}
	return m, func() tea.Msg {
		f, err := src.SaveFile(context.Background(), ws, req)
		return savedMsg{ws: ws, path: path, file: f, err: err, text: text}
	}
}

func (m model) saved(msg savedMsg) (model, tea.Cmd) {
	var v *viewer
	for _, t := range m.tabs {
		if t.ws == msg.ws && t.path == msg.path {
			v = t
		}
	}
	if v == nil || v.buf == nil {
		return m, nil
	}
	if errors.Is(msg.err, service.ErrConflict) {
		return m.ask(msg.path+" changed on disk", "Overwrite", []string{
			"Someone changed the file after it was opened here.",
			"Overwrite replaces their version with this buffer; cancel keeps editing.",
		}, func(m model) (model, tea.Cmd) { return m.save(true) }), nil
	}
	if msg.err != nil {
		return m.failed("save", msg.err), nil
	}
	nv := *v
	nv.file, nv.disk = msg.file, ""
	if nv.buf.Text() == msg.text {
		nv.buf.MarkSaved()
	}
	nv.lines = strings.Split(strings.ReplaceAll(strings.TrimSuffix(msg.text, "\n"), "\t", "    "), "\n")
	if m.file == v {
		m.file = &nv
	}
	for i, t := range m.tabs {
		if t == v {
			m.tabs[i] = &nv
		}
	}
	m, cmd := m.notify("saved " + msg.path)
	m.tree.loaded = false
	m, tree := m.loadTree()
	return m, tea.Batch(cmd, tree)
}

func (m model) unsaved() []string {
	var out []string
	for _, t := range m.tabs {
		if t.buf != nil && t.buf.Dirty() {
			out = append(out, t.path)
		}
	}
	return out
}

func (m model) manualTarget() (string, *service.Lease, bool) {
	ws := m.wsID()
	l := m.leaseFor(ws)
	return ws, l, l != nil
}

func (m model) commitManual() (model, tea.Cmd) {
	if _, _, ok := m.manualTarget(); !ok {
		return m.notify("nothing to commit: this workspace is not leased (i in a file starts editing)")
	}
	if u := m.unsavedIn(m.wsID()); len(u) > 0 {
		return m.failed("commit", fmt.Errorf("save %s first (ctrl+s)", strings.Join(u, ", "))), nil
	}
	s := ""
	m.ed.commit = &s
	return m, nil
}

func (m model) unsavedIn(ws string) []string {
	var out []string
	for _, t := range m.tabs {
		if t.ws == ws && t.buf != nil && t.buf.Dirty() {
			out = append(out, t.path)
		}
	}
	return out
}

func (m model) commitKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := *m.ed.commit
	done, cancel := lineInput(&s, msg)
	switch {
	case cancel:
		m.ed.commit = nil
	case done:
		if strings.TrimSpace(s) == "" {
			return m, nil
		}
		m.ed.commit = nil
		ws, l, _ := m.manualTarget()
		src := m.src
		return m, func() tea.Msg {
			ok, err := src.CommitManual(context.Background(), l, s)
			text := "nothing to commit"
			if ok {
				text = "committed as \"manual: " + strings.TrimSpace(s) + "\""
			}
			return manualMsg{ws: ws, ok: text, err: err}
		}
	default:
		m.ed.commit = &s
	}
	return m, nil
}

func (m model) discardManual() (model, tea.Cmd) {
	ws, l, ok := m.manualTarget()
	if !ok {
		return m.notify("nothing to discard: this workspace is not leased")
	}
	src := m.src
	return m.ask("Discard every uncommitted change in "+m.ws.label()+"?", "Discard", []string{
		"Uncommitted edits and new files of the worktree are deleted; commits are kept.",
		"Open buffers of this workspace are closed.",
	}, func(m model) (model, tea.Cmd) {
		return m, func() tea.Msg {
			err := src.DiscardManual(context.Background(), l)
			return manualMsg{ws: ws, ok: "manual changes discarded", err: err, reload: true}
		}
	}), nil
}

func (m model) releaseLease() (model, tea.Cmd) {
	ws, l, ok := m.manualTarget()
	if !ok {
		return m.notify("this workspace is not leased")
	}
	if u := m.unsavedIn(ws); len(u) > 0 {
		return m.failed("release", fmt.Errorf("unsaved buffers: %s", strings.Join(u, ", "))), nil
	}
	if err := m.src.ReleaseLease(context.Background(), l); err != nil {
		return m.failed("release", err), nil
	}
	m = m.withLeases(func(x map[string]*service.Lease) { delete(x, ws) })
	for i, t := range m.tabs {
		if t.ws == ws && t.buf != nil {
			nv := *t
			nv.buf = nil
			m.tabs[i] = &nv
			if m.file == t {
				m.file = &nv
			}
		}
	}
	return m.notify("lease released; agents may write in " + m.ws.label() + " again")
}

func (m model) readOnlyReason(v *viewer) string {
	switch {
	case v.ws == service.MainWorkspace:
		return "read-only: main checkout"
	case m.ws.selected.ID == v.ws && m.ws.selected.TaskStatus == c.TaskRunning && m.leaseFor(v.ws) == nil:
		return "read-only: an agent is writing (task running)"
	}
	return ""
}

func (m model) editorTitle(v *viewer) string {
	b := v.buf
	mode := "EDIT"
	if m.ed.insert && m.file == v {
		mode = "INSERT"
	}
	row, col := b.Cursor()
	t := fmt.Sprintf(" %s · %s %d:%d", v.path, mode, row+1, col+1)
	if b.Dirty() {
		t += " · ● unsaved"
	}
	if v.disk != "" {
		t += " · changed on disk"
	}
	return t + "   (ctrl+s save · u undo · / find · esc leave)"
}

func expandTabs(s string) string { return strings.ReplaceAll(s, "\t", "    ") }

func displayCol(line string, col int) int {
	r := []rune(line)
	return len([]rune(expandTabs(string(r[:min(col, len(r))]))))
}

func (m model) editorLines(v *viewer, width, room int) []string {
	b := v.buf
	lines := b.Lines()
	row, col := b.Cursor()
	scroll := min(v.scroll, max(len(lines)-room, 0))
	end := min(scroll+room, len(lines))
	gutter := len(fmt.Sprint(len(lines)))
	text := max(width-gutter-4, 1)
	shown := make([]string, 0, end-scroll)
	for _, l := range lines[scroll:end] {
		shown = append(shown, expandTabs(l))
	}
	hl := highlight(v.lexer, shown, text, m.theme)
	var out []string
	for i := range shown {
		l := hl[i]
		if scroll+i == row && m.file == v {
			r := []rune(shown[i])
			at := min(displayCol(lines[row], col), len(r))
			cur := " "
			rest := ""
			if at < len(r) {
				cur, rest = string(r[at]), string(r[at+1:])
			}
			mark := "\x1b[7m" + cur + "\x1b[27m"
			if m.ed.insert {
				mark = m.theme.Render(ui.ToneActive, "▏") + cur
			}
			l = clip(string(r[:at]), text) + mark + clip(rest, max(text-at-1, 0))
		}
		num := m.theme.Render(ui.ToneMuted, fmt.Sprintf("%*d │", gutter, scroll+i+1))
		out = append(out, " "+num+" "+l)
	}
	return out
}
