package studio

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	c "caffe/internal/contracts"
	"caffe/internal/service"
	"caffe/internal/studio/ui"
)

const sideBySideWidth = 120

type diffView struct {
	task   service.TaskView
	diff   *service.Diff
	files  []dfile
	rows   []drow
	pairs  []pair
	side   bool
	scroll int
	err    error
	loaded bool
}

type dfile struct {
	path       string
	added, del bool
	adds, dels int
	row, pair  int
}

type drow struct {
	kind     byte
	text     string
	old, new int
	file     int
}

type pair struct{ l, r int }

type diffMsg struct {
	taskID string
	diff   *service.Diff
	err    error
}

func (m model) openDiff(t service.TaskView) (model, tea.Cmd) {
	if m.src == nil {
		return m, nil
	}
	if t.Role != c.RoleDeveloper {
		return m.failed("diff", fmt.Errorf("%s is a %s task; only developer tasks have changes", t.Key, t.Role)), nil
	}
	m.diff, m.inspect, m.report = &diffView{task: t}, nil, nil
	src, id := m.src, t.ID
	return m, func() tea.Msg {
		d, err := src.TaskDiff(context.Background(), id)
		return diffMsg{id, d, err}
	}
}

func (m model) openWorkspaceDiff() (model, tea.Cmd) {
	w := m.ws.selected
	if w.Kind != "task" {
		return m.failed("diff", errors.New("the main checkout has no task diff; select a task workspace (ctrl+w)")), nil
	}
	if m.live.snap != nil {
		for _, t := range m.live.snap.Tasks {
			if t.ID == w.TaskID {
				return m.openDiff(t)
			}
		}
	}
	return m.openDiff(service.TaskView{Task: c.Task{ID: w.TaskID, Key: w.TaskKey, RunID: w.RunID, Role: c.RoleDeveloper, Status: w.TaskStatus, BranchName: w.Branch}})
}

func (m model) updateDiff(msg diffMsg) model {
	if m.diff == nil || m.diff.task.ID != msg.taskID {
		return m
	}
	d := &diffView{task: m.diff.task, diff: msg.diff, err: msg.err, loaded: true}
	if msg.err == nil {
		d.files, d.rows = parseDiff(msg.diff.Text)
		d.pairs = pairRows(d.rows, d.files)
	}
	m.diff = d
	return m
}

func parseDiff(text string) ([]dfile, []drow) {
	var files []dfile
	var rows []drow
	old, nw := 0, 0
	for _, l := range strings.Split(text, "\n") {
		l = strings.ReplaceAll(l, "\t", "    ")
		f := len(files) - 1
		switch {
		case strings.HasPrefix(l, "diff --git "):
			p := l
			if i := strings.LastIndex(l, " b/"); i >= 0 {
				p = l[i+3:]
			}
			files = append(files, dfile{path: p, row: len(rows)})
			rows = append(rows, drow{kind: 'f', text: p, file: len(files) - 1})
		case f < 0:
		case strings.HasPrefix(l, "new file mode"):
			files[f].added = true
		case strings.HasPrefix(l, "deleted file mode"):
			files[f].del = true
		case strings.HasPrefix(l, "@@"):
			old, nw = hunkStart(l)
			rows = append(rows, drow{kind: 'h', text: l, file: f})
		case strings.HasPrefix(l, "--- "), strings.HasPrefix(l, "+++ "), strings.HasPrefix(l, "index "),
			strings.HasPrefix(l, "similarity "), strings.HasPrefix(l, "rename "), strings.HasPrefix(l, "old mode"), strings.HasPrefix(l, "new mode"):
		case strings.HasPrefix(l, "+"):
			rows = append(rows, drow{kind: '+', text: l[1:], new: nw, file: f})
			nw++
			files[f].adds++
		case strings.HasPrefix(l, "-"):
			rows = append(rows, drow{kind: '-', text: l[1:], old: old, file: f})
			old++
			files[f].dels++
		case strings.HasPrefix(l, " "):
			rows = append(rows, drow{kind: ' ', text: l[1:], old: old, new: nw, file: f})
			old++
			nw++
		case l != "":
			rows = append(rows, drow{kind: 'm', text: l, file: f})
		}
	}
	return files, rows
}

