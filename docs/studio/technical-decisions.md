# Caffè Studio — Technical Decisions

ADR format. Library facts were checked on **2026-11-18** against the module
proxy (`proxy.golang.org/…/@latest`), the projects' repositories at the cited
tag, and the GitHub API. "Verified" means checked there; anything else is
marked **Unverified** or **Investigate**. Versions will move; re-check before
adding a dependency, and pin a release that is at least two weeks old.

Current toolchain constraint: `go.mod` targets Go 1.24+; the local toolchain is
1.27. Some candidates below require newer Go versions — called out per ADR.

| ADR | Decision | Status |
|---|---|---|
| 001 | Bubble Tea v2 + Lip Gloss v2 + Bubbles v2 | Proposed |
| 002 | Event delivery: snapshot + `rowid` cursor + `PRAGMA data_version` | Proposed |
| 003 | Editor: hybrid — highlighted viewer, `$EDITOR` hand-off, then a small own editor | Proposed |
| 004 | Terminals: pipes for commands, hand-off for shells; embedded PTY only after a spike | Proposed |
| 005 | Runtime-specific features as optional capability interfaces | Proposed |
| 006 | LSP: thin own client on a maintained JSON-RPC library | Proposed, Investigate types package |
| 007 | DAP: `google/go-dap` types + `dlv dap` | Proposed |
| 008 | Worktree selection model and cross-process lease | Proposed |
| 009 | Git: keep the Git CLI | Proposed |
| 010 | Runs hosted in detached `caffe` processes | Proposed |
| 011 | File watching and search | Proposed |

---

## ADR-001 — TUI framework

**Context.** Studio needs: an event loop that never blocks on I/O, external
events injected from goroutines, suspending the UI to hand the terminal to an
editor/shell/attach command, alt screen, resize events, optional mouse,
layout composition, and **Windows as a first-class target** (decided 2026-11-18).

**Options.**

| | Bubble Tea v2 (+ Lip Gloss v2, Bubbles v2) | tview / tcell |
|---|---|---|
| Model | Elm-style: `Update` returns commands; state is explicit | retained-mode widgets with callbacks |
| Latest | `charm.land/bubbletea/v2` v2.1.2 (2026-11-18); v2.0.0 stable since 2026-02-24 — verified | `rivo/tview` v0.42.0 (2025-08-27, no tag since); `gdamore/tcell/v3` v3.6.0 — verified |
| Hand-off to a child process | `tea.ExecProcess(cmd, fn)`; also `Program.ReleaseTerminal`/`RestoreTerminal` — verified in `exec.go`, `tea.go` | `Application.Suspend` (not re-verified here) |
| External events | `Program.Send(msg)` — verified | `QueueUpdateDraw` |
| Alt screen / mouse / resize | declarative fields on `tea.View` (`AltScreen`, `MouseMode`), `tea.WindowSizeMsg` — verified | yes |
| Components | Bubbles v2.2.1: viewport, list, table, tree, textarea, textinput, help, key, filepicker, spinner, … — verified | Flex, Grid, Pages, Table, TreeView, TextArea, … |
| Windows | build CI on windows-latest — verified; runtime behaviour of mouse/keys on Windows terminals **unverified** | own Windows CI |
| License | MIT | MIT / Apache-2.0 |

**Decision.** Bubble Tea v2 with Lip Gloss v2 and Bubbles v2, imported only
from `internal/studio/...`.

**Rationale.**
- The Elm model matches §7 of the architecture: one model, all I/O in
  commands, views as pure functions of state — directly testable by feeding
  messages and comparing rendered strings.
- `ExecProcess` is exactly the hand-off Studio relies on for `$EDITOR`,
  shells and runtime attach (ADR-003/004/005).
- tview is mature for static widget layouts (k9s), but its last tag is over a
  year old and pins an older tcell; its callback style spreads state across
  widgets, which works against reconstructible UI state (NFR-R6).

