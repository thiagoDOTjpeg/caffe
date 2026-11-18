# Caffè Studio — Repository Audit

Audit of the Caffè codebase as of 2026-11-18, done to ground the Studio design.
Every finding below was checked in the code, not taken from the existing
documentation.

**Legend:** **Implemented** · **Partial** · **Missing** · **Proposed** · **Investigate**

---

## 1. Existing capabilities

| Capability | Status | Where |
|---|---|---|
| Runs, tasks, dependencies, agents, executions, artifacts and events persisted in SQLite (WAL, foreign keys, embedded migrations) | Implemented | `internal/persistence/database.go`, `repositories.go`, `migrations/001_init.sql` |
| Every task and run status change writes its row and its event **in one transaction**, with a compare-and-swap on the expected status | Implemented | `persistence.UpdateTask`, `UpdateRun` (`repositories.go`) |
| Explicit task state machine; invalid transitions are rejected | Implemented | `core/state_machine.go` (`taskTransitions`) |
| DAG validation, bounded concurrent scheduler, retries, permanent failures, cascading cancellation, per-attempt timeouts | Implemented | `core/task_graph.go`, `core/scheduler.go` |
| Role policies: researcher, developer (own worktree, real diff required), reviewer (correction loop), validator (orchestrator-run commands with real exit codes) | Implemented | `core/execution_manager.go` |
| One agent execution end to end; completion = report file, never "idle" | Implemented | `agents/manager.go` (`Execute`, `await`) |
| Runtime adapter interface with one implementation (Herdr CLI) and an in-memory fake | Implemented | `runtime/runtime.go`, `runtime/herdr/`, `runtime/fake.go` |
| Per-task Git worktree and branch; diff and changed-file list; safe removal (never forced, branch kept) | Implemented | `git/manager.go` |
| Heartbeat, stale-run detection, safe resume with re-attach to surviving agents | Implemented | `core/orchestrator.go` (`heartbeat`), `core/recovery_manager.go` |
| Cross-process cancellation (another process flips the run to `cancelled`; the owner notices within one heartbeat) | Implemented | `Orchestrator.Cancel`, `heartbeat` |
| Several runs at once, each owned by its own `caffe` process, sharing one database | Implemented | one process per `Run`/`Resume`; WAL + `busy_timeout` |
| Consolidated Markdown report built purely from persisted state | Implemented | `core/report.go` (`BuildReport`) |
| Live validation-command output (written to a log file while the command runs) | Implemented | `runCommand` writes to `command-N.log` |
| Per-agent terminal output | Partial | read **once, when the execution ends** (`agents.Manager.captureOutput`); live output exists only through `Runtime.ReadAgentOutput`, which nothing outside the agent manager calls |
| Read model for consumers (CLI, a future TUI) | Partial | the CLI reads repositories directly; see weakness W1 |
| Event consumption by an external process | Partial | events are persisted but can only be read **whole** per run (`ListEvents`); no cursor, no "since", no change notification |
| Human integration / merge workflow | Missing | Caffè never merges; no approve/reject concept exists |
| Live progress outside the owning process | Missing | `Orchestrator.Progress` is an in-process callback that receives formatted text |

## 2. Missing capabilities Studio needs