func hunkStart(h string) (old, nw int) {
	for _, f := range strings.Fields(h) {
		n, _, _ := strings.Cut(f[1:], ",")
		v, err := strconv.Atoi(n)
		switch {
		case err != nil:
		case f[0] == '-':
			old = v
		case f[0] == '+':
			nw = v
		}
	}
	return old, nw
}

func pairRows(rows []drow, files []dfile) []pair {
	var out []pair
	for i := 0; i < len(rows); {
		r := rows[i]
		if r.kind == 'f' {
			files[r.file].pair = len(out)
		}
		switch r.kind {
		case '-', '+':
			var dels, adds []int
			for ; i < len(rows) && rows[i].kind == '-'; i++ {
				dels = append(dels, i)
			}
			for ; i < len(rows) && rows[i].kind == '+'; i++ {
				adds = append(adds, i)
			}
			for j := range max(len(dels), len(adds)) {
				p := pair{-1, -1}
				if j < len(dels) {
					p.l = dels[j]
				}
				if j < len(adds) {
					p.r = adds[j]
				}
				out = append(out, p)
			}
		case ' ':
			out = append(out, pair{i, i})
			i++
		default:
			out = append(out, pair{i, i})
			i++
		}
	}
	return out
}

func (d *diffView) length() int {
	if d.side {
		return len(d.pairs)
	}
	return len(d.rows)
}

func (d *diffView) rowAt(i int) drow {
	if !d.side {
		return d.rows[i]
	}
	if p := d.pairs[i]; p.r >= 0 {
		return d.rows[p.r]
	} else {
		return d.rows[p.l]
	}
}

func (d *diffView) currentFile() int {
	if d.length() == 0 {
		return -1
	}
	return d.rowAt(min(d.scroll, d.length()-1)).file
}

func (m model) diffScroll(delta int) model {
	d := *m.diff
	d.scroll = min(max(d.scroll+delta, 0), max(d.length()-1, 0))
	m.diff = &d
	return m
}

func (m model) diffFile(delta int) model {
	d := *m.diff
	if len(d.files) == 0 {
		return m
	}
	f := min(max(d.currentFile()+delta, 0), len(d.files)-1)
	d.scroll = d.files[f].row
	if d.side {
		d.scroll = d.files[f].pair
	}
	m.diff = &d
	return m
}

func (m model) diffToggleSide() (model, tea.Cmd) {
	if m.width < sideBySideWidth && !m.diff.side {
		return m.notify(fmt.Sprintf("side by side needs %d columns", sideBySideWidth))
	}
	d := *m.diff
	f := d.currentFile()
	d.side = !d.side
	d.scroll = 0
	if f >= 0 {
		d.scroll = d.files[f].row
		if d.side {
			d.scroll = d.files[f].pair
		}
	}
	m.diff = &d
	return m, nil
}

func (m model) diffOpenFile() (model, tea.Cmd) {
	d := m.diff
	if !d.loaded || d.err != nil || d.length() == 0 {
		return m, nil
	}
	if d.diff.Source == service.DiffStored {
		return m.failed("open", fmt.Errorf("the worktree of %s was removed; only its stored diff is left", d.task.Key)), nil
	}
	top := d.rowAt(min(d.scroll, d.length()-1))
	f := d.files[top.file]
	if f.del {
		return m.notify(f.path + " was deleted by the task")
	}
	line := 1
	for i := min(d.scroll, d.length()-1); i < d.length(); i++ {
		if r := d.rowAt(i); r.file != top.file {
			break
		} else if r.new > 0 {
			line = r.new
			break
		}
	}
	t := d.task
	m.ws.selected = service.Workspace{ID: t.ID, Kind: "task", Branch: t.BranchName, RunID: t.RunID, TaskID: t.ID, TaskKey: t.Key, TaskStatus: t.Status}
	m.diff, m.screen = nil, screenWorkspace
	m, tree := m.loadTree()
	m, read := m.openFile(t.ID, f.path)
	m.file.scroll = line - 1
	return m, tea.Batch(tree, read)
}