**Consequences / risks.**
- v2 changed import paths to `charm.land/…/v2`; the `github.com/…/v2` path
  does not work (go.mod declares `charm.land`). Use `charm.land` paths only.
- Bubbles components are building blocks; layout (regions, focus, overlays)
  is Studio's own code.
- Lip Gloss v2 moved adaptive colours to `charm.land/lipgloss/v2/compat`;
  background detection in Bubble Tea arrives as `tea.BackgroundColorMsg`.
- **Go version (checked 2026-11-18):** `charm.land/bubbletea/v2` requires
  Go 1.24.2 at v2.0.0, 1.25 at v2.0.5 and **1.26 from v2.0.10** (v2.1.1, the
  version to pin, requires 1.26). The project's `go.mod` already declares
  `go 1.27.3` (written by `go mod init`), although the README says "Go 1.24+":
  the documented minimum must be corrected either way — a decision for the
  maintainer.
- **Spike:** `spikes/tui` (own module, Bubble Tea v2.1.1) checks resize,
  `Program.Send` from a goroutine, keys, mouse, `ExecProcess` to an editor and
  a shell, and panic recovery. **Linux: confirmed** on a real terminal
  (2026-11-18). **Windows: pending** (Windows Terminal and conhost).
- **Investigate (F-17):** a `teatest`-style test helper for v2.

---

## ADR-002 — Event delivery

**Context.** Studio is a separate process. The engine writes runs, tasks and
events to SQLite in WAL mode; some state changes have no event, events have
no cursor, and nothing notifies other processes ([audit.md](audit.md) W3, W4).

**Options.**

| Option | Verdict |
|---|---|
| Event sourcing: rebuild UI state by folding events | Rejected: not all changes are evented today, some events are written outside the state transaction, and making the log complete enough to be the only source would be a core rewrite. |
| Re-read everything on a fixed timer | Simple but wasteful and laggy; violates "avoid aggressive polling". |
| In-process pub/sub (Studio hosts the engine) | Only works for runs Studio started, and ties run lifetime to Studio (rejected by ADR-010). |
| Local socket / daemon broadcasting events | New long-running component and protocol; unnecessary for one machine. |
| Message broker (Redis, NATS, Kafka) | Excluded by principle. |
| **Snapshot + cursor + change detection** | **Chosen.** |

**Decision.**
1. Tables are the source of truth; a **consistent snapshot** of a run is read
   in one read transaction on a separate read-only connection.
2. Events get a cursor, `Seq` = `rowid` (monotonic because nothing ever
   deletes events), and `EventsSince(seq)`.
3. Missing events (`agent.*`, `execution.started`, `artifact.added`) are added
   inside the transaction of the row they describe.
4. Change detection: `PRAGMA data_version` on the read connection every
   250 ms; on change, read new events and refresh the affected snapshot.

**Consequences.** Duplicate processing is avoided by comparing event `Seq` to
the snapshot's `Seq`. Restart needs no stored cursor. Memory is bounded by
keeping snapshots only for visible runs and a ring buffer of events.

**Confirmed (F-01, 2026-11-18)** under `modernc.org/sqlite` v1.62.0, WAL mode,
by `internal/persistence/dataversion_test.go`:
- every commit from another connection changes `data_version` (50/50, race
  detector on); repeated reads, reads by any connection and a rolled-back write
  do not change it;
- cost: ~11–15 µs per call (`BenchmarkDataVersion`, i7-1255U), i.e. ~60 µs of
  CPU per second at a 250 ms interval — negligible;
- **constraint:** the value is per connection. The watcher must read it on one
  pinned `*sql.Conn`, never through the pool (the read-only store has 4
  connections with unrelated values).

The two writers in the test are separate connections in one process; separate
processes go through the same SQLite file locking and WAL index, so the
behaviour is the same. The fsnotify fallback is not needed.

---

## ADR-003 — Editor implementation strategy

**Context.** The product needs practical edits (open, change, save, undo,
search) in agent worktrees, not an IDE. Users already have editors.

**Options (facts verified unless marked).**

