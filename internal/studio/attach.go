package studio

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	tea "charm.land/bubbletea/v2"

	"caffe/internal/service"
)

// Attach hand-off (CC-16): Studio gives the terminal to the runtime's
// attach command and takes it back when the user detaches, then re-reads
// everything (architecture §13, suspend/resume).

type (
	attachReadyMsg struct {
		agent string
		cmd   *exec.Cmd
		err   error
	}
	attachDoneMsg struct {
		agent string
		err   error
	}
)

// pauser is the watcher's suspend control; nil in tests without a watcher.
type pauser interface {
	Pause()
	Resume()
}

func (m model) canAttach() bool { return m.src != nil && m.src.CanAttach() }

func (m model) attach() (model, tea.Cmd) {
	if !m.canAttach() {
		return m.failed("attach", errors.New("this runtime cannot attach to agents")), nil
	}
	a, ok := m.focusedAgent()
	if !ok {
		return m, nil
	}
	if agentRank(a.Visual) == 5 {
		return m.failed("attach", fmt.Errorf("agent %s has finished; its output is in o or the inspector", shortN(a.ID, 9))), nil
	}
	src, id, name := m.src, a.ID, a.Name
	return m, func() tea.Msg {
		cmd, err := src.AttachAgent(context.Background(), id)
		return attachReadyMsg{name, cmd, err}
	}
}

func (m model) updateAttach(msg tea.Msg) (model, tea.Cmd) {
	switch msg := msg.(type) {
	case attachReadyMsg:
		if msg.err != nil {
			if errors.Is(msg.err, service.ErrConflict) {
				m = m.failed("attach", fmt.Errorf("%w; showing the current state", msg.err))
				return m.refreshNow()
			}
			return m.failed("attach", msg.err), nil
		}
		if m.watcher != nil {
			m.watcher.Pause()
		}
		agent := msg.agent
		return m, tea.ExecProcess(msg.cmd, func(err error) tea.Msg { return attachDoneMsg{agent, err} })
	case attachDoneMsg:
		if m.watcher != nil {
			m.watcher.Resume()
		}
		var cmds []tea.Cmd
		if msg.err != nil {
			m = m.failed("attach to "+msg.agent, msg.err)
		} else {
			var cmd tea.Cmd
			m, cmd = m.notify("detached from " + msg.agent)
			cmds = append(cmds, cmd)
		}
		// The world moved on while the terminal was handed over: re-read it all.
		m.output = agentOutput{on: m.output.on}
		m, cmd := m.refreshNow()
		return m, tea.Batch(append(cmds, cmd, checkRuntime(m.src))...)
	}
	return m, nil
}
