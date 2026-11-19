# Caffè Studio — Product Specification

**Caffè Studio** — *Brew better software.*

Status: **Implemented**, except embedded shells (M3b, blocked on a Windows
run of the ADR-004 criteria) and the polish items PO-01 to PO-06 (see
[backlog.md](backlog.md)). Where the implementation differs from the
original design, this document says so.
How to use it: [user-guide.md](user-guide.md).

---

## 1. Vision

Caffè Studio is a local-first terminal environment in which a developer
supervises autonomous coding agents working on their codebase: they start
work, watch agents progress in parallel, understand why something is waiting,
open the exact worktree an agent used, read and change the code, run commands,
and decide what gets integrated.

The developer is an **active supervisor**, not an audience. Every agent
decision is inspectable, every change is attributable to a worktree, and
nothing reaches the main branch without the developer.

Studio is a new front end to the existing Caffè engine. It does not replace the
`caffe` CLI, and it does not contain orchestration logic.

## 2. Target user

A developer who already uses coding-agent CLIs (Claude Code, Codex,
Antigravity, …) in a terminal, works in Git, and wants to delegate several
pieces of work at once without losing track of them. They are comfortable with
keyboard-driven tools and have their own editor they will not give up.

## 3. Core workflows

### W1 — Delegate and supervise
1. Open Studio in a repository (`caffe studio`).
2. Start a run from a request (or a plan file).
3. Watch the Overview: phase, task board, which agents are working.
4. Notice a blocked agent (trust or permission dialog), attach to it, answer,
   return to Studio.
5. Read each task's report as it lands.

### W2 — Review an agent's change
1. Select a completed developer task.
2. Open its diff against its base.
3. Open a changed file in its worktree, with the worktree clearly labelled.
4. Read the reviewer's findings side by side.

### W3 — Intervene manually
1. On a finished developer task (or a failed one), open the worktree.
2. Edit a file (built-in or in `$EDITOR`).
3. Run the project's tests in that worktree and read the output.
4. Commit the manual change, attributed to the developer.

### W4 — Investigate a failure
1. A run ends `failed`. Overview shows which task and why.
2. Open the execution: prompt, report, exit codes, terminal output, error.
3. Jump from a failing test line to the source location.
4. Fix, re-run validation, or resume/re-run.

### W5 — Recover
1. Studio (or the machine) restarts while a run is active.
2. On reopening, Studio shows the run as still running (its own process
   survived) or as interrupted, with the `resume` and `cancel` actions.

### W6 — Decide integration (later milestone)
1. For a completed run, see each branch, its diff and its review verdict.
2. Approve or reject each one. Approval records the decision and offers the
   merge command; nothing is merged without explicit confirmation.

## 4. Feature boundaries

| In scope | Out of scope |
|---|---|
| Run control: start, cancel, resume, clean | Scheduling logic in the UI |
| Task board, dependency view, "why waiting" | Editing a running plan |
| Agent Room: state, task, runtime, worktree, live output, attach | Running agents inside Studio (the runtime does) |
| Execution inspector: prompt, report, artifacts, exit codes | Replaying agent sessions |
| File explorer, code viewer with syntax highlighting, diffs | A full IDE (refactorings, project models, plugins) |
| Practical editing: open, edit, save, undo, search | Multi-cursor, macros, extension system |
| External editor hand-off (`$EDITOR`) | Replacing Neovim / VS Code / JetBrains |
| Command execution with streamed output; interactive shells | A general terminal multiplexer |
| Diagnostics and navigation through LSP (Go first) | Language-specific features beyond LSP |
| Debugging through DAP (Delve first) | A custom debugger |
| Integration decisions with explicit confirmation | Automatic merging, conflict resolution by agents |

## 5. Interaction principles

1. **State before decoration.** Every state is shown in text and symbol;
   colour and avatars only reinforce it.
2. **The current worktree is always visible.** Any view showing or editing a
   file shows which worktree (main checkout or `caffe/<run>/<task>`) it
   belongs to.