| Option | Findings | Verdict |
|---|---|---|
| Bubbles `textarea` | `[][]rune` buffer; hard `maxLines = 10000`; no syntax highlighting; no undo/redo (`textarea.go` at v2.2.1) | Rejected as code editor; fine for commit messages and notes |
| Existing Go editor component | `pgavlin/femto` (tview): no tags, last push 2023, "API … should not be considered stable"; micro's buffer/display code is under `internal/` (only `pkg/highlight` is importable); no maintained Bubble Tea code editor found | None usable |
| Run a real editor (nvim, micro) inside an embedded PTY pane | Needs a terminal emulator (ADR-004), which is experimental | Possible later, depends on ADR-004 |
| **`$EDITOR` hand-off** (`tea.ExecProcess`) | Full power of the user's editor; zero editor code; works on every platform Bubble Tea supports | **Chosen for M2a** |
| **Own small editor model** | Buffer interface + undo stack + insert mode, rendered through the same viewer; scope capped (§below) | **Chosen for M2b** |

**Decision.** Hybrid.
- **Viewer** (M2a): `bubbles/viewport` (soft-wrap off, left gutter for line
  numbers, highlight ranges) + Chroma v2 tokens converted to Lip Gloss styles
  for the visible region only.
- **Edit by hand-off** (M2a): `e` opens `$VISUAL`/`$EDITOR` at the line; on
  return the buffer reloads.
- **Built-in editing** (M2b): own model — line-slice buffer behind an
  interface (a rope can replace it later), undo/redo of edit operations,
  insert/normal mode, `ctrl+s` safe save, in-buffer search. Explicitly not:
  multi-cursor, Vim emulation, completion, formatting.

**Rationale.** The hand-off gives real editing in M2a at almost no cost and
de-risks the product question Q1. A small own model is predictable to
maintain; adopting an abandoned or internal component is not.

**Chroma.** `github.com/alecthomas/chroma/v2` v2.27.0 (2026-06-17), MIT;
formatters `terminal`, `terminal16`, `terminal256`, `terminal16m`; Go lexers
present — verified. **Do not use v3**: `master` is `chroma/v3` at
`v3.0.0-alpha.5`; README on `master` may not match v2.

---

## ADR-004 — PTY and terminal implementation

**Context.** Needs: command output streaming (tests, builds), interactive
shells, resize, signals, cleanup, multiple sessions, and no coupling to agent
runtime sessions.

**Findings (verified).**
- `creack/pty` v1.1.24 (2024-10-31): Unix only; on Windows `StartWithSize`
  returns `ErrUnsupported`. Resize: `Setsize`, `InheritSize`, `Getsize`.
- Windows-capable PTY: `charmbracelet/x/xpty` v0.1.4 (creack/pty on Unix,
  `x/conpty` on Windows; experimental `x` repository) and
  `aymanbagabas/go-pty` v0.2.3 (Unix PTY + ConPTY, MIT, active).
- Embeddable emulators: `charmbracelet/x/vt` v0.1.0 (first tag 2026-10-12;
  "experimental … no promises of backwards compatibility"; API has `Write`,
  `Resize`, `CellAt`, `Draw`, damage tracking, scrollback, mouse);
  `taigrr/bubbleterm` v0.3.5 (Bubble Tea v2 component over `x/vt` +
  `creack/pty`; single maintainer); `hinshun/vt10x` unmaintained since 2022;
  tcell v3's `vt` package is "a work in progress".

**Decision.**
1. **Command sessions use pipes, not a PTY** (M3a). Output is captured
   line-by-line; programs that detect "not a TTY" behave like in CI, which is
   what tests and builds should do.
2. **Interactive shells use the hand-off** (M3a): `tea.ExecProcess` runs
   `$SHELL` in the selected workspace; exit returns to Studio. Works on all
   platforms with no PTY code.
