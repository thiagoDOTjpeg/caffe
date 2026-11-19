# Caffè Studio — Engineering Backlog

Status: **Proposed.** Each item is meant to be one reviewable change.
Priorities: **P0** (blocks the first usable release), **P1**, **P2**.
Effort: **S** < **M** < **L** < **XL** (relative). Milestones and order:
[roadmap.md](roadmap.md). Design references: [architecture.md](architecture.md)
(§), [technical-decisions.md](technical-decisions.md) (ADR).

Every item also implies: `make check` passes, and `docs/` is updated where
behaviour changes.

---

## M0 — Foundations

### [x] F-01 · Spike: change detection with `PRAGMA data_version`
- **Result:** confirmed, numbers in ADR-002. Kept as a permanent test (not build-tagged) because F-11 depends on the behaviour.
- **Priority / effort:** P0 · S · **Depends:** —
- **Description:** Measure `PRAGMA data_version` on a second connection while a
  writer commits, under `modernc.org/sqlite` + WAL. Record cost per call and
  whether every commit is detected. Throwaway test code; outcome written into
  ADR-002.
- **Acceptance:** ADR-002 states "confirmed" with numbers, or switches to the
  fsnotify-on-WAL fallback.
- **Packages:** `internal/persistence` (test only).
- **Tests:** the spike itself is a test behind a build tag.

### [x] F-02 · Read-only store connection
- **P0 · S · Depends:** —
- **Description:** `persistence.OpenReadOnly(path)`: `mode=ro`,
  `_txlock=deferred`, `busy_timeout`, foreign keys; no migrations; a
  `ReadTx(ctx, func(*sql.Tx) error)` helper.
- **Acceptance:** read transactions do not block a concurrent writer; writes
  through it fail.
- **Packages:** `internal/persistence`.
- **Tests:** concurrent writer + long read transaction; write attempt fails.

### [x] F-03 · Event cursor and `EventsSince` ← **first task**
- **P0 · S · Depends:** —
- **Description:** `contracts.Event.Seq int64` (= `rowid`), filled by all event
  reads; `Store.EventsSince(ctx, after int64, runID string, limit int)`
  ordered by `rowid`. Document that events are append-only.
- **Acceptance:** returns events strictly after `after`, in insertion order,
  across runs or per run; `ListEvents` also returns `Seq`.
- **Packages:** `internal/contracts`, `internal/persistence`.
- **Tests:** monotonic `Seq` across interleaved transactions from two
  connections; pagination with `limit`; per-run filter.

### [x] F-04 · Missing events in the state transaction
- **P0 · M · Depends:** F-03
- **Description:** Emit `agent.started`/`agent.stopped` (from `SaveAgent` on
  status change), `execution.started` (first `SaveExecution` of an id),
  `artifact.added` (`AddArtifact`), each in the same transaction as the row.
- **Acceptance:** a full fake run produces these events with run/task/agent
  ids; no existing event changes name or payload.
- **Packages:** `internal/persistence`, `internal/core` (call sites only if needed).
- **Tests:** event sequence of `TestFullRunPlansDelegatesReviewsAndValidates`
  includes the new types in the right order.

### [x] F-05 · Event type constants and payload documentation
- **P0 · S · Depends:** F-04
- **Description:** constants in `contracts` for every event type (prefixes for
  `task.*`, `run.*`, `execution.*`); a table of payload keys in
  `docs/architecture.md`.
- **Acceptance:** no string literal event type left in `core`.
- **Packages:** `internal/contracts`, `internal/core`, `docs/`.
- **Tests:** existing tests unchanged and green.

### [x] F-06 · Consistent `RunSnapshot`
- **P0 · M · Depends:** F-02, F-03
- **Description:** one read transaction returning run, tasks (+ deps),
  agents, executions, artifacts and `MAX(rowid)` of events.
- **Acceptance:** never torn under concurrent writes.
- **Packages:** `internal/persistence`.
- **Tests:** writer goroutine on another connection completing tasks +
  executions in loops; every snapshot satisfies invariants (no completed task
  with a running execution of the same attempt; counts consistent).

### [x] F-07 · `internal/service`: queries and view types
- **P0 · L · Depends:** F-06
- **Description:** `Service` with `Runs`, `RunSnapshot` (view types),
  `EventsSince`, `Execution`, `ReadArtifact` (bounded, redacted), `Report`,
  `Capabilities`. Derived fields: task waiting reason (reuse `core.Graph`),
  agent visual state (table in ux-specification §4.1), observed vs claimed.
- **Acceptance:** no Studio-facing logic outside `service`; derivations are
  pure functions.
- **Packages:** new `internal/service`.
- **Tests:** table tests for agent visual state and waiting reasons; integration
  test on a fake run.