3. **Claims vs evidence.** Agent-reported results are labelled as claims;
   orchestrator-observed results (exit codes, diffs) are labelled as observed.
4. **Destructive actions are confirmed** and say exactly what they do (cancel
   run *X*, remove worktree *Y*, discard *N* unsaved changes).
5. **Keyboard first, discoverable.** Every action is in the command palette
   with its key; `?` shows the keys of the focused view.
6. **Never lose work.** Unsaved buffers block quitting; manual edits are never
   committed as an agent's work.
7. **Honest about staleness.** If data could not be refreshed, the view says
   since when.

## 6. Screens

Detailed wireframes are in [ux-specification.md](ux-specification.md).

| Screen | Purpose | Milestone |
|---|---|---|
| Overview | Active run: phase, progress, task and agent summary, recent events, health | M1 |
| Task Board | Tasks by status column, dependency view, task details | M1 |
| Agent Room | One card per agent: role, kind, state, task, elapsed, last activity, worktree, live output, actions | M1 (live output M1.6) |
| Runs | Run history; open, resume, cancel | M1 |
| Execution Inspector | Prompt, report, artifacts, exit codes, error, runtime session | M1 |
| Workspace | Explorer, viewer/editor, tabs, splits, search, Git status, diffs | M2 |
| Terminal Manager | Command runs with streamed output; interactive shells | M3 |
| Diagnostics / Navigation | LSP diagnostics, definition, references, symbols | M4 |
| Debugger | Breakpoints, stack, variables, controls, console | M5 |
| Integration | Per-branch approve/reject with explicit merge confirmation | M6 |

## 7. Functional requirements

IDs are referenced from the backlog.

### Orchestration (FR-O)
- **FR-O1** List runs (active first) with status, request, age, duration.
- **FR-O2** Start a run from a request or a plan file; the run continues if Studio exits.
- **FR-O3** Cancel a run (confirmed); resume an interrupted run.
- **FR-O4** Show a run's tasks with status, role, attempts, dependencies, and the reason a task is not moving (waiting on X, queued behind the concurrency limit, blocked on a human, cancelled because Y failed).
- **FR-O5** Show agents of a run with role, kind, runtime state, task, elapsed time, last activity, worktree.
- **FR-O6** Show the event log of a run, filterable by task and type, updating live.
- **FR-O7** Show an execution: prompt, report (structured), terminal output, artifacts, exit codes, error.
- **FR-O8** Show warnings (read-only rule violations, backup failures) prominently.
- **FR-O9** Attach to a live agent through the runtime when it supports it, and return to Studio afterwards.
- **FR-O10** Show the consolidated report of a run.

### Workspace (FR-W)
- **FR-W1** Select a workspace: the main checkout or any task worktree that exists.
- **FR-W2** File explorer for the selected workspace, honouring `.gitignore`.
- **FR-W3** View files with syntax highlighting and line numbers.
- **FR-W4** Show a task's diff (from its stored diff artifact or live `git diff`).
- **FR-W5** Show Git status of the selected workspace.
- **FR-W6** Search file names and contents in the selected workspace.
- **FR-W7** Edit and save files; multiple buffers; undo/redo; dirty detection.
- **FR-W8** Open the current file at the current line in `$EDITOR`.
- **FR-W9** Refuse writes to a worktree that an agent currently owns; explain why.
- **FR-W10** Commit manual changes in a worktree, attributed to the developer.

### Execution (FR-X)
- **FR-X1** Run a command in a selected workspace, streaming output, with exit code and duration.
- **FR-X2** Run the configured validation commands in a selected workspace.
- **FR-X3** Stop a running command (its whole process group).
- **FR-X4** Interactive shell in a selected workspace.
- **FR-X5** Several concurrent sessions, each labelled with its workspace.
- **FR-X6** Jump from `file:line` in output to the source.

### Code intelligence (FR-C)
- **FR-C1** Diagnostics for open files.
- **FR-C2** Go to definition, find references, document symbols.
- **FR-C3** Language servers configured per language; Go (`gopls`) by default.