3. **Embedded PTY panes** (M3b) only after spike EX-07 passes these criteria:
   `vim`, `less`, `htop` and the user's shell render correctly; resize
   propagates; 10 MB of output stays responsive; no orphan after closing;
   behaviour on **Linux and Windows** identical. Candidate stack: `aymanbagabas/go-pty`
   (or `x/xpty`) + `charmbracelet/x/vt`. `creack/pty` alone is excluded: it has
   no Windows support in any release, and Windows is a target. If the spike fails, M3b is dropped.
4. **Process groups everywhere**: `Setpgid` on Unix, kill the group with
   SIGTERM then SIGKILL after 3 s. Windows: a job object with
   `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` (required, backlog W-03).

**Consequences.** M3a ships without experimental dependencies. Agent sessions
are never PTY sessions of Studio; they are reached through the runtime
(ADR-005).

---

## ADR-005 — Runtime-specific features

**Context.** Attach and live output are runtime features. Studio must stay
runtime-independent, and the `runtime.Runtime` contract should not change.

**Decision.** Optional interfaces discovered by type assertion, e.g.
`runtime.Attacher{ AttachCommand(agent string) ([]string, error) }`, exposed
through `service` (`ErrUnsupported` when absent). Live output uses the
existing `ReadAgentOutput`. The Herdr adapter implements `Attacher` with
`herdr [--session S] agent attach NAME`; the CLI's `attachHint` reuses it.

**Rejected.** Adding methods to `Runtime` (forces every adapter to implement
UI features); Studio calling `herdr` directly (couples Studio to Herdr).

---

## ADR-006 — LSP architecture

**Context.** Diagnostics, definition, references, symbols; Go (`gopls`)
first; several languages and workspace roots later.

**Findings (verified).**
- `golang.org/x/tools/gopls/internal/protocol` is not importable (internal).
- `go.lsp.dev/protocol` v1.0.1 (2026-06-27), LSP 3.18 types generated from the
  meta-model; revived after a 2022–2026 gap; **requires Go 1.26**; its go.mod
  lists ~200 indirect requirements via `tool` directives (effect on consumers
  **unverified**).
- `github.com/sourcegraph/jsonrpc2` v0.2.3 (2026-09-10), MIT, maintained.
- `charmbracelet/x/powernap` v0.1.6: LSP client utilities on sourcegraph
  jsonrpc2; experimental repository; production use **unverified**.

**Decision.** A thin own client in `internal/lsp`:
- transport and framing: `sourcegraph/jsonrpc2` over the server's stdio;
- protocol types: hand-written structs for the ~12 messages used
  (`initialize`, `initialized`, `shutdown`, `exit`, `didOpen`, `didChange`
  (full sync), `didClose`, `publishDiagnostics`, `definition`, `references`,
  `documentSymbol`, `window/logMessage`);
- one server per (language, workspace root), LRU cap.

**Investigate (CI-00 in backlog).** Whether `go.lsp.dev/protocol` adds an
acceptable module graph and whether raising the project to Go 1.26 is
acceptable; if both, use it instead of hand-written types.

**Rationale.** The feature set is small and stable; a dozen structs are cheaper
to own than a large generated package with toolchain requirements.

**Outcome of CI-00 (2026-11-19).** Go version is not the obstacle (the module
is on Go 1.27), but `go.lsp.dev/protocol` would bring a large module graph for
a dozen messages, so it is not used. `sourcegraph/jsonrpc2` is not needed
either: LSP framing (`Content-Length` header + JSON-RPC 2.0) is a small
amount of standard-library code. `internal/lsp` therefore has no
dependencies: own framing, own types for the messages listed above, results
decoded leniently (`Location`, `Location[]` and `LocationLink[]` for
definition; `DocumentSymbol[]` or `SymbolInformation[]` for symbols).

---

## ADR-007 — DAP architecture

**Findings (verified).** `github.com/google/go-dap` v0.12.0 (2024-02-22) is
the latest tag (master has later schema syncs without tags); Delve v1.27.2
pins the same v0.12.0. `dlv dap` starts a single-use DAP server; `launch`
supports `mode: "test"` (and `debug`, `exec`, `replay`, `core`); `attach`
`local` works with `dlv dap`.

