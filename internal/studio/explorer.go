package studio

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"caffe/internal/service"
	"caffe/internal/studio/ui"
)

type explorer struct {
	ws      string
	list    *service.FileList
	kids    map[string][]node
	dirMark map[string]bool
	open    map[string]bool
	cursor  int
	err     error
	loaded  bool
	focused bool
}

type node struct {
	name, path string
	dir        bool
}

type row struct {
	node
	depth int
}

type filesMsg struct {
	ws   string
	list *service.FileList
	err  error
}

func (m model) wsID() string {
	if m.ws.selected.ID == "" {
		return service.MainWorkspace
	}
	return m.ws.selected.ID
}

func (m model) loadTree() (model, tea.Cmd) {
	id := m.wsID()
	if m.src == nil || (m.tree.ws == id && m.tree.loaded && m.tree.err == nil) {
		return m, nil
	}
	if m.tree.ws == id {
		m.tree.loaded = false
	} else {
		m.tree = explorer{ws: id, focused: true}
	}
	src := m.src
	return m, func() tea.Msg {
		l, err := src.Files(context.Background(), id)
		return filesMsg{id, l, err}
	}
}

func (m model) updateFiles(msg filesMsg) model {
	if msg.ws != m.tree.ws {
		return m
	}
	t := explorer{ws: msg.ws, list: msg.list, err: msg.err, loaded: true, focused: m.tree.focused, open: m.tree.open, cursor: m.tree.cursor}
	if t.open == nil {
		t.open = map[string]bool{}
	}
	if msg.err == nil {
		t.kids, t.dirMark = index(msg.list)
	}
	m.tree = t
	return m
}

func index(l *service.FileList) (map[string][]node, map[string]bool) {
	kids := map[string][]node{}
	seen := map[string]bool{}
	for _, p := range l.Paths {
		for dir, child := path.Dir(p), p; ; dir, child = path.Dir(dir), dir {
			if dir == "." {
				dir = ""
			}
			if seen[child] {
				break
			}
			seen[child] = true
			kids[dir] = append(kids[dir], node{name: path.Base(child), path: child, dir: child != p})
			if dir == "" {
				break
			}
		}
	}
	for _, k := range kids {
		sort.Slice(k, func(i, j int) bool {
			if k[i].dir != k[j].dir {
				return k[i].dir
			}
			return k[i].name < k[j].name
		})
	}
	marked := map[string]bool{}
	for p := range l.Marks {
		for d := path.Dir(p); d != "."; d = path.Dir(d) {
			marked[d] = true
		}
	}
	return kids, marked
}

func (t explorer) rows() []row {
	var out []row
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		for _, n := range t.kids[dir] {
			out = append(out, row{n, depth})
			if n.dir && t.open[n.path] {
				walk(n.path, depth+1)
			}
		}
	}
	walk("", 0)
	return out
}

func (m model) treeMove(d int) model {
	m.tree.cursor = min(max(m.tree.cursor+d, 0), max(len(m.tree.rows())-1, 0))
	return m
}

func (m model) treeCursor() (row, bool) {
	r := m.tree.rows()
	if m.tree.cursor >= len(r) {
		return row{}, false
	}
	return r[m.tree.cursor], true
}

func (m model) treeOpen() (model, tea.Cmd) {
	r, ok := m.treeCursor()
	if !ok {
		return m, nil
	}
	if r.dir {
		m.tree.open = cloneSet(m.tree.open)
		m.tree.open[r.path] = !m.tree.open[r.path]
		return m, nil
	}
	if m.tree.list.Marks[r.path] == service.MarkDeleted {
		return m.notify(r.path + " was deleted by the task; its diff shows it")
	}
	return m.openFile(m.tree.ws, r.path)
}