### Debugging (FR-D)
- **FR-D1** Start a debug session for a test or program (Delve first).
- **FR-D2** Breakpoints, continue/step, call stack, variables.

### Integration (FR-I)
- **FR-I1** Per completed developer task: branch, diff summary, review verdict, validation result.
- **FR-I2** Record approve/reject; merging only after explicit confirmation of the exact command.

## 8. Non-functional requirements

### Performance
- **NFR-P1** Key handling and redraw never wait on I/O: all I/O runs off the UI loop.
- **NFR-P2** Visible state lags persisted state by at most ~1 s under normal load.
- **NFR-P3** Output buffers are bounded (default 10 000 lines per session); older output stays on disk where it exists.
- **NFR-P4** Files above a size limit (default 2 MB) open read-only in a plain viewer; highlighting is applied to the visible region only.
- **NFR-P5** Explorer and search stay responsive in repositories of 100 k files (lazy directory loading; search delegated to `rg`).
- **NFR-P6** Studio's own polling (database change check, focused agent output) is bounded and paused for hidden views.

### Reliability
- **NFR-R1** A runtime or database error degrades a view, never crashes Studio.
- **NFR-R2** Runs started from Studio survive Studio exiting or crashing.
- **NFR-R3** Studio restores the terminal on every exit path, including panics and signals.
- **NFR-R4** No orphaned processes: sessions started by Studio are terminated as process groups on close and on exit.
- **NFR-R5** Unsaved edits are never lost silently (quit is blocked; crash recovery files are a later increment).
- **NFR-R6** UI state is reconstructible from persisted state alone.

### Portability
- **NFR-T1** **Linux and Windows** are the supported targets for every milestone. A milestone is done only when its acceptance criteria hold on both.
- **NFR-T2** macOS is not a target: it may work (the Unix code paths cover it) but is neither tested nor supported.
- **NFR-T3** Platform-specific behaviour (process trees, detaching, terminals) has an explicit implementation per platform, not a Unix implementation with a Windows no-op.

### Accessibility and usability
- **NFR-A1** A high-contrast theme and a monochrome mode (`NO_COLOR`).
- **NFR-A2** Every state has a text label and a distinct symbol; colour is never the only signal.
- **NFR-A3** The focused pane is indicated by border style and title, not only colour.
- **NFR-A4** Fully usable by keyboard; mouse optional.
- **NFR-A5** Layout adapts down to 80×24; below that, a single-pane mode.

### Security
- **NFR-S1** No shell command is ever built from agent output or report content.
- **NFR-S2** Paths from reports and output are resolved and must stay inside the selected workspace before they are opened.
- **NFR-S3** Agent and command output pass through `runtime.Redact` before display and before being written to Studio's logs.
- **NFR-S4** Destructive actions (cancel, clean, discard, delete, merge) need explicit confirmation naming the target.
- **NFR-S5** Studio never answers agent dialogs on the developer's behalf.

## 9. Non-goals

- A replacement for Neovim, LazyVim, VS Code or JetBrains.
- A general-purpose terminal multiplexer.
- A web UI, a daemon reachable over a network, or a cloud backend.
- Accounts, telemetry, or any mandatory remote service.
- Running or embedding an LLM, or replacing the external agent runtime.
- Automatic merging or agent-driven conflict resolution.
- Editing a plan while a run executes it.

## 10. Open product questions

| # | Question | Default until decided |
|---|---|---|
| Q1 | Is built-in editing required in M2, or is `$EDITOR` hand-off enough for the first release? | `$EDITOR` hand-off in M2a; built-in editing in M2b (ADR-003) |
| Q2 | May the developer edit an agent's worktree while its run is still active (task finished, run not)? | Yes, only when no task holds the worktree lease (ADR-008) |
| Q3 | Should integration (W6) merge, or only record a decision and print the command? | Record + print; executing the merge is opt-in and confirmed |
| Q4 | Is Windows a first-class target? | **Decided 2026-11-18: yes.** Linux + Windows; macOS out of scope (NFR-T1/T2) |