**Decision.** `internal/dap` uses `go-dap` v0.12.0 message types and codec,
starts `dlv dap --listen 127.0.0.1:0` as a session process (process group,
cleanup as ADR-004), connects over TCP, and launches with `mode: test` or
`debug` in the selected workspace. The adapter is configured, not hard-coded,
so other DAP adapters can be added.

**Investigate.** How the chosen address is read back from `dlv dap` output
reliably, or whether `--client-addr` (Delve connecting to Studio) is
preferable.

**Outcome (2026-11-19).** `--client-addr`: Studio listens on `127.0.0.1:0`
and `dlv dap --client-addr <addr>` connects to it, so no output is parsed.
`go-dap` is not used: as for LSP (ADR-006), the framing is shared
(`internal/frame`) and `internal/dap` has hand-written types for the dozen
requests it sends. Tests run against a fake adapter (`internal/dap/daptest`);
a real-Delve test runs with `CAFFE_DAP_REAL=1`.

---

## ADR-008 — Worktree selection model

**Context.** The developer must always know which worktree is shown or edited;
changes from different worktrees must never mix; agent writes and human edits
must not interleave ([audit.md](audit.md) W7).

**Decision.**
- **One selected workspace at a time** (main checkout or one task worktree),
  shown in header and status bar. Buffers belong to a workspace; opening a
  path from another workspace asks before switching.
- **Cross-process lease** table `worktree_leases`: agent or human holder,
  compare-and-swap acquire, heartbeat, expiry. Human writes need the lease;
  the engine takes an agent lease before developer writes and waits
  (`blocked`) while a human holds it.
- **Release requires a clean worktree** (commit as `manual:` or discard), so
  the engine's `collectChanges` never commits human work.
- The main checkout is never leased and never written by the engine.

**Rejected.** Advisory file locks (`flock`): not visible in the database,
platform differences, no reason recorded. Copying files between worktrees:
violates "never combine changes".

---

## ADR-009 — Git integration

**Findings (verified).** go-git v5.19.3 cannot create linked worktrees
(`worktree` ❌ in COMPATIBILITY.md); v6 is `v6.0.0-beta.1` with partial,
experimental (`x/`) worktree support; merges are fast-forward only; no rebase
or stash.

**Decision.** Keep the Git CLI through `internal/git` (argument vectors, no
shell, `GIT_TERMINAL_PROMPT=0`), extended with read helpers Studio needs:
`ls-files --cached --others --exclude-standard` (explorer, honours
`.gitignore`), `status --porcelain=v2`, `diff` per file, `log`, `blame`
(later), `merge-tree --write-tree` (integration preview, M4).
**Investigate:** minimum Git version for `merge-tree --write-tree` (newer
Git feature).

---

## ADR-010 — Run hosting

**Decision.** Studio starts runs as detached `caffe run --run-id <uuid>`
processes (own session/process group, output to
`.caffe/artifacts/<run>/console.log`) and waits until the run row exists.
**Rejected:** hosting `Orchestrator.Run` inside Studio (run dies with Studio),
a daemon (new long-running component). **Required on Windows (W-04):** verify detach flags
and console behaviour.

---

## ADR-011 — File watching and search

**Findings (verified).** fsnotify v1.10.1: no recursive watching; inotify
watch limits on Linux; kqueue needs one fd per file on macOS; no events on
network/virtual filesystems. ripgrep 15.2.0: `--json` documented as JSON Lines
(`begin`, `end`, `match`, `context`, `summary`), with no explicit stability
promise; message types "may expand".

**Decision.**
- fsnotify watches only directories of open buffers and the expanded explorer
  directories; refresh on focus as a fallback. Never a recursive watch.
- Content search via `rg --json` when `rg` is on `PATH`, parsed leniently
  (unknown types ignored, `bytes` fields decoded); fallback `git grep -n`.
- File search over `git ls-files` output, fuzzy-matched in memory.