| Need | Gap | Evidence |
|---|---|---|
| Consistent snapshot of a run | Each `List*` call is its own query on a single connection. A writer in another process can commit between two queries, so a snapshot can be torn (a task `completed` whose execution still reads `running`). | `Store.ListTasks`, `ListExecutions`… each call `s.db.QueryContext` |
| Incremental events | No `ListEventsSince(cursor)`. Ordering is `created_at, rowid`; the `id` is a UUID, so it cannot be used as a cursor. | `ListEvents` in `repositories.go` |
| Change detection across processes | Nothing tells another process that the database changed. | — |
| Events for every state change | Agent rows (`SaveAgent`), execution start (`SaveExecution` with `running`) and artifacts (`AddArtifact`) change state **without** an event. Only the end of an execution (`execution.<status>`) is evented. | `execution_manager.go`, `orchestrator.go`, `recovery_manager.go` |
| Typed event contract | Payloads are `map[string]any` with ad-hoc keys; types are built by string concatenation (`"task."+status`). | `insertEvent`, `Scheduler.Transition` |
| Starting a run without blocking the UI | `Orchestrator.Run` blocks until the run ends and its lifetime is the caller's process. | `core/orchestrator.go` (`Run`, `drive`) |
| A known run id before the run starts | The id is generated inside `Run`. A launcher process cannot learn it except by parsing stdout. | `Orchestrator.Run` (`uuid.NewString()`) |
| Live agent output for any agent | No application-level read of a live agent's screen. | `Runtime.ReadAgentOutput` is used only in `agents/manager.go` |
| Attaching a human to an agent | The CLI prints a `herdr … agent attach` hint built from config strings; the runtime interface has no notion of "attach". | `session.attachHint` in `execution_manager.go` |
| Cross-process worktree ownership | Worktree locks are in-memory (`Orchestrator.wtLock`). A human editing from another process is invisible to the orchestrator. Worse: `collectChanges` runs `git add -A` + commit before every review, so a human's uncommitted edits in an agent worktree would be committed as the agent's work. | `core/orchestrator.go` (`wtLock`), `execution_manager.go` (`collectChanges`, `CommitAll`) |
| Process-group cleanup | Validation commands use `exec.CommandContext` without a process group: cancellation kills the direct child only; grandchildren (e.g. test binaries spawned by `go test`) can survive. | `runCommand` in `execution_manager.go` |

## 3. Architectural strengths

- **Persisted state is the source of truth, and it is transactional.** Every
  run/task transition is a compare-and-swap plus an event in the same
  transaction. A UI can rebuild everything from the database at any time.
- **Multi-process by design.** Ownership is a heartbeat, cancellation goes
  through the database, and resume is claimed with a compare-and-swap. A
  separate Studio process fits this model without changing the core.
- **Runtime independence is real.** `core` imports `runtime`, never
  `runtime/herdr` (`grep` confirms). Herdr is wired only in `internal/app/app.go`.
- **"Done" is evidence-based** (report file, diff, exit codes). Studio can
  display claims and evidence separately because the data already separates them
  (`Execution.ExitCode` set only for orchestrator-run commands; reports labelled
  "claimed" in `BuildReport`).
- **Small and readable:** ~5.3 k lines of non-test Go (plus ~2.4 k of tests) across ten packages; 63 tests, race-clean.
- **Artifacts on disk with stable paths** (`artifacts/<run>/<task>/<exec8>/prompt.md|report.json|output.txt`, `<task>/changes.diff`, `<task>/attempt-N/command-N.log`).

## 4. Architectural weaknesses

| # | Weakness | Evidence | Impact on Studio |
|---|---|---|---|
| W1 | No application layer. `internal/app.App` exposes `Store`, `RT` and `Orch` as public fields and the CLI calls repositories and the runtime directly. | `internal/app/commands.go`: 9 `a.Store.*` calls, 2 `a.RT.*` calls | A TUI would copy that pattern and violate the separation principle. An application API must come first. |
| W2 | Progress is formatted text through a callback. | `Orchestrator.Progress func(line string)` | Not usable as structured state; only useful in-process. |
| W3 | Event stream is not consumable incrementally. | `ListEvents(runID)` only | Polling would re-read the whole log. |
| W4 | Some state changes are not evented, and `o.event` writes outside the state-changing transaction. | `SaveAgent`, `SaveExecution`, `AddArtifact`; `Orchestrator.event` | Events cannot be the only input of the UI; they can trigger re-reads. |
| W5 | Single connection with `_txlock=immediate`. | `database.go` (`SetMaxOpenConns(1)`, `_txlock`) | A read transaction opened through this pool would take the write lock. Studio needs its own read-only connection. |
| W6 | Agent output is not observable until an execution ends. | `captureOutput` after `await` | The Agent Room needs a live read path. |
| W7 | Worktree ownership is process-local. | `wtLock` | Manual edits need a cross-process contract. |
| W8 | Run hosting is tied to the calling process. | `Run`/`Resume` block | A run started by Studio must not die with Studio. |

