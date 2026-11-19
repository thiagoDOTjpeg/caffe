package studio

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"caffe/internal/service"
	"caffe/internal/studio/edit"
	"caffe/internal/studio/ui"
)

type overlay int

const (
	overlayNone overlay = iota
	overlayHelp
	overlayPalette
	overlayLeader
	overlayConfirm
	overlayNewRun
	overlayWorkspaces
	overlayFinder
	overlayGrep
)

type handler func(m model) (model, tea.Cmd)

func actions() (*ui.Registry, map[string]handler) {
	r := &ui.Registry{}
	h := map[string]handler{}
	add := func(a ui.Action, fn handler) {
		r.Add(a)
		h[a.ID] = fn
	}
	g := ui.ScopeGlobal
	r.Group("g", "go to screen")
	r.Group("v", "view")
	r.Group("r", "run")
	r.Group("a", "agent")
	r.Group("w", "workspace")

	add(ui.Action{ID: "app.quit", Title: "Quit Caffè Studio", Scope: g, Keys: []string{"ctrl+q", "ctrl+c"}},
		func(m model) (model, tea.Cmd) { return m.quit() })
	add(ui.Action{ID: "help.toggle", Title: "Show the keys of this view", Scope: g, Keys: []string{"?"}},
		func(m model) (model, tea.Cmd) { return m.toggleOverlay(overlayHelp), nil })
	add(ui.Action{ID: "palette.open", Title: "Command palette", Scope: g, Keys: []string{":", "ctrl+p"}},
		func(m model) (model, tea.Cmd) {
			m.overlay, m.paletteQuery, m.paletteSel = overlayPalette, "", 0
			return m, nil
		})
	add(ui.Action{ID: "leader.open", Title: "Leader menu", Scope: g, Keys: []string{"space"}},
		func(m model) (model, tea.Cmd) { m.overlay, m.leaderGroup = overlayLeader, ""; return m, nil })
	add(ui.Action{ID: "overlay.close", Title: "Close the overlay or view", Scope: g, Keys: []string{"esc", "q"}},
		func(m model) (model, tea.Cmd) {
			if m.overlay != overlayNone {
				m.overlay = overlayNone
				return m, nil
			}
			if next, had := m.dismissErrors(); had {
				return next, nil
			}
			if m.editing() {
				return m.leaveEditor()
			}
			m.inspect, m.report, m.diff = nil, nil, nil
			return m, nil
		})
	add(ui.Action{ID: "workspace.select", Title: "Select the workspace", Scope: g, Keys: []string{"ctrl+w"}, Leader: "w s"},
		func(m model) (model, tea.Cmd) { return m.openWorkspaces() })
	add(ui.Action{ID: "layout.sidebar", Title: "Toggle the sidebar", Scope: g, Keys: []string{"ctrl+b"}, Leader: "v b"},
		func(m model) (model, tea.Cmd) { m.toggles.Sidebar = flip(m.layout().Sidebar.Visible()); return m, nil })
	add(ui.Action{ID: "layout.panel", Title: "Toggle the bottom panel", Scope: g, Keys: []string{"`"}, Leader: "v p"},
		func(m model) (model, tea.Cmd) { m.toggles.Panel = flip(m.layout().Panel.Visible()); return m, nil })
	for i, name := range screenNames {
		s := screen(i)
		leaderKey := strings.ToLower(name[:1])
		if s == screenTerminals {
			leaderKey = "x"
		}
		add(ui.Action{ID: "screen." + strings.ToLower(name), Title: "Go to " + name, Scope: g,
			Keys: []string{fmt.Sprint(i + 1)}, Leader: "g " + leaderKey},
			func(m model) (model, tea.Cmd) {
				m.screen, m.overlay, m.inspect, m.report, m.diff = s, overlayNone, nil, nil, nil
				if s == screenWorkspace {
					return m.loadTree()
				}
				if s == screenIntegration {
					return m.loadIntegration()
				}
				return m, nil
			})
	}
	rs := ui.Scope("runs")
	move := func(d int) handler { return func(m model) (model, tea.Cmd) { return m.runsMove(d), nil } }
	add(ui.Action{ID: "runs.down", Title: "Next run", Scope: rs, Keys: []string{"j", "down"}}, move(1))
	add(ui.Action{ID: "runs.up", Title: "Previous run", Scope: rs, Keys: []string{"k", "up"}}, move(-1))
	add(ui.Action{ID: "runs.top", Title: "First run", Scope: rs, Keys: []string{"g", "home"}}, move(-1<<30))
	add(ui.Action{ID: "runs.bottom", Title: "Last run", Scope: rs, Keys: []string{"G", "end"}}, move(1<<30))
	add(ui.Action{ID: "runs.open", Title: "Open the run", Scope: rs, Keys: []string{"enter"}},
		func(m model) (model, tea.Cmd) { return m.runsOpen() })
	add(ui.Action{ID: "runs.filter", Title: "Filter runs", Scope: rs, Keys: []string{"/"}},
		func(m model) (model, tea.Cmd) { m.runs.filtering = true; return m, nil })
	add(ui.Action{ID: "runs.active", Title: "Show only active runs", Scope: rs, Keys: []string{"a"}},
		func(m model) (model, tea.Cmd) { m.runs.activeOnly = !m.runs.activeOnly; return m, nil })
	ts := ui.Scope("tasks")
	tmove := func(dc, dr int) handler { return func(m model) (model, tea.Cmd) { return m.tasksMove(dc, dr), nil } }
	add(ui.Action{ID: "tasks.left", Title: "Previous column", Scope: ts, Keys: []string{"h", "left"}}, tmove(-1, 0))
	add(ui.Action{ID: "tasks.right", Title: "Next column", Scope: ts, Keys: []string{"l", "right"}}, tmove(1, 0))
	add(ui.Action{ID: "tasks.down", Title: "Next task in the column", Scope: ts, Keys: []string{"j", "down"}}, tmove(0, 1))
	add(ui.Action{ID: "tasks.up", Title: "Previous task in the column", Scope: ts, Keys: []string{"k", "up"}}, tmove(0, -1))
	view := func(v taskView) handler {
		return func(m model) (model, tea.Cmd) { m.tasks.view = v; return m, nil }
	}
	add(ui.Action{ID: "tasks.board", Title: "Task board", Scope: ts, Keys: []string{"b"}}, view(viewBoard))
	add(ui.Action{ID: "tasks.graph", Title: "Task dependency graph", Scope: ts, Keys: []string{"g"}}, view(viewGraph))
	add(ui.Action{ID: "tasks.list", Title: "Task list", Scope: ts, Keys: []string{"L"}}, view(viewList))
	as := ui.Scope("agents")
	amove := func(d func(int) int) handler { return func(m model) (model, tea.Cmd) { return m.agentsMove(d), nil } }
	add(ui.Action{ID: "agents.left", Title: "Previous agent", Scope: as, Keys: []string{"h", "left"}}, amove(func(int) int { return -1 }))
	add(ui.Action{ID: "agents.right", Title: "Next agent", Scope: as, Keys: []string{"l", "right"}}, amove(func(int) int { return 1 }))
	add(ui.Action{ID: "agents.down", Title: "Agent below", Scope: as, Keys: []string{"j", "down"}}, amove(func(n int) int { return n }))
	add(ui.Action{ID: "agents.up", Title: "Agent above", Scope: as, Keys: []string{"k", "up"}}, amove(func(n int) int { return -n }))
	add(ui.Action{ID: "tasks.inspect", Title: "Inspect the task's latest execution", Scope: ts, Keys: []string{"enter"}},
		func(m model) (model, tea.Cmd) {
			t, ok := m.cursorTask()
			if !ok {
				return m, nil
			}
			return m.openInspector(latestExecution(m.live.snap, t.ID))
		})
	add(ui.Action{ID: "agents.inspect", Title: "Inspect the agent's execution", Scope: as, Keys: []string{"i"}},
		func(m model) (model, tea.Cmd) {
			if m.live.snap == nil || len(m.live.snap.Agents) == 0 {
				return m, nil
			}
			agents := sortedAgents(m.withRuntime(m.live.snap.Agents))
			if a := agents[m.agents.index(agents)]; a.Execution != nil {
				return m.openInspector(a.Execution.ID)
			}
			return m, nil
		})
	add(ui.Action{ID: "agents.output", Title: "Show or hide the agent's output", Scope: as, Keys: []string{"o"}, Leader: "a o"},
		func(m model) (model, tea.Cmd) {
			m.output = agentOutput{on: !m.output.on}
			return m, nil
		})
	add(ui.Action{ID: "agents.attach", Title: "Attach to the agent's terminal", Scope: as, Keys: []string{"a"}, Leader: "a a"},
		func(m model) (model, tea.Cmd) { return m.attach() })
	ws := ui.Scope("workspace")
	vscroll := func(d int) handler { return func(m model) (model, tea.Cmd) { return m.viewerScroll(d), nil } }
	add(ui.Action{ID: "viewer.down", Title: "Scroll down", Scope: ws, Keys: []string{"j", "down"}}, vscroll(1))
	add(ui.Action{ID: "viewer.up", Title: "Scroll up", Scope: ws, Keys: []string{"k", "up"}}, vscroll(-1))
	add(ui.Action{ID: "viewer.pagedown", Title: "Scroll half a page down", Scope: ws, Keys: []string{"ctrl+d", "pgdown"}}, vscroll(20))
	add(ui.Action{ID: "viewer.pageup", Title: "Scroll half a page up", Scope: ws, Keys: []string{"ctrl+u", "pgup"}}, vscroll(-20))
	add(ui.Action{ID: "viewer.top", Title: "Top of the file", Scope: ws, Keys: []string{"g", "home"}}, vscroll(-1<<30))
	add(ui.Action{ID: "viewer.bottom", Title: "End of the file", Scope: ws, Keys: []string{"G", "end"}}, vscroll(1<<30))
	add(ui.Action{ID: "viewer.edit", Title: "Open the file in $EDITOR", Scope: ws, Keys: []string{"e"}},
		func(m model) (model, tea.Cmd) { return m.editFile() })
	add(ui.Action{ID: "tabs.next", Title: "Next tab", Scope: ws, Keys: []string{"]"}},
		func(m model) (model, tea.Cmd) { return m.nextTab(1) })
	add(ui.Action{ID: "tabs.prev", Title: "Previous tab", Scope: ws, Keys: []string{"["}},
		func(m model) (model, tea.Cmd) { return m.nextTab(-1) })
	add(ui.Action{ID: "tabs.close", Title: "Close the tab", Scope: ws, Keys: []string{"x"}},
		func(m model) (model, tea.Cmd) { return m.closeTab() })
	add(ui.Action{ID: "tabs.split", Title: "Split: show the previous tab alongside", Scope: ws, Keys: []string{"|"}},
		func(m model) (model, tea.Cmd) { return m.toggleSplit() })
	add(ui.Action{ID: "tabs.swap", Title: "Switch to the other side of the split", Scope: ws, Keys: []string{"\\"}},
		func(m model) (model, tea.Cmd) { return m.swapSplit() })
	add(ui.Action{ID: "viewer.explorer", Title: "Back to the explorer", Scope: ws, Keys: []string{"h", "left"}},
		func(m model) (model, tea.Cmd) { m.tree.focused = true; return m, nil })
	xs := ui.Scope("explorer")
	xmove := func(d int) handler { return func(m model) (model, tea.Cmd) { return m.treeMove(d), nil } }
	add(ui.Action{ID: "explorer.down", Title: "Next entry", Scope: xs, Keys: []string{"j", "down"}}, xmove(1))
	add(ui.Action{ID: "explorer.up", Title: "Previous entry", Scope: xs, Keys: []string{"k", "up"}}, xmove(-1))
	add(ui.Action{ID: "explorer.top", Title: "First entry", Scope: xs, Keys: []string{"g", "home"}}, xmove(-1<<30))
	add(ui.Action{ID: "explorer.bottom", Title: "Last entry", Scope: xs, Keys: []string{"G", "end"}}, xmove(1<<30))
	add(ui.Action{ID: "explorer.open", Title: "Open the file or toggle the directory", Scope: xs, Keys: []string{"enter", "l", "right"}},
		func(m model) (model, tea.Cmd) { return m.treeOpen() })
	add(ui.Action{ID: "explorer.collapse", Title: "Collapse, or go to the parent directory", Scope: xs, Keys: []string{"h", "left"}},
		func(m model) (model, tea.Cmd) { return m.treeLeft(), nil })
	add(ui.Action{ID: "explorer.viewer", Title: "Back to the open file", Scope: xs, Keys: []string{"o"}},
		func(m model) (model, tea.Cmd) { m.tree.focused = m.file == nil; return m, nil })
	find := func(m model) (model, tea.Cmd) { return m.openFinder() }
	add(ui.Action{ID: "workspace.find", Title: "Find a file in the workspace", Scope: g, Leader: "w f"}, find)
	add(ui.Action{ID: "explorer.find", Title: "Find a file", Scope: xs, Keys: []string{"f"}}, find)
	add(ui.Action{ID: "viewer.find", Title: "Find a file", Scope: ws, Keys: []string{"f"}}, find)
	add(ui.Action{ID: "workspace.grep", Title: "Search text in the workspace", Scope: g, Leader: "w g"},
		func(m model) (model, tea.Cmd) { return m.openGrep() })
	add(ui.Action{ID: "workspace.diff", Title: "Diff of the selected task workspace", Scope: g, Leader: "w d"},
		func(m model) (model, tea.Cmd) { return m.openWorkspaceDiff() })
	add(ui.Action{ID: "explorer.diff", Title: "Diff of this task workspace", Scope: xs, Keys: []string{"d"}},
		func(m model) (model, tea.Cmd) { return m.openWorkspaceDiff() })
	add(ui.Action{ID: "viewer.diff", Title: "Diff of this task workspace", Scope: ws, Keys: []string{"d"}},
		func(m model) (model, tea.Cmd) { return m.openWorkspaceDiff() })
	add(ui.Action{ID: "tasks.diff", Title: "Diff of the developer task", Scope: ts, Keys: []string{"d"}},
		func(m model) (model, tea.Cmd) {
			if t, ok := m.cursorTask(); ok {
				return m.openDiff(t)
			}
			return m, nil
		})
	add(ui.Action{ID: "viewer.editbuf", Title: "Edit the file here", Scope: ws, Keys: []string{"i"}},
		func(m model) (model, tea.Cmd) { return m.requireLease("buffer") })
	commit := func(m model) (model, tea.Cmd) { return m.commitManual() }
	discard := func(m model) (model, tea.Cmd) { return m.discardManual() }
	release := func(m model) (model, tea.Cmd) { return m.releaseLease() }
	add(ui.Action{ID: "workspace.commit", Title: "Commit the manual changes", Scope: g, Leader: "w c"}, commit)
	add(ui.Action{ID: "workspace.discard", Title: "Discard the manual changes", Scope: g, Leader: "w x"}, discard)
	add(ui.Action{ID: "workspace.release", Title: "Release the worktree to agents", Scope: g, Leader: "w l"}, release)
	for _, sc := range []ui.Scope{ws, xs} {
		add(ui.Action{ID: "keys." + string(sc) + ".commit", Title: "Commit the manual changes", Scope: sc, Keys: []string{"C"}}, commit)
		add(ui.Action{ID: "keys." + string(sc) + ".discard", Title: "Discard the manual changes", Scope: sc, Keys: []string{"X"}}, discard)
		add(ui.Action{ID: "keys." + string(sc) + ".release", Title: "Release the worktree to agents", Scope: sc, Keys: []string{"L"}}, release)
	}
	es2 := ui.Scope("editor")
	diagsH := func(m model) (model, tea.Cmd) { return m.openDiagnostics() }
	symsH := func(m model) (model, tea.Cmd) { return m.symbols() }
	for _, sc := range []ui.Scope{ws, es2} {
		add(ui.Action{ID: "code." + string(sc) + ".diagnostics", Title: "Diagnostics of the file", Scope: sc, Keys: []string{"!"}}, diagsH)
		add(ui.Action{ID: "code." + string(sc) + ".symbols", Title: "Symbols of the file", Scope: sc, Keys: []string{"S"}}, symsH)
	}
	add(ui.Action{ID: "editor.definition", Title: "Go to the definition", Scope: es2, Keys: []string{"ctrl+]"}},
		func(m model) (model, tea.Cmd) { return m.navigate(false, nil) })
	add(ui.Action{ID: "editor.references", Title: "References of the name", Scope: es2, Keys: []string{"R"}},
		func(m model) (model, tea.Cmd) { return m.navigate(true, nil) })
	bop := func(fn func(b *edit.Buffer)) handler {
		return func(m model) (model, tea.Cmd) { return m.bufOp(fn), nil }
	}
	ins := func(fn func(b *edit.Buffer)) handler {
		return func(m model) (model, tea.Cmd) { return m.enterInsert(fn), nil }
	}
	add(ui.Action{ID: "editor.left", Title: "Left", Scope: es2, Keys: []string{"h", "left"}}, bop(func(b *edit.Buffer) { b.Move(0, -1, false) }))
	add(ui.Action{ID: "editor.right", Title: "Right", Scope: es2, Keys: []string{"l", "right"}}, bop(func(b *edit.Buffer) { b.Move(0, 1, false) }))
	add(ui.Action{ID: "editor.down", Title: "Down", Scope: es2, Keys: []string{"j", "down"}}, bop(func(b *edit.Buffer) { b.Move(1, 0, false) }))
	add(ui.Action{ID: "editor.up", Title: "Up", Scope: es2, Keys: []string{"k", "up"}}, bop(func(b *edit.Buffer) { b.Move(-1, 0, false) }))
	add(ui.Action{ID: "editor.pagedown", Title: "Half a page down", Scope: es2, Keys: []string{"ctrl+d", "pgdown"}}, bop(func(b *edit.Buffer) { b.Move(20, 0, false) }))
	add(ui.Action{ID: "editor.pageup", Title: "Half a page up", Scope: es2, Keys: []string{"ctrl+u", "pgup"}}, bop(func(b *edit.Buffer) { b.Move(-20, 0, false) }))
	add(ui.Action{ID: "editor.home", Title: "Start of the line", Scope: es2, Keys: []string{"0", "home"}}, bop(func(b *edit.Buffer) { b.LineStart() }))
	add(ui.Action{ID: "editor.end", Title: "End of the line", Scope: es2, Keys: []string{"$", "end"}}, bop(func(b *edit.Buffer) { b.LineEnd(false) }))
	add(ui.Action{ID: "editor.top", Title: "First line", Scope: es2, Keys: []string{"g"}}, bop(func(b *edit.Buffer) { b.MoveTo(0, 0, false) }))
	add(ui.Action{ID: "editor.bottom", Title: "Last line", Scope: es2, Keys: []string{"G"}}, bop(func(b *edit.Buffer) { b.MoveTo(1<<30, 0, false) }))
	add(ui.Action{ID: "editor.insert", Title: "Insert before the cursor", Scope: es2, Keys: []string{"i"}}, ins(nil))
	add(ui.Action{ID: "editor.append", Title: "Insert after the cursor", Scope: es2, Keys: []string{"a"}}, ins(func(b *edit.Buffer) { b.Move(0, 1, true) }))
	add(ui.Action{ID: "editor.appendline", Title: "Insert at the end of the line", Scope: es2, Keys: []string{"A"}}, ins(func(b *edit.Buffer) { b.LineEnd(true) }))
	add(ui.Action{ID: "editor.openbelow", Title: "New line below", Scope: es2, Keys: []string{"o"}}, ins(func(b *edit.Buffer) { b.OpenLine(true) }))
	add(ui.Action{ID: "editor.openabove", Title: "New line above", Scope: es2, Keys: []string{"O"}}, ins(func(b *edit.Buffer) { b.OpenLine(false) }))
	add(ui.Action{ID: "editor.delchar", Title: "Delete the character", Scope: es2, Keys: []string{"x", "delete"}}, bop(func(b *edit.Buffer) { b.DeleteChar() }))
	add(ui.Action{ID: "editor.delline", Title: "Delete the line", Scope: es2, Keys: []string{"D"}}, bop(func(b *edit.Buffer) { b.DeleteLine() }))
	add(ui.Action{ID: "editor.undo", Title: "Undo", Scope: es2, Keys: []string{"u"}}, bop(func(b *edit.Buffer) { b.Undo() }))
	add(ui.Action{ID: "editor.redo", Title: "Redo", Scope: es2, Keys: []string{"ctrl+r"}}, bop(func(b *edit.Buffer) { b.Redo() }))
	add(ui.Action{ID: "editor.find", Title: "Find in the file", Scope: es2, Keys: []string{"/"}},
		func(m model) (model, tea.Cmd) { s := ""; m.ed.search = &s; return m, nil })
	add(ui.Action{ID: "editor.next", Title: "Next match", Scope: es2, Keys: []string{"n"}},
		func(m model) (model, tea.Cmd) { return m.findNext(true) })
	add(ui.Action{ID: "editor.prev", Title: "Previous match", Scope: es2, Keys: []string{"N"}},
		func(m model) (model, tea.Cmd) { return m.findNext(false) })
	add(ui.Action{ID: "editor.save", Title: "Save", Scope: es2, Keys: []string{"ctrl+s"}},
		func(m model) (model, tea.Cmd) { return m.save(false) })
	add(ui.Action{ID: "editor.commit", Title: "Commit the manual changes", Scope: es2, Keys: []string{"C"}}, commit)
	is2 := ui.Scope("integration")
	add(ui.Action{ID: "integration.down", Title: "Next branch", Scope: is2, Keys: []string{"j", "down"}},
		func(m model) (model, tea.Cmd) { return m.integMove(1), nil })
	add(ui.Action{ID: "integration.up", Title: "Previous branch", Scope: is2, Keys: []string{"k", "up"}},
		func(m model) (model, tea.Cmd) { return m.integMove(-1), nil })
	add(ui.Action{ID: "integration.approve", Title: "Approve the branch", Scope: is2, Keys: []string{"a"}},
		func(m model) (model, tea.Cmd) { return m.integDecide(service.DecisionApprove) })
	add(ui.Action{ID: "integration.reject", Title: "Reject the branch", Scope: is2, Keys: []string{"r"}},
		func(m model) (model, tea.Cmd) { return m.integDecide(service.DecisionReject) })
	add(ui.Action{ID: "integration.merge", Title: "Merge the approved branch (preview first)", Scope: is2, Keys: []string{"m"}},
		func(m model) (model, tea.Cmd) { return m.integMerge() })
	add(ui.Action{ID: "integration.diff", Title: "Diff of the branch", Scope: is2, Keys: []string{"d"}},
		func(m model) (model, tea.Cmd) { return m.integDiff() })
	xt := ui.Scope("terminals")
	xo := ui.Scope("termout")
	tmv := func(d int) handler { return func(m model) (model, tea.Cmd) { return m.termMove(d), nil } }
	tsc := func(d int) handler { return func(m model) (model, tea.Cmd) { return m.termScroll(d), nil } }
	termNew := func(m model) (model, tea.Cmd) { return m.termNew() }
	termVal := func(m model) (model, tea.Cmd) { return m.termValidate() }
	shell := func(m model) (model, tea.Cmd) { return m.openShell() }
	add(ui.Action{ID: "terminals.down", Title: "Next session", Scope: xt, Keys: []string{"j", "down"}}, tmv(1))
	add(ui.Action{ID: "terminals.up", Title: "Previous session", Scope: xt, Keys: []string{"k", "up"}}, tmv(-1))
	add(ui.Action{ID: "terminals.new", Title: "Run a command", Scope: xt, Keys: []string{"n"}}, termNew)
	add(ui.Action{ID: "terminals.validate", Title: "Run the validation commands", Scope: xt, Keys: []string{"v"}}, termVal)
	add(ui.Action{ID: "terminals.shell", Title: "Open a shell in the workspace", Scope: xt, Keys: []string{"s"}}, shell)
	add(ui.Action{ID: "terminals.stop", Title: "Stop the session", Scope: xt, Keys: []string{"c"}},
		func(m model) (model, tea.Cmd) { return m.termStop() })
	add(ui.Action{ID: "terminals.close", Title: "Close the session", Scope: xt, Keys: []string{"x"}},
		func(m model) (model, tea.Cmd) { return m.termClose() })
	add(ui.Action{ID: "terminals.focus", Title: "Move through the output (links)", Scope: xt, Keys: []string{"enter", "l", "right"}},
		func(m model) (model, tea.Cmd) { return m.termFocus(), nil })
	add(ui.Action{ID: "terminals.pageup", Title: "Scroll the output up", Scope: xt, Keys: []string{"ctrl+u", "pgup"}}, tsc(-20))
	add(ui.Action{ID: "terminals.pagedown", Title: "Scroll the output down", Scope: xt, Keys: []string{"ctrl+d", "pgdown"}}, tsc(20))
	add(ui.Action{ID: "terminals.end", Title: "Follow the output", Scope: xt, Keys: []string{"G", "end"}},
		func(m model) (model, tea.Cmd) { m.terms.scroll = 0; return m, nil })
	add(ui.Action{ID: "termout.down", Title: "Next line", Scope: xo, Keys: []string{"j", "down"}}, tmv(1))
	add(ui.Action{ID: "termout.up", Title: "Previous line", Scope: xo, Keys: []string{"k", "up"}}, tmv(-1))
	add(ui.Action{ID: "termout.pagedown", Title: "Half a page down", Scope: xo, Keys: []string{"ctrl+d", "pgdown"}}, tmv(20))
	add(ui.Action{ID: "termout.pageup", Title: "Half a page up", Scope: xo, Keys: []string{"ctrl+u", "pgup"}}, tmv(-20))
	add(ui.Action{ID: "termout.top", Title: "First line", Scope: xo, Keys: []string{"g", "home"}}, tmv(-1<<30))
	add(ui.Action{ID: "termout.bottom", Title: "Last line", Scope: xo, Keys: []string{"G", "end"}}, tmv(1<<30))
	add(ui.Action{ID: "termout.open", Title: "Open the file:line of this line", Scope: xo, Keys: []string{"enter", "o"}},
		func(m model) (model, tea.Cmd) { return m.termOpenLink() })
	add(ui.Action{ID: "termout.back", Title: "Back to the sessions", Scope: xo, Keys: []string{"h", "left"}},
		func(m model) (model, tea.Cmd) { m.terms.focus = false; return m, nil })
	ds := ui.Scope("diff")
	dscroll := func(d int) handler { return func(m model) (model, tea.Cmd) { return m.diffScroll(d), nil } }
	dfile := func(d int) handler { return func(m model) (model, tea.Cmd) { return m.diffFile(d), nil } }
	add(ui.Action{ID: "diff.down", Title: "Scroll down", Scope: ds, Keys: []string{"j", "down"}}, dscroll(1))
	add(ui.Action{ID: "diff.up", Title: "Scroll up", Scope: ds, Keys: []string{"k", "up"}}, dscroll(-1))
	add(ui.Action{ID: "diff.pagedown", Title: "Scroll half a page down", Scope: ds, Keys: []string{"ctrl+d", "pgdown"}}, dscroll(20))
	add(ui.Action{ID: "diff.pageup", Title: "Scroll half a page up", Scope: ds, Keys: []string{"ctrl+u", "pgup"}}, dscroll(-20))
	add(ui.Action{ID: "diff.top", Title: "Top of the diff", Scope: ds, Keys: []string{"g", "home"}}, dscroll(-1<<30))
	add(ui.Action{ID: "diff.bottom", Title: "End of the diff", Scope: ds, Keys: []string{"G", "end"}}, dscroll(1<<30))
	add(ui.Action{ID: "diff.next", Title: "Next file", Scope: ds, Keys: []string{"n", "l", "right"}}, dfile(1))
	add(ui.Action{ID: "diff.prev", Title: "Previous file", Scope: ds, Keys: []string{"p", "h", "left"}}, dfile(-1))
	add(ui.Action{ID: "diff.side", Title: "Toggle side by side", Scope: ds, Keys: []string{"s"}},
		func(m model) (model, tea.Cmd) { return m.diffToggleSide() })
	add(ui.Action{ID: "diff.open", Title: "Open the file in its worktree", Scope: ds, Keys: []string{"enter", "o"}},
		func(m model) (model, tea.Cmd) { return m.diffOpenFile() })
	is := ui.Scope("inspector")
	tab := func(d int) handler { return func(m model) (model, tea.Cmd) { return m.inspectorTab(d), nil } }
	scroll := func(d int) handler { return func(m model) (model, tea.Cmd) { return m.inspectorScroll(d), nil } }
	add(ui.Action{ID: "inspector.next", Title: "Next tab", Scope: is, Keys: []string{"l", "right"}}, tab(1))
	add(ui.Action{ID: "inspector.prev", Title: "Previous tab", Scope: is, Keys: []string{"h", "left"}}, tab(-1))
	add(ui.Action{ID: "inspector.down", Title: "Scroll down", Scope: is, Keys: []string{"j", "down"}}, scroll(1))
	add(ui.Action{ID: "inspector.up", Title: "Scroll up", Scope: is, Keys: []string{"k", "up"}}, scroll(-1))
	add(ui.Action{ID: "inspector.pagedown", Title: "Scroll a page down", Scope: is, Keys: []string{"ctrl+d", "pgdown"}}, scroll(10))
	add(ui.Action{ID: "inspector.pageup", Title: "Scroll a page up", Scope: is, Keys: []string{"ctrl+u", "pgup"}}, scroll(-10))
	ctl := func(fn func(model) (model, tea.Cmd)) handler { return func(m model) (model, tea.Cmd) { return fn(m) } }
	newRun, cancel, resume, clean := ctl(model.openNewRun), ctl(model.cancelRun), ctl(model.resumeRun), ctl(model.cleanWorktrees)
	add(ui.Action{ID: "run.new", Title: "Start a new run", Scope: g, Leader: "r n"}, newRun)
	add(ui.Action{ID: "run.cancel", Title: "Cancel the run", Scope: g, Leader: "r c"}, cancel)
	add(ui.Action{ID: "run.resume", Title: "Resume the interrupted run", Scope: g, Leader: "r R"}, resume)
	add(ui.Action{ID: "run.clean", Title: "Clean worktrees of finished runs", Scope: g, Leader: "r x"}, clean)
	runsScope, ov := ui.Scope("runs"), ui.Scope("overview")
	add(ui.Action{ID: "runs.new", Title: "Start a new run", Scope: runsScope, Keys: []string{"n"}}, newRun)
	add(ui.Action{ID: "runs.cancel", Title: "Cancel the run under the cursor", Scope: runsScope, Keys: []string{"c"}}, cancel)
	add(ui.Action{ID: "runs.resume", Title: "Resume the run under the cursor", Scope: runsScope, Keys: []string{"R"}}, resume)
	add(ui.Action{ID: "runs.clean", Title: "Clean worktrees of finished runs", Scope: runsScope, Keys: []string{"x"}}, clean)
	add(ui.Action{ID: "overview.new", Title: "Start a new run", Scope: ov, Keys: []string{"n"}}, newRun)
	add(ui.Action{ID: "overview.cancel", Title: "Cancel the run", Scope: ov, Keys: []string{"c"}}, cancel)
	add(ui.Action{ID: "overview.resume", Title: "Resume the interrupted run", Scope: ov, Keys: []string{"R"}}, resume)
	add(ui.Action{ID: "run.report", Title: "Open the run's report", Scope: ui.Scope("overview"), Keys: []string{"r"}, Leader: "r o"},
		func(m model) (model, tea.Cmd) { return m.openReport() })
	rs2 := ui.Scope("report")
	rscroll := func(d int) handler { return func(m model) (model, tea.Cmd) { return m.reportScroll(d), nil } }
	add(ui.Action{ID: "report.down", Title: "Scroll down", Scope: rs2, Keys: []string{"j", "down"}}, rscroll(1))
	add(ui.Action{ID: "report.up", Title: "Scroll up", Scope: rs2, Keys: []string{"k", "up"}}, rscroll(-1))
	add(ui.Action{ID: "report.pagedown", Title: "Scroll a page down", Scope: rs2, Keys: []string{"ctrl+d", "pgdown"}}, rscroll(10))
	add(ui.Action{ID: "report.pageup", Title: "Scroll a page up", Scope: rs2, Keys: []string{"ctrl+u", "pgup"}}, rscroll(-10))
	add(ui.Action{ID: "report.top", Title: "Top of the report", Scope: rs2, Keys: []string{"g", "home"}}, rscroll(-1<<30))
	add(ui.Action{ID: "report.bottom", Title: "End of the report", Scope: rs2, Keys: []string{"G", "end"}}, rscroll(1<<30))
	focus := func(m model) (model, tea.Cmd) {
		m.events.focused = !m.events.focused && m.layout().Panel.Visible()
		return m, nil
	}
	add(ui.Action{ID: "pane.next", Title: "Next pane", Scope: g, Keys: []string{"tab"}}, focus)
	add(ui.Action{ID: "pane.prev", Title: "Previous pane", Scope: g, Keys: []string{"shift+tab"}}, focus)
	es := ui.Scope("events")
	rows := func(m model) int { return max(m.layout().Panel.H-1, 1) }
	escroll := func(d int) handler {
		return func(m model) (model, tea.Cmd) { return m.eventsScroll(d, rows(m)) }
	}
	add(ui.Action{ID: "events.older", Title: "Older events", Scope: es, Keys: []string{"k", "up"}}, escroll(1))
	add(ui.Action{ID: "events.newer", Title: "Newer events", Scope: es, Keys: []string{"j", "down"}}, escroll(-1))
	add(ui.Action{ID: "events.pageup", Title: "A page of older events", Scope: es, Keys: []string{"ctrl+u", "pgup"}},
		func(m model) (model, tea.Cmd) { return m.eventsScroll(rows(m), rows(m)) })
	add(ui.Action{ID: "events.pagedown", Title: "A page of newer events", Scope: es, Keys: []string{"ctrl+d", "pgdown"}},
		func(m model) (model, tea.Cmd) { return m.eventsScroll(-rows(m), rows(m)) })
	add(ui.Action{ID: "events.newest", Title: "Follow new events", Scope: es, Keys: []string{"G", "end"}},
		func(m model) (model, tea.Cmd) { m.events.scroll = 0; return m, nil })
	add(ui.Action{ID: "events.type", Title: "Filter events by type", Scope: es, Keys: []string{"/"}},
		func(m model) (model, tea.Cmd) { m.events.typing = true; return m, nil })
	add(ui.Action{ID: "events.task", Title: "Filter events by task (cycle)", Scope: es, Keys: []string{"t"}},
		func(m model) (model, tea.Cmd) { return m.cycleTask(), nil })
	add(ui.Action{ID: "events.clear", Title: "Clear the event filters", Scope: es, Keys: []string{"x"}},
		func(m model) (model, tea.Cmd) {
			m.events.typeFilter, m.events.taskFilter, m.events.scroll = "", "", 0
			return m, nil
		})
	return r, h
}