func (m model) treeLeft() model {
	r, ok := m.treeCursor()
	if !ok {
		return m
	}
	if r.dir && m.tree.open[r.path] {
		m.tree.open = cloneSet(m.tree.open)
		delete(m.tree.open, r.path)
		return m
	}
	parent := path.Dir(r.path)
	for i, x := range m.tree.rows() {
		if x.path == parent {
			m.tree.cursor = i
		}
	}
	return m
}

func cloneSet(s map[string]bool) map[string]bool {
	out := make(map[string]bool, len(s)+1)
	for k, v := range s {
		out[k] = v
	}
	return out
}

func (m model) workspaceScreen(width, height int) string {
	ew := min(36, width/3)
	left := m.explorerLines(ew, height)
	right := []string{"", "  enter on a file opens it here"}
	if m.file != nil {
		right = strings.Split(m.buffersScreen(width-ew-1, height), "\n")
	}
	out := make([]string, height)
	for i := range out {
		r := ""
		if i < len(right) {
			r = right[i]
		}
		out[i] = ui.Fit(left[i], ew) + m.theme.Render(ui.ToneMuted, "│") + r
	}
	return strings.Join(out, "\n")
}

func (m model) explorerLines(width, height int) []string {
	t := m.tree
	title := " EXPLORER · " + m.ws.label()
	if t.focused || m.file == nil {
		title = m.theme.Render(ui.ToneAccent, title)
	}
	out := []string{title}
	switch {
	case !t.loaded && t.list == nil:
		out = append(out, "  loading…")
	case t.err != nil:
		out = append(out, " "+m.theme.Render(ui.ToneError, t.err.Error()))
	case len(t.kids[""]) == 0:
		out = append(out, "  no files")
	}
	git := m.gitLines(width)
	rows := t.rows()
	room := max(height-len(out)-len(git), 1)
	scroll := max(0, t.cursor-room+1)
	for i := scroll; i < min(len(rows), scroll+room); i++ {
		r := rows[i]
		marker, icon, mark := " ", "  ", " "
		if i == t.cursor {
			marker = "▸"
		}
		if r.dir {
			icon = "▸ "
			if t.open[r.path] {
				icon = "▾ "
			}
			if t.dirMark[r.path] {
				mark = "•"
			}
		} else if mk := t.list.Marks[r.path]; mk != "" {
			mark = mk
		}
		line := marker + mark + " " + strings.Repeat("  ", r.depth) + icon + r.name
		if mark != " " {
			tone := ui.ToneWarn
			if mark == service.MarkDeleted {
				tone = ui.ToneError
			}
			line = marker + m.theme.Render(tone, mark) + " " + strings.Repeat("  ", r.depth) + icon + r.name
		}
		out = append(out, clipStyled(line, width))
	}
	for len(out) < height-len(git) {
		out = append(out, "")
	}
	return append(out, git...)[:height]
}

func (m model) gitLines(width int) []string {
	l := m.tree.list
	if l == nil {
		return nil
	}
	out := []string{m.theme.Render(ui.ToneMuted, clip(" GIT · "+l.Branch, width))}
	var parts []string
	for _, p := range []struct {
		n    int
		what string
	}{{l.Conflicted, "conflicted"}, {l.Staged, "staged"}, {l.Unstaged, "modified"}, {l.Untracked, "untracked"}} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.what))
		}
	}
	switch {
	case len(parts) == 0:
		out = append(out, "  clean")
	case l.Conflicted > 0:
		out = append(out, "  "+m.theme.Render(ui.ToneError, clip(strings.Join(parts, " · "), width-3)))
	default:
		out = append(out, "  "+m.theme.Render(ui.ToneWarn, clip(strings.Join(parts, " · "), width-3)))
	}
	if l.Base != "" {
		committed := 0
		for _, mk := range l.Marks {
			if mk != service.MarkManual {
				committed++
			}
		}
		out = append(out, clip(fmt.Sprintf("  %d committed vs %s", committed, short(l.Base)), width))
	}
	return out
}

func clipStyled(s string, w int) string { return strings.TrimRight(ui.Fit(s, w), " ") }
