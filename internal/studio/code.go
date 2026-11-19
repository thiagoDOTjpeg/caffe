package studio

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"caffe/internal/lsp"
	"caffe/internal/studio/ui"
)

type codeIntel struct {
	mgr      *lsp.Manager
	notes    chan lsp.PublishDiagnostics
	diags    map[string][]lsp.Diagnostic
	versions map[string]int
	roots    map[string]string
	down     map[string]string
	list     *locList
}

type locItem struct {
	label  string
	path   string
	line   int
	inside bool
	sym    *lsp.Symbol
}

type locList struct {
	title string
	ws    string
	items []locItem
	sel   int
	file  string
}

type (
	diagMsg    struct{ d lsp.PublishDiagnostics }
	lspOpenMsg struct{ ws, abs, root, lang, err string }
	lspListMsg struct {
		list *locList
		err  error
	}
)

func newCode(configs ...lsp.ServerConfig) codeIntel {
	c := codeIntel{mgr: lsp.NewManager(3, configs...), notes: make(chan lsp.PublishDiagnostics, 64)}
	c.mgr.OnDiag = func(_ string, d lsp.PublishDiagnostics) {
		select {
		case c.notes <- d:
		default:
		}
	}
	return c
}

func waitDiag(ch chan lsp.PublishDiagnostics) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg { return diagMsg{<-ch} }
}

