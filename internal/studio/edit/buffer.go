package edit

import "strings"

type state struct {
	lines    []string
	row, col int
}

type Buffer struct {
	lines     []string
	row, col  int
	newline   bool
	crlf      bool
	undo      []state
	redo      []state
	saved     []string
	grouping  bool
	lastQuery string
}

func New(text string) *Buffer {
	b := &Buffer{}
	b.crlf = strings.Contains(text, "\r\n")
	if b.crlf {
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}
	b.newline = strings.HasSuffix(text, "\n")
	b.lines = strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	b.MarkSaved()
	return b
}

func (b *Buffer) Text() string {
	nl := "\n"
	if b.crlf {
		nl = "\r\n"
	}
	s := strings.Join(b.lines, nl)
	if b.newline {
		s += nl
	}
	return s
}

func (b *Buffer) Lines() []string    { return b.lines }
func (b *Buffer) Cursor() (int, int) { return b.row, b.col }
func (b *Buffer) Dirty() bool        { return !equal(b.lines, b.saved) }
func (b *Buffer) MarkSaved()         { b.saved = append([]string(nil), b.lines...) }

func (b *Buffer) snapshot() state {
	return state{lines: append([]string(nil), b.lines...), row: b.row, col: b.col}
}

func (b *Buffer) restore(s state) {
	b.lines, b.row, b.col = s.lines, s.row, s.col
}

func (b *Buffer) change() {
	if !b.grouping {
		b.undo = append(b.undo, b.snapshot())
		b.redo = nil
	}
}

func (b *Buffer) BeginGroup() {
	b.EndGroup()
	b.undo = append(b.undo, b.snapshot())
	b.redo = nil
	b.grouping = true
}

func (b *Buffer) EndGroup() {
	if b.grouping {
		b.grouping = false
		if n := len(b.undo); n > 0 && equal(b.undo[n-1].lines, b.lines) {
			b.undo = b.undo[:n-1]
		}
	}
}

func equal(a, c []string) bool {
	if len(a) != len(c) {
		return false
	}
	for i := range a {
		if a[i] != c[i] {
			return false
		}
	}
	return true
}

func (b *Buffer) Undo() bool {
	b.EndGroup()
	if len(b.undo) == 0 {
		return false
	}
	b.redo = append(b.redo, b.snapshot())
	b.restore(b.undo[len(b.undo)-1])
	b.undo = b.undo[:len(b.undo)-1]
	return true
}

func (b *Buffer) Redo() bool {
	b.EndGroup()
	if len(b.redo) == 0 {
		return false
	}
	b.undo = append(b.undo, b.snapshot())
	b.restore(b.redo[len(b.redo)-1])
	b.redo = b.redo[:len(b.redo)-1]
	return true
}

func (b *Buffer) line() []rune { return []rune(b.lines[b.row]) }

func (b *Buffer) clamp(insert bool) {
	b.row = max(0, min(b.row, len(b.lines)-1))
	n := len(b.line())
	if !insert && n > 0 {
		n--
	}
	b.col = max(0, min(b.col, n))
}

func (b *Buffer) Move(dr, dc int, insert bool) {
	b.row += dr
	b.col += dc
	b.clamp(insert)
}

func (b *Buffer) MoveTo(row, col int, insert bool) {
	b.row, b.col = row, col
	b.clamp(insert)
}

func (b *Buffer) LineStart() { b.col = 0 }

func (b *Buffer) LineEnd(insert bool) {
	b.col = len(b.line())
	b.clamp(insert)
}

func (b *Buffer) Insert(s string) {
	b.change()
	for i, part := range strings.Split(s, "\n") {
		if i > 0 {
			b.splitLine()
		}
		r := b.line()
		ins := []rune(part)
		b.lines[b.row] = string(r[:b.col]) + part + string(r[b.col:])
		b.col += len(ins)
	}
}

func (b *Buffer) splitLine() {
	r := b.line()
	head, tail := string(r[:b.col]), string(r[b.col:])
	b.lines = append(b.lines[:b.row+1], append([]string{tail}, b.lines[b.row+1:]...)...)
	b.lines[b.row] = head
	b.row, b.col = b.row+1, 0
}

func (b *Buffer) Newline() {
	b.change()
	b.splitLine()
}

func (b *Buffer) OpenLine(below bool) {
	b.change()
	at := b.row
	if below {
		at++
	}
	b.lines = append(b.lines[:at], append([]string{""}, b.lines[at:]...)...)
	b.row, b.col = at, 0
}

func (b *Buffer) Backspace() {
	if b.col == 0 && b.row == 0 {
		return
	}
	b.change()
	if b.col > 0 {
		r := b.line()
		b.lines[b.row] = string(r[:b.col-1]) + string(r[b.col:])
		b.col--
		return
	}
	prev := []rune(b.lines[b.row-1])
	b.lines[b.row-1] += b.lines[b.row]
	b.lines = append(b.lines[:b.row], b.lines[b.row+1:]...)
	b.row, b.col = b.row-1, len(prev)
}

func (b *Buffer) DeleteChar() {
	r := b.line()
	if b.col >= len(r) {
		return
	}
	b.change()
	b.lines[b.row] = string(r[:b.col]) + string(r[b.col+1:])
	b.clamp(false)
}

func (b *Buffer) DeleteLine() {
	b.change()
	if len(b.lines) == 1 {
		b.lines[0] = ""
	} else {
		b.lines = append(b.lines[:b.row], b.lines[b.row+1:]...)
	}
	b.clamp(false)
}

func (b *Buffer) Find(query string, forward bool) bool {
	if query == "" {
		query = b.lastQuery
	}
	if query == "" {
		return false
	}
	b.lastQuery = query
	n := len(b.lines)
	for i := 0; i <= n; i++ {
		row := b.row + i
		if !forward {
			row = b.row - i
		}
		row = ((row % n) + n) % n
		r := []rune(b.lines[row])
		from, to := 0, len(r)
		if i == 0 {
			if forward {
				from = min(b.col+1, len(r))
			} else {
				to = b.col
			}
		}
		if i == n {
			if forward {
				to = b.col + 1
			} else {
				from = b.col
			}
		}
		if from > to {
			continue
		}
		seg := string(r[from:to])
		idx := strings.Index(seg, query)
		if !forward {
			idx = strings.LastIndex(seg, query)
		}
		if idx >= 0 {
			b.row, b.col = row, from+len([]rune(seg[:idx]))
			return true
		}
	}
	return false
}

func (b *Buffer) LastQuery() string { return b.lastQuery }