### [x] F-08 · CLI on the service
- **P0 · M · Depends:** F-07
- **Description:** `status`, `runs`, `tasks`, `inspect`, `agents` use
  `service`; `App` stops exposing `Store`/`RT` to commands.
- **Acceptance:** identical CLI output (golden files); `internal/app/commands.go`
  has no `a.Store.`/`a.RT.` references.
- **Packages:** `internal/app`, `internal/service`.
- **Tests:** golden-output tests for each command on a seeded database.

### [x] F-09 · `--run-id` for `caffe run`
- **P0 · S · Depends:** —
- **Description:** optional flag; validated UUID; passed to `Orchestrator.Run`
  (new optional parameter or field).
- **Acceptance:** run created with the given id; duplicate id fails clearly.
- **Packages:** `internal/core`, `internal/app`.
- **Tests:** core test with explicit id; duplicate.

### [x] F-10 · Detached launcher
- **Done on Unix.** Windows: `detach_windows.go` compiles (`CREATE_NEW_PROCESS_GROUP | DETACHED_PROCESS`) but was never run; survival of the child when the caller's console closes is **unverified**.
- **P0 · M · Depends:** F-09, F-07
- **Description:** `service.StartRun`/`ResumeRun`: exec the current `caffe`
  binary (`os.Executable`) with `--run-id`, own session (`Setsid`), stdin
  null, output to `artifacts/<run>/console.log`; wait for the run row or child
  exit (≤ 10 s).
- **Acceptance:** run survives the launcher process; start failure returns the
  console tail.
- **Packages:** `internal/service`.
- **Tests:** integration test with a fake-runtime build of the binary (or a
  helper process via `os.Args[0]` re-exec pattern); failure path.

### [x] F-11 · Watcher
- **P0 · S · Depends:** F-01, F-02
- **Description:** `service.Watcher.Changes(ctx)` per ADR-002; pausable.
- **Acceptance:** emits within one tick after another connection commits;
  silent otherwise.
- **Packages:** `internal/service`.
- **Tests:** commit from a second connection → one `Change`.

### [x] F-12 · Diff base from persisted state + `TaskDiff`
- **P0 · M · Depends:** F-07
- **Description:** export the engine's diff-base rule (nearest developer
  dependency's branch merge-base, else run base commit) as a function over
  persisted tasks; use it in `core` and in `service.TaskDiff` (stored artifact
  or live `git diff`).
- **Acceptance:** engine behaviour unchanged; `TaskDiff` equals the stored
  `changes.diff` for a finished task.
- **Packages:** `internal/core`, `internal/service`.
- **Tests:** existing stacked-developer test; new equality test.

### [x] F-13 · Process groups for validation commands
- **Done on Unix.** Windows: no-op (`procgroup_windows.go`); children of a cancelled command can survive. Upgrade path: a job object with kill-on-close.
- **P0 · S · Depends:** —
- **Description:** `runCommand` starts commands in their own process group
  and kills the group on timeout/cancel (Unix); Windows behaviour documented.
- **Acceptance:** a command that spawns a sleeping grandchild leaves nothing
  running after cancel.
- **Packages:** `internal/core`.
- **Tests:** grandchild survival test, portable since W-03 (`core/procgroup_test.go`).

### [x] F-14 · `runtime.Attacher` capability
- **P1 · S · Depends:** —
- **Description:** optional interface (ADR-005); Herdr implementation;
  `attachHint` uses it.
- **Acceptance:** CLI blocked-agent hint unchanged; fake runtime can opt in.
- **Packages:** `internal/runtime`, `internal/runtime/herdr`, `internal/core`.
- **Tests:** adapter argv test with and without session.

### [x] F-15 · Live agent output and runtime status queries
- **P1 · M · Depends:** F-07
- **Description:** `AgentOutput` (live via `ReadAgentOutput` if the agent
  exists, else captured `output.txt`; labelled; redacted; bounded lines) and
  `AgentRuntimeStatus` for a set of agents.
- **Acceptance:** source label correct in both cases; runtime errors map to
  `ErrUnavailable`.
- **Packages:** `internal/service`.
- **Tests:** fake runtime live/gone cases.

### [x] F-16 · `studio:` configuration section
- **P1 · S · Depends:** —
- **Description:** optional section with `theme`, `mouse`, `reduce_motion`,
  `avatars`, `editor`; strict validation like the rest.
- **Acceptance:** absent section = defaults; unknown keys rejected.
- **Packages:** `internal/config`.
- **Tests:** config table tests.

### F-17 · Spike: TUI stack
- **P0 · S · Depends:** —
- **Description:** throwaway program on `charm.land/bubbletea/v2`: alt screen,
  resize, `ExecProcess` to `$EDITOR` and back, `Program.Send` from a
  goroutine, at Go 1.24 vs the library's minimum Go; check a v2-compatible
  test helper.