func copyMap[V any](in map[string]V) map[string]V {
	out := make(map[string]V, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (m model) lspSync(v *viewer, text string) tea.Cmd {
	if m.code.mgr == nil || m.src == nil || v == nil || v.file == nil || v.file.Binary || v.file.Truncated {
		return nil
	}
	if _, ok := m.code.mgr.ConfigFor(v.file.Path); !ok {
		return nil
	}
	src, mgr, ws, abs := m.src, m.code.mgr, v.ws, v.file.Path
	version := m.code.versions[abs] + 1
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		root, err := src.ResolvePath(ctx, ws, ".")
		if err != nil {
			return lspOpenMsg{ws: ws, abs: abs, err: err.Error()}
		}
		c, lang, err := mgr.For(ctx, abs, root)
		if err != nil {
			return lspOpenMsg{ws: ws, abs: abs, root: root, err: err.Error()}
		}
		if version == 1 {
			err = c.DidOpen(abs, lang, text, version)
		} else {
			err = c.DidChange(abs, version, text)
		}
		msg := lspOpenMsg{ws: ws, abs: abs, root: root, lang: lang}
		if err != nil {
			msg.err = err.Error()
		}
		return msg
	}
}

func (m model) updateCode(msg tea.Msg) (model, tea.Cmd) {
	switch msg := msg.(type) {
	case diagMsg:
		m.code.diags = copyMap(m.code.diags)
		m.code.diags[lsp.URIToPath(msg.d.URI)] = msg.d.Diagnostics
		return m, waitDiag(m.code.notes)
	case lspOpenMsg:
		if msg.root != "" {
			m.code.roots = copyMap(m.code.roots)
			m.code.roots[msg.ws] = msg.root
		}
		m.code.down = copyMap(m.code.down)
		if msg.err != "" {
			m.code.down[msg.ws] = msg.err
			return m, nil
		}
		delete(m.code.down, msg.ws)
		m.code.versions = copyMap(m.code.versions)
		m.code.versions[msg.abs]++
	case lspListMsg:
		if msg.err != nil {
			return m.failed("code", msg.err), nil
		}
		if len(msg.list.items) == 0 {
			return m.notify(msg.list.title + ": nothing found")
		}
		if len(msg.list.items) == 1 && msg.list.items[0].sym == nil && strings.HasPrefix(msg.list.title, "Definition") {
			return m.openLoc(msg.list.ws, msg.list.items[0])
		}
		m.code.list = msg.list
	}
	return m, nil
}

func (m model) fileDiags(v *viewer) []lsp.Diagnostic {
	if v == nil || v.file == nil {
		return nil
	}
	return m.code.diags[v.file.Path]
}

func diagSummary(ds []lsp.Diagnostic) string {
	e, w := 0, 0
	for _, d := range ds {
		if d.Severity == 1 {
			e++
		} else {
			w++
		}
	}
	var parts []string
	if e > 0 {
		parts = append(parts, fmt.Sprintf("%d error(s)", e))
	}
	if w > 0 {
		parts = append(parts, fmt.Sprintf("%d warning(s)", w))
	}
	return strings.Join(parts, ", ")
}

func (m model) gutterMark(ds []lsp.Diagnostic, line int) string {
	mark := " "
	for _, d := range ds {
		if d.Range.Start.Line == line {
			if d.Severity == 1 {
				return m.theme.Render(ui.ToneError, "●")
			}
			mark = m.theme.Render(ui.ToneWarn, "●")
		}
	}
	return mark
}

func (m model) relInWorkspace(ws, abs string) (string, bool) {
	root := m.code.roots[ws]
	if root == "" {
		return "", false
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func (m model) locItems(ws string, locs []lsp.Location, label func(lsp.Location) string) []locItem {
	var out []locItem
	for _, l := range locs {
		abs := l.Path()
		rel, inside := m.relInWorkspace(ws, abs)
		show := rel
		if !inside {
			show = abs
		}
		text := fmt.Sprintf("%s:%d", show, l.Range.Start.Line+1)
		if label != nil {
			text += "  " + label(l)
		}
		if !inside {
			text += "  (outside the workspace)"
		}
		out = append(out, locItem{label: text, path: rel, line: l.Range.Start.Line + 1, inside: inside})
	}
	return out
}

func (m model) openDiagnostics() (model, tea.Cmd) {
	v := m.file
	ds := m.fileDiags(v)
	if len(ds) == 0 {
		return m.notify("no diagnostics for this file")
	}
	ds = append([]lsp.Diagnostic(nil), ds...)
	sort.SliceStable(ds, func(i, j int) bool { return ds[i].Range.Start.Line < ds[j].Range.Start.Line })
	l := &locList{title: "Diagnostics · " + v.path, ws: v.ws}
	for _, d := range ds {
		sev := "warning"
		if d.Severity == 1 {
			sev = "error"
		}
		l.items = append(l.items, locItem{label: fmt.Sprintf("%d: %s: %s", d.Range.Start.Line+1, sev, d.Message), path: v.path, line: d.Range.Start.Line + 1, inside: true})
	}
	m.code.list = l
	return m, nil
}

func (m model) withClient(title string, fn func(ctx context.Context, c *lsp.Client, abs string) ([]locItem, error)) (model, tea.Cmd) {
	v := m.file
	if v == nil || v.file == nil || m.code.mgr == nil || m.src == nil {
		return m, nil
	}
	if _, ok := m.code.mgr.ConfigFor(v.file.Path); !ok {
		return m.notify("no language server for " + v.path)
	}
	if why := m.code.down[v.ws]; why != "" {
		return m.failed("code", fmt.Errorf("language server unavailable: %s", why)), nil
	}
	mgr, abs, root, ws := m.code.mgr, v.file.Path, m.code.roots[v.ws], v.ws
	if root == "" {
		return m.notify("the language server is still starting")
	}
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		c, _, err := mgr.For(ctx, abs, root)
		if err != nil {
			return lspListMsg{err: err}
		}
		items, err := fn(ctx, c, abs)
		return lspListMsg{list: &locList{title: title, ws: ws, items: items, file: abs}, err: err}
	}
}

func (m model) symbols() (model, tea.Cmd) {
	return m.withClient("Symbols", func(ctx context.Context, c *lsp.Client, abs string) ([]locItem, error) {
		syms, err := c.DocumentSymbols(ctx, abs)
		var out []locItem
		for i := range syms {
			s := syms[i]
			out = append(out, locItem{label: fmt.Sprintf("%s%s  %d", strings.Repeat("  ", s.Depth), s.Name, s.Range.Start.Line+1),
				line: s.Range.Start.Line + 1, inside: true, sym: &s})
		}
		return out, err
	})
}

func (m model) cursorPos() (lsp.Position, bool) {
	if !m.editing() {
		return lsp.Position{}, false
	}
	r, c := m.file.buf.Cursor()
	return lsp.Position{Line: r, Character: utf16Col(m.file.buf.Lines()[r], c)}, true
}

func utf16Col(line string, col int) int {
	n := 0
	for i, r := range []rune(line) {
		if i >= col {
			break
		}
		n++
		if r > 0xFFFF {
			n++
		}
	}
	return n
}

func (m model) navigate(refs bool, at *lsp.Position) (model, tea.Cmd) {
	pos, ok := m.cursorPos()
	if at != nil {
		pos, ok = *at, true
	}
	if !ok {
		return m.notify("move the cursor onto a name first (i edits; S lists symbols)")
	}
	title := "Definition"
	if refs {
		title = "References"
	}
	mm := m
	return m.withClient(title, func(ctx context.Context, c *lsp.Client, abs string) ([]locItem, error) {
		var locs []lsp.Location
		var err error
		if refs {
			locs, err = c.References(ctx, abs, pos)
		} else {
			locs, err = c.Definition(ctx, abs, pos)
		}
		return mm.locItems(mm.file.ws, locs, nil), err
	})
}

func (m model) openLoc(ws string, it locItem) (model, tea.Cmd) {
	if !it.inside {
		return m.failed("open", fmt.Errorf("%s is outside the workspace; Studio only opens files of the selected workspace", strings.Fields(it.label)[0])), nil
	}
	m.code.list = nil
	path := it.path
	if path == "" && m.file != nil {
		path = m.file.path
	}
	if m.editing() && m.file.path == path && m.file.ws == ws {
		m.file.buf.MoveTo(it.line-1, 0, false)
		return m.follow(), nil
	}
	return m.openAt(ws, path, it.line)
}

func (m model) listKey(k string) (tea.Model, tea.Cmd) {
	l := *m.code.list
	switch k {
	case "esc", "q":
		m.code.list = nil
		return m, nil
	case "ctrl+q", "ctrl+c":
		return m.run("app.quit")
	case "j", "down":
		l.sel = min(l.sel+1, len(l.items)-1)
	case "k", "up":
		l.sel = max(l.sel-1, 0)
	case "enter", "o":
		return m.openLoc(l.ws, l.items[l.sel])
	case "r":
		if s := l.items[l.sel].sym; s != nil {
			m.code.list = nil
			p := s.Range.Start
			return m.navigate(true, &p)
		}
	}
	m.code.list = &l
	return m, nil
}

func (m model) listView(height int) string {
	l := m.code.list
	var b strings.Builder
	b.WriteString(" " + l.title + "\n\n")
	room := max(height-5, 1)
	first := max(0, l.sel-room+1)
	for i := first; i < min(len(l.items), first+room); i++ {
		marker := " "
		if i == l.sel {
			marker = "▸"
		}
		text := l.items[i].label
		if !l.items[i].inside {
			text = m.theme.Render(ui.ToneMuted, text)
		}
		b.WriteString(" " + marker + " " + text + "\n")
	}
	hint := "\n  enter opens · esc closes"
	if len(l.items) > 0 && l.items[0].sym != nil {
		hint = "\n  enter opens · r references · esc closes"
	}
	b.WriteString(hint)
	return b.String()
}
