package studio

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	goruntime "runtime"
	"strings"

	tea "charm.land/bubbletea/v2"

	c "caffe/internal/contracts"
)

type editorDoneMsg struct{ err error }

func editorCmd(env func(string) string, path string, line int) *exec.Cmd {
	words := strings.Fields(env("VISUAL"))
	if len(words) == 0 {
		words = strings.Fields(env("EDITOR"))
	}
	if len(words) == 0 {
		words = []string{"vi"}
		if goruntime.GOOS == "windows" {
			words = []string{"notepad"}
		}
	}
	name := words[0][strings.LastIndexAny(words[0], `/\`)+1:]
	name = strings.TrimSuffix(strings.ToLower(name), ".exe")
	args := words[1:]
	switch name {
	case "notepad":
		args = append(args, path)
	case "code", "codium", "cursor":
		args = append(args, "-g", fmt.Sprintf("%s:%d", path, line))
	case "subl", "hx", "helix", "zed":
		args = append(args, fmt.Sprintf("%s:%d", path, line))
	default:
		args = append(args, fmt.Sprintf("+%d", line), path)
	}
	return exec.Command(words[0], args...)
}

func (m model) editFile() (model, tea.Cmd) {
	v := m.file
	if v == nil || !v.loaded || v.err != nil {
		return m, nil
	}
	if m.ws.selected.TaskStatus == c.TaskRunning {
		return m.failed("edit", errors.New("an agent is writing in this worktree (task running); edit it when the task has finished")), nil
	}
	if m.watcher != nil {
		m.watcher.Pause()
	}
	cmd := editorCmd(os.Getenv, v.file.Path, v.scroll+1)
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return editorDoneMsg{err} })
}

func (m model) editorDone(msg editorDoneMsg) (model, tea.Cmd) {
	if m.watcher != nil {
		m.watcher.Resume()
	}
	if msg.err != nil {
		m = m.failed("editor", msg.err)
	}
	if m.file == nil {
		return m, nil
	}
	read := m.readFile(m.file.ws, m.file.path)
	m.tree.loaded = false
	m, tree := m.loadTree()
	return m, tea.Batch(read, tree)
}