- **Acceptance:** ADR-001 updated with the pinned versions and Go minimum.
- **Packages:** none committed except ADR changes.
- **Tests:** manual on **Linux and Windows** (Windows Terminal and the classic console host).
- **Status:** spike written (`spikes/tui`, in the Windows kit as `tui-spike.exe`, Linux build in `dist/linux/tui-spike`); Finding: Bubble Tea v2.1.1 needs Go 1.26 (ADR-001).
- **Linux: OK** (2026-11-18, reported by the maintainer on their machine: every check of the spike worked). **Windows: pending** (kit step 2). F-17 closes when Windows is checked too (NFR-T1).

## M0w — Windows parity

Target decided 2026-11-18: Linux + Windows; macOS out of scope.

### [x] W-01 · Portable test fakes
- **Done.** Herdr adapter tests pass as a Windows binary under Wine, and on real Windows. **Real Windows (2026-11-18, kit of `4635dc9`, Windows 10.0.26200, Git 2.54): kit step 1 passed, all 13 packages.** Side fix: fakes set `GORACE=atexit_sleep_ms=0` (a `-race` child otherwise sleeps 1 s per exit; the adapter suite went 12 s → 1.7 s).
- **P0 · M · Depends:** —
- **Description:** the Herdr adapter tests use a `#!/bin/sh` script as fake
  `herdr`, and the orchestrator tests use `sh check.sh` as validation
  commands. Replace both with the test binary re-executing itself as the
  fake (the pattern of `service/launcher_unix_test.go`), so no test needs a
  shell.
- **Acceptance:** `grep -rn '"sh"\|#!/bin/sh' --include=*_test.go` finds only
  Unix-only test files; the same assertions hold.
- **Packages:** `internal/runtime/herdr`, `internal/core`.