func (m model) diffScreen(width, height int) string {
	d := m.diff
	title := fmt.Sprintf(" Diff · %s", d.task.Key)
	switch {
	case !d.loaded:
		title += " · loading…"
	case d.err == nil:
		if d.diff.Base != "" {
			title += " · vs " + short(d.diff.Base)
		}
		if d.diff.Source == service.DiffStored {
			title += " · stored (worktree removed)"
		}
		if d.diff.Truncated {
			title += " · truncated"
		}
	}
	out := []string{m.theme.Render(ui.ToneAccent, clip(title, width-1)) +
		m.theme.Render(ui.ToneMuted, "   (n/p file · s side by side · enter open · esc close)"), ""}
	switch {
	case d.err != nil:
		return strings.Join(append(out, " "+m.theme.Render(ui.ToneError, d.err.Error())), "\n")
	case !d.loaded:
		return strings.Join(out, "\n")
	case len(d.rows) == 0:
		return strings.Join(append(out, "  no committed changes"), "\n")
	}
	room := max(height-len(out), 1)
	lw := min(32, width/4)
	cur := d.currentFile()
	var list []string
	for i, f := range d.files {
		marker := " "
		if i == cur {
			marker = "▸"
		}
		tag := "M"
		switch {
		case f.added:
			tag = "A"
		case f.del:
			tag = "D"
		}
		list = append(list, fmt.Sprintf("%s%s %s", marker, tag, f.path))
		list = append(list, m.theme.Render(ui.ToneMuted, fmt.Sprintf("    +%d -%d", f.adds, f.dels)))
	}
	if first := max(0, 2*cur-room+2); first > 0 && first < len(list) {
		list = list[first:]
	}
	body := m.diffLines(width-lw-1, room)
	for i := range room {
		l := ""
		if i < len(list) {
			l = list[i]
		}
		if i < len(list) && i/2 == cur && strings.HasPrefix(l, "▸") {
			l = m.theme.Render(ui.ToneAccent, l)
		}
		out = append(out, ui.Fit(l, lw)+m.theme.Render(ui.ToneMuted, "│")+body[i])
	}
	return strings.Join(out, "\n")
}

func (m model) diffLines(width, height int) []string {
	d := m.diff
	out := make([]string, 0, height)
	start := min(d.scroll, max(d.length()-1, 0))
	for i := start; i < d.length() && len(out) < height; i++ {
		if !d.side {
			out = append(out, m.diffRow(d.rows[i], width, true))
			continue
		}
		p := d.pairs[i]
		if p.l == p.r && d.rows[p.l].kind != ' ' {
			out = append(out, m.diffRow(d.rows[p.l], width, true))
			continue
		}
		half := (width - 1) / 2
		side := func(idx int) string {
			if idx < 0 {
				return ""
			}
			return m.diffRow(d.rows[idx], half, false)
		}
		l, r := side(p.l), side(p.r)
		if p.l == p.r {
			ctx := d.rows[p.l]
			l = m.numbered(ctx.old, ' ', ctx.text, half)
			r = m.numbered(ctx.new, ' ', ctx.text, half)
		}
		out = append(out, ui.Fit(l, half)+m.theme.Render(ui.ToneMuted, "│")+r)
	}
	for len(out) < height {
		out = append(out, "")
	}
	return out
}

func (m model) diffRow(r drow, width int, unified bool) string {
	switch r.kind {
	case 'f':
		return m.theme.Render(ui.ToneAccent, clip(" ▌"+r.text, width))
	case 'h':
		return m.theme.Render(ui.ToneMuted, clip(" "+r.text, width))
	case 'm':
		return m.theme.Render(ui.ToneMuted, clip(" "+r.text, width))
	}
	n := r.new
	if r.kind == '-' {
		n = r.old
	}
	if unified && r.kind == ' ' {
		n = r.new
	}
	return m.numbered(n, r.kind, r.text, width)
}

func (m model) numbered(n int, kind byte, text string, width int) string {
	num := "    "
	if n > 0 {
		num = fmt.Sprintf("%4d", n)
	}
	body := clip(string(kind)+text, max(width-7, 1))
	switch kind {
	case '+':
		body = m.theme.Render(ui.ToneOK, body)
	case '-':
		body = m.theme.Render(ui.ToneError, body)
	}
	return " " + m.theme.Render(ui.ToneMuted, num+" │") + " " + body
}
