package studio

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"

	"caffe/internal/service"
	"caffe/internal/studio/edit"
	"caffe/internal/studio/ui"
)

const (
	plainSize   = 2 << 20
	maxFileRead = 16 << 20
)

type viewer struct {
	ws     string
	path   string
	file   *service.FileContent
	lines  []string
	lexer  chroma.Lexer
	err    error
	loaded bool
	scroll int

	disk  string
	label string
	buf   *edit.Buffer
}

type fileMsg struct {
	ws, path string
	file     *service.FileContent
	err      error
}

func (m model) openFile(ws, path string) (model, tea.Cmd) {
	if m.src == nil {
		return m, nil
	}
	m.tree.focused = false
	for _, t := range m.tabs {
		if t.ws == ws && t.path == path {
			m.file = t
			m.fileGen++
			return m, watchFile(m.fileGen)
		}
	}
	m.file = &viewer{ws: ws, path: path, label: m.ws.label()}
	return m, m.readFile(ws, path)
}

func (m model) openAt(ws, path string, line int) (model, tea.Cmd) {
	m.screen, m.diff, m.inspect, m.report = screenWorkspace, nil, nil, nil
	m, read := m.openFile(ws, path)
	if m.file != nil {
		m.file.scroll = max(line-3, 0)
	}
	m, tree := m.loadTree()
	return m, tea.Batch(read, tree)
}

func (m model) readFile(ws, path string) tea.Cmd {
	src := m.src
	return func() tea.Msg {
		f, err := src.ReadFile(context.Background(), ws, path, maxFileRead)
		return fileMsg{ws, path, f, err}
	}
}

func (m model) updateFile(msg fileMsg) (model, tea.Cmd) {
	if m.file == nil || m.file.ws != msg.ws || m.file.path != msg.path {
		return m, nil
	}
	v := &viewer{ws: msg.ws, path: msg.path, file: msg.file, err: msg.err, loaded: true, scroll: m.file.scroll, disk: m.file.disk, label: m.file.label, buf: m.file.buf}
	if msg.err == nil && !msg.file.Binary {
		v.lines = strings.Split(strings.ReplaceAll(strings.TrimSuffix(msg.file.Text, "\n"), "\t", "    "), "\n")
		if msg.file.Size <= plainSize {
			v.lexer = lexers.Match(filepath.Base(msg.path))
		}
	}
	m.file = v
	m.fileGen++
	if msg.err != nil {
		return m, nil
	}
	return m, watchFile(m.fileGen)
}

func (m model) viewerScroll(delta int) model {
	if m.file == nil {
		return m
	}
	v := *m.file
	v.scroll = min(max(v.scroll+delta, 0), max(len(v.lines)-1, 0))
	m.file = &v
	return m
}

func (m model) viewerScreen(v *viewer, width, height int) string {
	title := " " + v.path
	switch {
	case !v.loaded:
		title += " · loading…"
	case v.err == nil && v.file.Truncated:
		title += fmt.Sprintf(" · first %d MB of %d MB", maxFileRead>>20, v.file.Size>>20)
	case v.err == nil && v.lexer == nil && v.file.Size > plainSize:
		title += " · plain (large file)"
	}
	switch v.disk {
	case "reloaded":
		title += " · changed on disk, reloaded"
	case "deleted":
		title += " · deleted on disk"
	}
	if v.loaded && v.err == nil {
		if ro := m.readOnlyReason(v); ro != "" {
			title += " · " + ro + "   (e $EDITOR · h tree)"
		} else {
			title += "   (i edit · e $EDITOR · h tree)"
		}
	}
	if v.buf != nil && v.loaded && v.err == nil {
		title = m.editorTitle(v)
		if m.ed.search != nil && m.file == v {
			title = " / " + *m.ed.search + "▏  (enter finds · esc cancels)"
		}
	}
	out := []string{m.theme.Render(ui.ToneAccent, clip(title, width-1)), ""}
	if v.buf != nil && v.loaded && v.err == nil {
		return strings.Join(append(out, m.editorLines(v, width, max(height-len(out), 1))...), "\n")
	}
	switch {
	case v.err != nil:
		return strings.Join(append(out, " "+m.theme.Render(ui.ToneError, v.err.Error())), "\n")
	case !v.loaded:
		return strings.Join(out, "\n")
	case v.file.Binary:
		return strings.Join(append(out, " binary file, not shown"), "\n")
	}
	room := max(height-len(out), 1)
	scroll := min(v.scroll, max(len(v.lines)-room, 0))
	visible := v.lines[scroll:min(scroll+room, len(v.lines))]
	gutter := len(fmt.Sprint(len(v.lines)))
	text := max(width-gutter-4, 1)
	for i, l := range highlight(v.lexer, visible, text, m.theme) {
		num := m.theme.Render(ui.ToneMuted, fmt.Sprintf("%*d │", gutter, scroll+i+1))
		out = append(out, " "+num+" "+l)
	}
	return strings.Join(out, "\n")
}

func highlight(lexer chroma.Lexer, lines []string, width int, th ui.Theme) []string {
	if lexer == nil {
		out := make([]string, len(lines))
		for i, l := range lines {
			out[i] = clip(l, width)
		}
		return out
	}
	it, err := chroma.Coalesce(lexer).Tokenise(nil, strings.Join(lines, "\n")+"\n")
	if err != nil {
		return highlight(nil, lines, width, th)
	}
	out := make([]string, 0, len(lines))
	var b strings.Builder
	left := width
	flush := func() { out = append(out, b.String()); b.Reset(); left = width }
	for tok := it(); tok != chroma.EOF; tok = it() {
		for i, part := range strings.Split(tok.Value, "\n") {
			if i > 0 {
				flush()
			}
			r := []rune(part)
			if left <= 0 || len(r) == 0 {
				continue
			}
			if len(r) > left {
				r = append(r[:left-1], '…')
			}
			left -= len(r)
			b.WriteString(th.Render(tone(tok.Type), string(r)))
		}
	}
	return out[:min(len(out), len(lines))]
}

func tone(t chroma.TokenType) ui.Tone {
	switch {
	case t.InCategory(chroma.Comment):
		return ui.ToneMuted
	case t.InCategory(chroma.Keyword):
		return ui.ToneAccent
	case t.InSubCategory(chroma.LiteralString):
		return ui.ToneOK
	case t.InSubCategory(chroma.LiteralNumber):
		return ui.ToneWarn
	case t == chroma.NameFunction:
		return ui.ToneActive
	}
	return ui.ToneNormal
}