## 5. Technical risks

| Risk | Likelihood | Notes |
|---|---|---|
| Embedding interactive terminals inside the TUI needs a terminal emulator, which is a large, platform-sensitive component | High | See `technical-decisions.md` ADR-004 |
| Building a real code editor inside a TUI is open-ended | High | See ADR-003; recommendation avoids it for the first editing increment |
| Human edits racing with agent writes in the same worktree | Medium | Needs the worktree lease (ADR-008) |
| Polling the runtime CLI for live agent output is a process spawn per read | Medium | Read only for the focused agent; bounded frequency |
| SQLite readers in another process during heavy writing | Low | WAL readers do not block writers; snapshot reads must use a separate deferred read-only connection |
| Herdr-specific behaviour leaking into Studio (attach command, pane ids) | Medium | Optional runtime capabilities (ADR-005) |

## 6. Required foundational improvements (before any TUI code)

These are small, core-adjacent changes. None changes scheduling, the task
lifecycle, recovery or the runtime contract.

1. **Application API** (`internal/service`): queries and commands for runs,
   tasks, agents, executions, artifacts and events. The CLI is migrated onto it
   first, which proves it before Studio exists.
2. **Event cursor**: expose `rowid` as `Event.Seq` and add
   `EventsSince(seq, limit)`. Valid because no table is ever deleted from
   (no `DELETE` statement exists; the only path is `ON DELETE CASCADE`, which
   nothing triggers), so `rowid` is monotonic.
3. **Missing events**: `agent.started`, `agent.stopped`, `execution.started`,
   `artifact.added`, written in the same transaction as the row they describe.
4. **Consistent snapshots**: a read-only, deferred-transaction connection and a
   `RunSnapshot` read in one transaction.
5. **Change detection**: `PRAGMA data_version` on the read connection (cheap,
   changes when another connection commits).
6. **Detached run start**: `caffe run --run-id <uuid>` (and `resume`) so a
   launcher knows the id, plus a launcher that starts the process in its own
   process group and logs to the run's artifact directory.
7. **Live agent output** through the application API.
8. **Worktree lease** (cross-process) and a rule that `collectChanges` never
   commits work it does not own.
9. **Process groups** for orchestrator-run commands.

## 7. Components to reuse

| Component | Reuse for |
|---|---|
| `persistence.Store` and the schema | the single source of truth for every view |
| `core.Orchestrator` (`Run`, `Resume`, `Cancel`, `Clean`) | all run control; Studio never re-implements it |
| `core.BuildReport` | the report view (render the Markdown) |
| `core.Graph` (`Deps`, `Waiting`, `Doomed`) | dependency views and "why is it waiting" |
| `git.Manager` (`Diff`, `ChangedFiles`, `Status`, `IsDirty`, `ListWorktrees`, `owned`) | worktree browser, diffs, dirty detection; path safety |
| `runtime.Runtime` + `Fake` | live agent output, attach (through new optional capability), tests |
| `config.Load` | settings and validation |
| `runtime.Redact` | every display of agent or command output |
| `contracts.ParseReport` | rendering reports with their real structure |

## 8. Components that must not be duplicated

- **Scheduling, retries, review rounds**: only in `core`.
- **Task and run transitions**: only through `core.transition` / `Store.Update*` compare-and-swap.
- **Git operations on managed worktrees**: only through `git.Manager`, which enforces that paths stay under `.caffe/worktrees`.
- **Runtime calls**: only through `runtime.Runtime`; Studio never runs `herdr` itself.
- **Report parsing and redaction**: `contracts`, `runtime.Redact`.
- **Configuration parsing**: `config`.

## 9. Corrections to the existing documentation

- `docs/architecture.md` says agents receive "the summary of every direct
  dependency's latest report"; correct. It does not mention that agent output is
  captured only at the end; Studio docs state it explicitly.
- Nothing in the existing docs claims a live event stream; no correction needed.