func (m model) toggleOverlay(o overlay) model {
	if m.overlay == o {
		m.overlay = overlayNone
	} else {
		m.overlay = o
	}
	return m
}

func (m model) scope() ui.Scope {
	if m.events.focused && m.layout().Panel.Visible() {
		return "events"
	}
	if m.diff != nil {
		return "diff"
	}
	if m.report != nil {
		return "report"
	}
	if m.inspect != nil {
		return "inspector"
	}
	if m.editing() {
		return "editor"
	}
	if m.screen == screenWorkspace && (m.tree.focused || m.file == nil) {
		return "explorer"
	}
	if m.screen == screenTerminals && m.terms.focus {
		return "termout"
	}
	return ui.Scope(strings.ToLower(screenNames[m.screen]))
}

func (m model) run(id string) (tea.Model, tea.Cmd) {
	if fn, ok := m.handlers[id]; ok {
		return fn(m)
	}
	return m, nil
}

func (m model) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	switch m.overlay {
	case overlayPalette:
		return m.paletteKey(msg)
	case overlayLeader:
		return m.leaderKey(k)
	case overlayConfirm:
		return m.confirmKey(k)
	case overlayNewRun:
		return m.newRunKey(msg)
	case overlayWorkspaces:
		return m.workspacesKey(k)
	case overlayFinder:
		return m.finderKey(msg)
	case overlayGrep:
		return m.grepKey(msg)
	}
	if m.events.typing {
		return m.eventsFilterKey(msg)
	}
	if m.code.list != nil {
		return m.listKey(k)
	}
	if m.ed.commit != nil {
		return m.commitKey(msg)
	}
	if m.screen == screenIntegration && m.integ.note != nil {
		return m.integNoteKey(msg)
	}
	if m.editing() && m.ed.search != nil {
		return m.searchKey(msg)
	}
	if m.editing() && m.ed.insert {
		return m.insertKey(msg)
	}
	if m.screen == screenTerminals && m.terms.typing {
		return m.termInputKey(msg)
	}
	if m.screen == screenRuns && m.runs.filtering {
		return m.runsFilterKey(msg)
	}
	if a, ok := m.registry.Match(m.scope(), k); ok {
		return m.run(a.ID)
	}
	return m, nil
}