### [x] W-02 · Path separators in planner context
- **P0 · S · Depends:** —
- **Description:** `planner.CollectContext` prints OS paths (`cmd\app\` on
  Windows). Print slash paths (`filepath.ToSlash`), as Git does, so prompts
  and tests are the same on both platforms. Found by running the Windows
  test binary under Wine.
- **Acceptance:** `TestCollectContext` passes on Windows.

### [x] W-03 · Kill process trees on Windows
- **Done; validated on real Windows:** **Real Windows (2026-11-18, kit of `4635dc9`, Windows 10.0.26200, Git 2.54): kit step 1 passed, all 13 packages.** Job object with kill-on-close; the test is now one portable file, and with the job disabled it fails (control run). Found and fixed on the way: a *cancelled* command was recorded with the exit code of its killing (1 on Windows) instead of no result; `runCommand` now returns −1 + "cancelled" on both platforms. Known gap: a child spawned in the instant between `Start` and the job assignment escapes (needs `CREATE_SUSPENDED`, which `os/exec` cannot resume).
- **P0 · M · Depends:** F-13
- **Description:** `procgroup_windows.go` is a no-op today. Assign each
  validation command to a job object with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`
  and close it on timeout, cancel and exit (via `golang.org/x/sys/windows`,
  already an indirect dependency).
- **Acceptance:** the grandchild-survival test has a Windows version and passes there.

### W-04 · Detached runs on Windows
- **P0 · M · Depends:** F-10
- **Description:** verify `detach_windows.go` (`CREATE_NEW_PROCESS_GROUP |
  DETACHED_PROCESS`) on real Windows: the run survives closing the launching
  console and Ctrl-C in it; fix the flags if not.
- **Acceptance:** launcher tests have a Windows version; manual check of
  closing the console.
- **Progress (2026-11-18):** launcher tests are portable (`service/launcher_test.go`); on Windows the child must have no console (`GetConsoleWindow() == 0`). Passes under Wine, **but a control build without `DETACHED_PROCESS` passes too there** — Wine cannot tell; the assertion is first exercised by `run-tests.ps1` on real Windows. Manual console-close check: kit step 4. **Real Windows (2026-11-18, kit of `4635dc9`, Windows 10.0.26200, Git 2.54): kit step 1 passed, all 13 packages.** The no-console assertion now holds on real Windows; still pending: the manual console-close / Ctrl-C check (kit step 4).

### W-05 · CI on Linux and Windows
- **P0 · S · Depends:** W-01, W-02 · **Requires a decision:** where the CI runs
  (the repository has no remote yet).
- **Description:** run `go vet ./...` and `go test -race ./...` on Linux and
  Windows for every change; Git for Windows available on the runner.
- **Acceptance:** both jobs green.

### W-06 · Real run through Herdr on Windows
- **P1 · S · Depends:** W-05 · **Manual.**
- **Description:** the end-to-end flow of `docs/development.md` on Windows
  (Herdr's socket is a named pipe there; agent CLIs installed and signed in).
- **Acceptance:** one run completes; differences from Linux documented in
  `docs/runtime-integration.md`.

## M1a — Command Center (read-only)

### [x] CC-01 · `caffe studio` bootstrap
- **P0 · M · Depends:** F-07, F-17
- **Description:** command, program start, alt screen, panic/signal handler
  that restores the terminal, Studio log file.
- **Acceptance:** any exit path restores the terminal; panic writes a crash log.
- **Packages:** `internal/studio`, `internal/app`.
- **Tests:** model init test; crash handler test.

### [x] CC-02 · Layout regions and responsive breakpoints
- **P0 · M · Depends:** CC-01
- **Description:** header, sidebar, main, bottom panel, status bar; sizes per
  ux-specification §1.3.
- **Acceptance:** golden renders at 60×16, 80×24, 120×40, 200×60.
- **Packages:** `internal/studio/ui`.
- **Tests:** golden view tests.

### [x] CC-03 · Theme and symbols
- **P0 · S · Depends:** CC-02
- **Description:** palette from `docs/brand.md`, high-contrast theme,
  `NO_COLOR` monochrome, state symbols table.
- **Acceptance:** every state distinguishable in monochrome.
- **Tests:** golden renders in each theme.

### [x] CC-04 · Action registry, key maps, help, palette, leader menu
- **Done.** Overlays are drawn in place of the main region for now (floating layers when a screen needs to stay visible). No `enabled` predicate yet: no action needs one so far.
- **P0 · L · Depends:** CC-02
- **Description:** registry (id, title, scope, keys, enabled); help overlay,
  command palette and leader menu generated from it.
- **Acceptance:** no key handled outside the registry; conflicting bindings
  in one scope fail a test.
- **Packages:** `internal/studio/ui`.
- **Tests:** conflict detection; palette filtering.

### [x] CC-05 · Live state: watcher, snapshots, feed, staleness
- **Done.** `internal/studio/live.go`: one feed ring of 2 000 events for all runs (filtered per run in the view), first load starts at `Seq − 2000` instead of replaying history; a watcher that fails to start shows "not updating". Studio-triggered immediate refresh comes with CC-14.
- **P0 · M · Depends:** F-11, CC-01
- **Description:** model wiring per architecture §7: debounced refresh,
  `Seq`-based dedupe, ring buffer, stale marking, backoff.
- **Acceptance:** transition visible ≤ 1 s; database error → stale banner, no crash.
- **Tests:** message-driven tests with a fake service.

### [x] CC-06 · Runs screen
- **P0 · S · Depends:** CC-05 · **Acceptance:** list, filter, selection kept by id.
- **Done.** `j/k g/G` move, `/` filters by id/status/request (enter keeps, esc clears), `a` active only, `enter` selects the run and opens Overview. Run actions (`n R c x`) are CC-14.

### [x] CC-07 · Overview screen
- **P0 · M · Depends:** CC-05 · **Acceptance:** matches wireframe §2.1 fields; health indicators from heartbeat and capabilities.
- **Done.** Header carries run, progress and runtime; `Capabilities` rechecked every 10 s (`service.RuntimeInfo` alias keeps `runtime` out of Studio). AGENTS and HEALTH are stacked, not side by side; the warning count is over the in-memory feed (a lower bound on long runs).

### [x] CC-08 · Task Board (columns + details)
- **P0 · M · Depends:** CC-05 · **Acceptance:** status→column mapping per §2.2; waiting reason shown.
- **Done.** `h/l` move between non-empty columns, `j/k` within one; cursor by task id (follows a task to its new column). Unknown statuses fall into PENDING. Card actions (`enter d w a`) come with CC-11/WS/CC-16; `g`/`l` view switch with CC-09.

### [x] CC-09 · Task dependency graph view
- **P1 · M · Depends:** CC-08 · **Acceptance:** layered rendering; fallback to list above 30 tasks.
- **Tests:** layering algorithm unit tests (diamond, fan-out, long chain).
- **Done.** `b` board, `g` graph, `L` list (`l` is already "next column"). Layers by longest path, ordered by barycenter sweeps; an edge between same-row neighbours is an arrow, every other dependency is listed under the task (`⤷ a, b`) instead of crossing lines.

### [x] CC-10 · Agent Room cards (stored state)
- **P0 · M · Depends:** CC-05 · **Acceptance:** visual states from F-07; sort order per §2.3.
- **Done.** Order: blocked/lost, working/waiting, interrupted, starting, idle, finished; ties keep snapshot order. Focused card has a double border; `h/l` across, `j/k` by row; cursor by agent id. Avatars are PO-01; "idle for …" on Waiting needs runtime status (CC-15); card actions come with CC-11/CC-15/CC-16.

### [x] CC-11 · Execution Inspector
- **P0 · M · Depends:** CC-05 · **Acceptance:** Report/Prompt/Output/Artifacts/Events tabs; observed vs claimed labels identical to `caffe inspect`.
- **Done.** Opened with `enter` on a Task Board card (latest execution) or `i` on an agent card; `tab`/`h l` switch tabs, `j k ctrl+d ctrl+u` scroll, `esc` closes. Labels are `contracts.LabelObserved`/`LabelClaimed`, shared with the run report. Service: `ExecutionDetail.Report` (the execution's own report, beside its `prompt.md`, else the task's latest) + `ClaimsUnverified`, and `ReadPrompt` (confined to the artifact directory). Running executions reload on each change; Events tab is over the in-memory feed.

### [x] CC-12 · Events panel with filters
- **P0 · S · Depends:** CC-05 · **Acceptance:** filter by task and type; scroll back loads older pages.
- **Done.** `tab`/`shift+tab` focus the panel (when visible); then `j/k ctrl+d ctrl+u G` scroll, `/` type substring, `t` cycles the task filter, `x` clears. Reaching the top of what is loaded reads the previous 200 events with the new `EventsBefore` (persistence + service). The inspector's tabs moved to `h/l` (`tab` is now the global pane key). Fixed on the way: the panel dropped the newest event (it rendered H lines into H−1 rows). Known gap: if the live ring evicts events while older pages are loaded, a hole can appear between them.

### [x] CC-13 · Confirm dialog and toasts
- **P0 · S · Depends:** CC-04 · **Acceptance:** default "Cancel"; dialog text names target and consequence.
- **Done.** `model.ask(title, action, body, handler)` opens a centred dialog that captures every key; `h/l/tab` move, `enter`/`space` choose, `esc q n` cancel; the handler runs only on an explicit choice of the action button. Toasts: `notify` (expires after 5 s) and `failed` (stays; `esc` dismisses errors before closing a view), newest shown in the status bar. No action uses the dialog yet: the first consumers are CC-14's cancel and clean.

### [x] CC-17 · Report view
- **P1 · S · Depends:** CC-05 · **Acceptance:** renders `service.Report` Markdown as plain text sections.
- **Done.** `r` on Overview or `space r o`; `j/k ctrl+d ctrl+u g/G` scroll, `esc` closes. `markdownText` covers what `core.BuildReport` writes (headings, tables → aligned columns, lists, `**`/`` ` `` dropped), not Markdown in general.

### [x] CC-18 · Empty, loading, error, disconnected states
- **P0 · S · Depends:** CC-05 · **Acceptance:** every state in ux-specification §6 has a golden render.
- **Done.** Goldens `state-*` (plus `layout-50x12` for too small). `caffe studio` outside a project opens a full-screen message instead of failing; the header shows `✗ database` while stale and the adapter's own error when the runtime is unreachable; interrupted runs show the resume/cancel commands; a Seq gap in the feed (cannot normally happen) is logged and the feed reloaded from a fresh snapshot. The empty state says `caffe run "…"` only: the `n` key comes with CC-14, and "live output/attach disabled with the reason" with CC-15/CC-16.

### [x] CC-19 · End-to-end Studio test on the fake runtime
- **P0 · M · Depends:** CC-06 … CC-11
- **Description:** drive a full fake run in another goroutine and assert
  Studio's model reaches the expected screens/states.
- **Tests:** this is the test; plus import-boundary test for `internal/studio`.
- **Done.** `internal/studio/e2e_test.go`: a real orchestrator (real Git + SQLite, fake runtime) runs an impl → review plan in a goroutine; Studio's model runs on the real service and watcher through a small Bubble Tea-like pump (no v2 `teatest` was needed). Asserts the empty state, then impl running / review waiting / developer card while the agent is held, then the completed run on the board, the inspector's claim label, the Runs screen, the report and an ordered, deduped feed. Stable over `-race -count=5`. Engine imports are in the test only; `boundary_test.go` still guards Studio's code.

## M1b / M1c — Run controls and live agents

### [x] CC-14 · Run controls
- **P0 · M · Depends:** F-10, CC-13
- **Description:** new run (request or plan file), cancel, resume, clean
  through `service.Commands`, with confirmations and in-flight state.
- **Acceptance:** run survives Studio exit; `ErrConflict` refreshes and informs.
- **Done.** Keys: `n` new (form: request + optional plan file), `c` cancel, `R` resume, `x` clean on Runs (cursor run) and Overview (selected run), and `space r n/c/R/x` everywhere. Cancel and clean confirm (Cancel is the default); resume refuses non-interrupted runs. In flight: the run shows `⋯ cancelling`/`(cancelling…)`, a second request is refused; every result refreshes at once. New runs use `StartRun` (detached, F-10: survive Studio). Service gained `CancelRun`/`Clean` through an `Engine` (the App passes its orchestrator), `ErrConflict` (lost race or already finished: toast + refresh, no retry) and `ErrUnsupported` (no engine). `clean --failed` is CLI-only for now.

### [x] CC-15 · Live agent output
- **P1 · M · Depends:** F-15, CC-10 · **Acceptance:** focused agent at 1 Hz, labelled source, paused when hidden.
- **Done.** `o` (or `space a o`) opens the output pane in the lower half of the Agent Room for the card under the cursor: read once a second only while the room is visible, titled with its source (`live, refreshed 1s ago` / `captured at end` / `no output saved` / the runtime's error). The same tick reads the runtime status of unfinished agents on the Overview or Agent Room every 2 s and re-derives their visual state with it (Working, Waiting, Blocked, Lost), advisory only: finished executions keep their stored state, and status older than 6 s is ignored. Test helper `drive` now runs commands concurrently and settles after 50 ms of quiet.

### [x] CC-16 · Attach hand-off
- **P1 · S · Depends:** F-14, CC-10 · **Acceptance:** `ExecProcess` of the attach argv; full refresh on return; hidden when unsupported.
- **Done.** `a` (or `space a a`) on an unfinished agent's card: `service.AttachAgent` builds the runtime's attach argv (`ErrUnsupported` without `runtime.Attacher`, `ErrConflict` for a stopped agent), Studio pauses the watcher, hands the terminal over with `tea.ExecProcess`, and on return resumes the watcher, re-reads the run and runtime health and reports the result. `[a] attach` is only offered when `service.CanAttach()`. Not yet exercised against a real Herdr attach (kit step 3 territory).

## M2a — Workspace (view)

**Done (2026-11-19):** WS-01 … WS-10. Manual checks of the roadmap (100 k-file repository, 50 MB log, scratch-repo branches) still to do on a real project.

### [x] WS-01 · Workspaces query and selector
- **Done.** `service.Workspaces` (main + recorded task worktrees, `Missing`, `Dirty`); lease field deferred to WS-11. Selector: `ctrl+w` or `space w s`; removed worktrees cannot be selected. Status bar always shows `ws:`; the header shows it for a task workspace.
- **P0 · M · Depends:** F-07 · **Description:** `service.Workspaces`
  (main + task worktrees, missing-on-disk flagged, dirty, lease). Selector
  overlay. **Acceptance:** header/status bar always show the selected workspace.

### [x] WS-02 · Explorer
- **Done.** `service.Files`: `git ls-files` (tracked + untracked, not ignored) with marks `A`/`M`/`D` vs the task's diff base (`core.DiffBase`, shared with `TaskDiff`) and `●` for uncommitted files; deleted files are listed. Explorer next to the viewer in the Workspace screen (not in the sidebar: 20 columns is too narrow); index built once, only expanded directories walked (100 k files: test). Keys: `j`/`k`, `enter`/`l` open, `h` collapse/parent; `h` in the viewer returns to the tree.
- **P0 · M · Depends:** WS-01 · **Description:** lazy tree from `git ls-files
  --cached --others --exclude-standard`; change markers vs task base.
  **Tests:** large listing (100 k entries) stays responsive.

### [x] WS-03 · Highlighted viewer
- **Done.** `service.ResolvePath` (symlinks followed, escapes refused) and `service.ReadFile` (bounded, binary detected). Viewer in the Workspace screen: Chroma v2 tokens mapped to theme tones (plain in mono), visible lines only, plain above 2 MB, reads at most 16 MB. Own scrolling instead of `bubbles/viewport` (no new dependency; same pattern as the report view). Opening a file comes with the explorer (WS-02).
- **P0 · M · Depends:** WS-01 · **Description:** `bubbles/viewport` + Chroma v2,
  visible-region tokenisation, large-file plain mode (NFR-P4), `ResolvePath`.
  **Tests:** path escape and symlink tests; golden renders.

### [x] WS-04 · Tabs and splits
- **Done.** Buffers keyed by (workspace, path): opening an open file activates its tab. Tab bar over the viewer, `workspace:name` when the buffer is not in the selected workspace; buffers survive switching workspace. Keys in the viewer: `[`/`]` tabs, `x` close, `|` split (previous tab on the left), `\` switch side. One split only.
- **P1 · M · Depends:** WS-03 · **Acceptance:** buffers keyed by (workspace, path); tabs show workspace when it differs.

### [x] WS-05 · Diff view
- **Done.** `d` on a developer task (Task Board), in a task workspace (explorer/viewer) or `space w d`. Changed files with `A`/`M`/`D` and +/− counts on the left; unified, or side by side (`s`, terminal ≥ 120 columns); `n`/`p` next/previous file. `enter` opens the file at the hunk in the task's worktree and selects that workspace (no question: M2a has no unsaved buffers). Stored diffs (worktree removed) show, but cannot open files.
- **P0 · L · Depends:** F-12, WS-03 · **Acceptance:** unified and side-by-side; file list; open file at hunk in its worktree.

### [x] WS-06 · Git status panel
- **Done.** `git.Status2` parses `status --porcelain=v2 --branch -z` (replaces the v1 parser); `service.Files` carries branch and staged/modified/untracked/conflicted counts. Shown as a GIT block under the explorer tree, with a task's committed change count vs its diff base. No separate panel: it is three lines.
- **P1 · S · Depends:** WS-01 · **Description:** `git status --porcelain=v2` via `internal/git`.

### [x] WS-07 · File finder
- **Done.** `space w f`, or `f` in the explorer/viewer. Fuzzy subsequence match over the explorer's list (matched from the end, so file names win; gaps and directory matches cost), 200 best shown; `enter` opens.
- **P1 · S · Depends:** WS-02 · **Description:** fuzzy match over `ls-files`.

### [x] WS-08 · Content search
- **Done.** `service.Search`: literal, smart case; `rg --json` (lenient: unknown types and broken lines skipped, `bytes` paths and lines decoded; test on recorded rg 15.2 output), else `git grep -z -n --untracked --exclude-standard`; stops at the limit (500) and kills the search. `space w g`: type, `enter` searches, `enter` opens the hit at its line.
- **P1 · M · Depends:** WS-01 · **Description:** `rg --json` lenient parser, fallback `git grep -n`. **Tests:** parser on recorded output incl. `bytes` fields and unknown types.

### [x] WS-09 · `$EDITOR` hand-off
- **Done.** `e` in the viewer: `$VISUAL`, else `$EDITOR` (split on spaces, no shell), else `vi`/`notepad`; at the top visible line (`+N`, or `file:N` for code/subl/hx/zed). Watcher paused during the hand-off; on return the file and the tree reload (expansion kept). Refused while the workspace's task is running (until leases, WS-11).
- **P0 · S · Depends:** WS-03 · **Acceptance:** opens at line; buffer reloads if changed.

### [x] WS-10 · File watching for open buffers
- **Done, without fsnotify.** One `os.Stat` of the open file per second while the Workspace screen shows it (no I/O when hidden, NFR-P6); a change reloads it in place (the viewer is read-only) and the title says "changed on disk, reloaded" or "deleted on disk". No new dependency and the same on Windows; fsnotify on the buffers' directories becomes worth it with tabs (WS-04) or editing (M2b).
- **P1 · S · Depends:** WS-03 · **Description:** fsnotify on open files' directories only; changed-on-disk marker.

## M2b — Workspace (edit)

### WS-11 · `worktree_leases` table and store API
- **P1 · M · Depends:** — · **Description:** migration `002`; acquire (CAS), renew, release, expire. **Tests:** two connections racing to acquire.

### WS-12 · Engine honours leases
- **P1 · L · Depends:** WS-11 · **Description:** agent lease around developer executions and corrections; mark task `running` before acquiring; `blocked` while a human holds it (architecture §9.2).
  **Acceptance:** no change in behaviour without human leases; all existing engine tests green.
  **Tests:** correction waits for a human lease; crash with an agent lease → recovery releases it; expired dirty human lease blocks.

### WS-13 · Studio human lease
- **P1 · M · Depends:** WS-11, WS-01 · **Acceptance:** heartbeat; release refused while dirty with the reason.

### WS-14 · Built-in editor model
- **P1 · XL · Depends:** WS-03, WS-13 · **Description:** ADR-003 scope: buffer interface, insert/normal, undo/redo, in-buffer search, safe save (temp + rename, changed-on-disk check).
  **Tests:** edit-operation property tests (undo∘do = id); save conflict tests.

### WS-15 · Manual commit and discard
- **P1 · M · Depends:** WS-13 · **Description:** `CommitManual` with the user's identity and `manual:` prefix; discard confirmed. **Acceptance:** manual commits never contain files the agent changed after the lease was taken (they cannot: lease).

### WS-16 · Clean honours leases
- **P1 · S · Depends:** WS-11 · **Acceptance:** `caffe clean` skips leased worktrees and says why.

## M3a / M3b — Execution

### [x] EX-01 · `internal/session`: command sessions
- **P1 · L · Depends:** — · **Description:** argv sessions, process group, ring buffer, log file, batched output messages, stop (TERM→KILL). **Tests:** orphan check; buffer bound; batching.

### [x] EX-02 · Terminal Manager screen
- **P1 · M · Depends:** EX-01, CC-04

### [x] EX-03 · Run validation commands in a workspace
- **P1 · S · Depends:** EX-01, WS-01 · **Acceptance:** same commands as `validation.commands`, labelled as a manual run (not recorded as a task execution).

### [x] EX-04 · `file:line` links
- **P1 · S · Depends:** EX-02, WS-03 · **Tests:** Go test output, compiler errors, stack traces; links outside the workspace refused.

### [x] EX-05 · Shell hand-off
- **P1 · S · Depends:** CC-01, WS-01

### [x] EX-06 · Session cleanup on quit and signals
- **P1 · S · Depends:** EX-01 · **Acceptance:** quit confirms when sessions run; nothing survives.

### EX-07 · Spike: embedded PTY pane
- **P2 · M · Depends:** EX-01 · **Description:** ADR-004 criteria with `go-pty`/`x/xpty` + `x/vt`. **Acceptance:** go/no-go recorded in ADR-004.

### EX-08 · Embedded shells
- **P2 · XL · Depends:** EX-07 (go) · **Acceptance:** criteria of ADR-004 met in production code.

## M4 — Integration

### IN-01 · Integration decisions model
- **P1 · M · Depends:** F-07 · **Description:** table (task, decision approve/reject, note, author, time); service API; shown in `caffe inspect`.

### IN-02 · Integration screen
- **P1 · M · Depends:** IN-01, WS-05

### IN-03 · Merge preview and confirmed merge
- **P1 · L · Depends:** IN-01, WS-15 · **Description:** `git merge-tree --write-tree` preview; exact command shown; executes only on confirmation, only on a clean main checkout; never pushes.
  **Tests:** conflicting and clean branches; dirty checkout refusal.

### IN-04 · `caffe integrate` CLI parity
- **P2 · S · Depends:** IN-03

## M5 — Code intelligence

### CI-00 · Investigate LSP protocol types
- **P2 · S · Depends:** — · **Description:** module-graph and Go-version impact of `go.lsp.dev/protocol` v1 (ADR-006). **Acceptance:** ADR-006 finalised.

### CI-01 · JSON-RPC LSP client core
- **P2 · L · Depends:** CI-00 · **Tests:** fake server over pipes: init/shutdown, request/response, notifications, server crash.

### CI-02 · Server manager
- **P2 · M · Depends:** CI-01, WS-01 · **Acceptance:** one server per (language, root), LRU cap, clean shutdown.

### CI-03 · Document sync and diagnostics panel
- **P2 · M · Depends:** CI-02, WS-03

### CI-04 · Definition, references, symbols
- **P2 · M · Depends:** CI-03 · **Acceptance:** locations outside the workspace are flagged, not opened silently.

### CI-05 · Go test-failure navigation
- **P2 · S · Depends:** EX-04

## M6 — Debugging

### DB-01 · DAP client core
- **P2 · L · Depends:** EX-01 · **Tests:** fake adapter; event handling; disconnect.

### DB-02 · Delve launch (test/debug)
- **P2 · M · Depends:** DB-01 · **Description:** `dlv dap` as a session; `mode: test` from a test name. **Tests:** optional real-`dlv` test behind an env flag.

### DB-03 · Breakpoints in the viewer
- **P2 · M · Depends:** DB-02, WS-03

### DB-04 · Debugger screen: stack, variables, controls
- **P2 · L · Depends:** DB-03

### DB-05 · Debug console
- **P2 · S · Depends:** DB-04

## M7 — Polish (continuous)

| Id | Title | P / effort | Depends | Acceptance |
|---|---|---|---|---|
| PO-01 | Role avatars | P2 · S | CC-10 | optional; never the only state carrier; off in monochrome |
| PO-02 | Working "steam" animation | P2 · S | PO-01 | ≤ 2 fps; off with `reduce_motion`/`NO_COLOR` |
| PO-03 | Additional themes | P2 · M | CC-03 | contrast checked for every state colour |
| PO-04 | Layout customization (persisted per project) | P2 · M | CC-02 | stored outside `config.yaml` (UI state file) |
| PO-05 | Performance pass (large logs, large repos, many tasks) | P2 · M | M2a, M3a | NFR-P targets measured and met |
| PO-06 | Buffer crash-recovery files | P2 · M | WS-14 | unsaved edits recoverable after a crash |
| PO-07 | User documentation for Studio | P1 · M | M1a | README section + `docs/studio/user-guide.md` |