func (m model) paletteKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	matches := m.registry.Filter(m.paletteQuery)
	switch msg.String() {
	case "esc":
		m.overlay = overlayNone
	case "ctrl+q", "ctrl+c":
		return m.run("app.quit")
	case "enter":
		if m.paletteSel < len(matches) {
			m.overlay = overlayNone
			return m.run(matches[m.paletteSel].ID)
		}
	case "up", "ctrl+k":
		m.paletteSel = max(0, m.paletteSel-1)
	case "down", "ctrl+j":
		m.paletteSel = min(len(matches)-1, m.paletteSel+1)
	case "backspace":
		if r := []rune(m.paletteQuery); len(r) > 0 {
			m.paletteQuery, m.paletteSel = string(r[:len(r)-1]), 0
		}
	default:
		if msg.Text != "" {
			m.paletteQuery, m.paletteSel = m.paletteQuery+msg.Text, 0
		}
	}
	return m, nil
}

func (m model) leaderKey(k string) (tea.Model, tea.Cmd) {
	if k == "esc" {
		m.overlay = overlayNone
		return m, nil
	}
	if m.leaderGroup == "" {
		if title, _ := m.registry.LeaderGroup(k); title != "" {
			m.leaderGroup = k
		} else {
			m.overlay = overlayNone
		}
		return m, nil
	}
	if a, ok := m.registry.MatchLeader(m.leaderGroup + " " + k); ok {
		m.overlay = overlayNone
		return m.run(a.ID)
	}
	m.overlay = overlayNone
	return m, nil
}

func (m model) overlayView() (title, body string) {
	var b strings.Builder
	switch m.overlay {
	case overlayHelp:
		fmt.Fprintf(&b, " Keys · %s\n\n", screenNames[m.screen])
		for _, a := range m.registry.Help(m.scope()) {
			fmt.Fprintf(&b, "  %-20s %s\n", ui.KeyLabel(a), a.Title)
		}
		b.WriteString("\n  esc or ? closes")
		return "HELP", b.String()
	case overlayPalette:
		fmt.Fprintf(&b, " : %s▏\n\n", m.paletteQuery)
		matches := m.registry.Filter(m.paletteQuery)
		if len(matches) == 0 {
			b.WriteString("  no matching command")
		}
		for i, a := range matches {
			marker := " "
			if i == m.paletteSel {
				marker = "▸"
			}
			fmt.Fprintf(&b, " %s %-34s %s\n", marker, a.Title, ui.KeyLabel(a))
		}
		return "PALETTE", b.String()
	case overlayLeader:
		if m.leaderGroup == "" {
			b.WriteString(" space …\n\n")
			for _, g := range m.registry.LeaderGroups() {
				fmt.Fprintf(&b, "  %s  %s\n", g[0], g[1])
			}
		} else {
			title, acts := m.registry.LeaderGroup(m.leaderGroup)
			fmt.Fprintf(&b, " space %s · %s\n\n", m.leaderGroup, title)
			for _, a := range acts {
				_, key, _ := strings.Cut(a.Leader, " ")
				fmt.Fprintf(&b, "  %s  %s\n", key, a.Title)
			}
		}
		b.WriteString("\n  esc closes")
		return "LEADER", b.String()
	}
	return "", ""
}
