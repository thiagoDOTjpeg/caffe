# Architecture

A modular monolith: one process, one SQLite file, Herdr as the only external
runtime. This document describes what is implemented.

## Modules

```
cmd/caffe            entry point, signal handling
internal/app        wiring + Cobra commands. No orchestration logic.
internal/config     .caffe/config.yaml: defaults, strict loading, validation
internal/contracts  domain entities, plan validation, report validation
internal/core       task graph, state machine, scheduler, role policies,
                    recovery, consolidated report
internal/planner    LLMProvider interface, agent-backed provider, plan parsing,
                    project context collection
internal/agents     one agent execution end to end; prompt rendering
internal/runtime    Runtime interface, in-memory Fake, secret redaction
internal/runtime/herdr   Herdr CLI adapter
internal/git        worktrees, diffs, safe cleanup
internal/persistence     SQLite store and migrations
prompts             role prompt templates (embedded)
tests/integration   end-to-end test against a real Herdr (opt-in)
```

Dependencies point inward: `core` knows `runtime.Runtime`, never Herdr.
`contracts` depends on nothing.

Deviations from the originally proposed tree, and why:

- `persistence` has one `repositories.go` instead of a file per entity: they
  share transactions and helpers, and no caller needs them apart.
- No `agents/registry.go`, `agents/profile.go` or `agents/roles/*.go`. A role
  is a prompt template (`prompts/<role>.md`) plus a policy function in
  `core/execution_manager.go`. A profile is two config fields (kind, args).
  There was nothing left for those files to hold.
- `runtime.Runtime` has no `CreatePane`: a Herdr workspace is created with a
  root pane, and Caffè uses one workspace per execution.
- `runtime/herdr/parser.go` and `errors.go` are folded into `cli.go` and
  `adapter.go`: Herdr prints JSON, so "parsing" is `json.Unmarshal`.

## Domain model

`Run` → `Task`s (a DAG via `task_dependencies`) → `Execution`s (one per
attempt, per correction, per validation command). `Agent` is a runtime session
that performed an execution. `Artifact` points to a file. `Event` is appended
for every state transition and notable fact.

A task has an `ID` (UUID) and a `Key` (the id from the plan, e.g. `impl`). Keys
are restricted to `[a-z0-9][a-z0-9_-]{0,39}` because they become branch and
directory names; they are validated, not escaped.

## Task lifecycle

```
pending ─► ready ─► assigned ─► running ─► completed ─► reviewing ─► completed
   │         │          │         │  ▲                      │
   │         │          │         ▼  │                      ├─► failed
   │         │          │       blocked                     └─► cancelled
   ▼         ▼          ▼         │
cancelled cancelled  failed ◄─────┴──────── running
                        │
                        └─► ready   (retry)
```

| Status | Meaning |
|---|---|
| `pending` | waiting for dependencies |
| `ready` | dependencies completed; queued behind the concurrency limit |
| `assigned` | claimed by a scheduler worker; the attempt counter was incremented |
| `running` | the agent session exists (or validation commands started) |
| `blocked` | the agent shows an approval or question dialog; a human must answer |
| `reviewing` | a completed developer task being corrected after a rejected review |
| `completed` / `failed` / `cancelled` | finished |

The table in `core/state_machine.go` is the single source of truth; any other
transition is rejected. Each transition is one SQLite transaction that updates
the row **only if it is still in the expected status** and inserts the event.
That compare-and-swap is what makes a task impossible to dispatch twice, also
across processes (`TestNoDuplicateDispatchAcrossSchedulers`).

Run statuses: `pending → planning → executing ⇄ reviewing ⇄ validating →
completed | failed | cancelled`, plus `interrupted` (owner died; resumable).
`reviewing`/`validating` reflect the most advanced role currently in flight.

## Scheduler

`core.Scheduler.Run` is a single loop:

1. promote: `pending → ready` where all dependencies are `completed`;
   `pending|ready → cancelled` where a dependency is `failed`/`cancelled`
   (cascading, with the reason);
2. claim ready tasks in plan order while fewer than
   `max_concurrent_agents` are in flight; each claim starts one goroutine;
3. wait for one result and settle it: `completed`, or `failed` followed by
   `ready` if attempts remain and the failure is not permanent.

Goroutines are bounded by the claim loop, not by a pool. The lock guarding the
in-memory graph is never held during I/O: a transition copies the task,
writes to the database, then swaps the copy in. Task timeouts
(`default_timeout`) are per attempt and count as failures. A panic in a task
handler is a task failure. Cancellation stops dispatching, lets running tasks
wind down (they stop their agents), and cancels what had not started.

## What "done" means

Herdr reports `idle`, `working`, `blocked`, `done`, `unknown`. None of them
means the task is finished: an idle agent may be waiting for a background
command, or sitting at a login screen.

So completion has one definition: **the agent wrote a well-formed JSON report
to the file it was told to write**. `agents.Manager.Execute`:

1. writes the full prompt to `<artifacts>/<task>/<execution>/prompt.md`;
2. creates a Herdr workspace in the working directory and starts the agent;
3. if the agent is blocked at startup (trust dialog), waits — it never answers;
4. submits a one-line prompt: *read that file, write your report to
   `<cwd>/.caffe-report-<id>.json`*. The prompt body never passes through
   argv, the terminal or the logs;
5. polls. A report on disk ends the execution (if the agent is still
   `working`, it gets a few more polls to finish writing). An agent idle
   without a report for `idle_nudge_after` is reminded once; idle for
   `idle_give_up_after` after that fails the execution. An agent that
   disappeared fails it, unless its report is already there;
6. saves the terminal output (redacted) and closes the workspace — on every
   outcome, including timeout and cancellation.

The report lives in the agent's working directory because sandboxed agents
cannot write elsewhere. It is moved to the artifact directory on delivery, and
`.caffe-report-*.json` is in `.git/info/exclude`.

A timed-out prompt submission is not assumed lost: the manager checks whether
the agent is working or the prompt is on its screen before sending again, and
sends at most twice.

## Role policies

| Role | Working directory | After the agent reports |
|---|---|---|
| researcher | project root | warn if `git status` of the checkout changed |
| developer | own worktree `.caffe/worktrees/<run>/<task>` on branch `<prefix>/<run>/<task>`, created from the base commit or from the branch of the developer task it depends on | commit leftovers, store the diff. **No diff = failure**, whatever the report says |
| reviewer | the worktree of the developer task it depends on (project root if none) | if rejected: correction round (below). Stray edits by the reviewer are discarded |
| validator | same worktree | with `validation.commands`: no agent; Caffè runs the commands and records exit codes |

A read/write lock per worktree keeps a correction from rewriting a tree that a
reviewer or validator is reading, and makes concurrent writers impossible.

**Review loop.** A reviewer report must contain `approved`. On rejection (with
at least one finding) the developer task goes `completed → reviewing`, a new
developer agent gets the findings in the same worktree, the diff is collected
again, the task returns to `completed`, and the reviewer runs again — up to
`max_review_rounds` corrections. Still rejected, or a failed correction, fails
the reviewer task permanently and cancels its dependents.

**Results flow downstream.** Each agent prompt includes the summary of every
direct dependency's latest report and the path to the full file, framed as
information from another agent, not instructions.

## Planner

`planner.LLMProvider.GeneratePlan` is the provider abstraction. The only
implementation, `AgentProvider`, runs the configured planner agent through the
same `agents.Manager`, read-only in the project root, and treats its report
file as the plan. No direct LLM API is used.

Plans are untrusted. `ParsePlan` rejects unknown JSON fields (a typo such as
`dependancies` must not silently produce an unordered graph) and
`ExecutionPlan.Validate` rejects: missing/duplicate/unsafe ids, invalid roles,
empty titles or descriptions, missing or blank acceptance criteria, unknown or
self dependencies, cycles, more than the task limit. An invalid plan is
retried `max_task_retries` times, then the run fails. Nothing in a plan is
ever executed as a command.

## Persistence

SQLite via `modernc.org/sqlite` (no cgo). WAL, foreign keys on,
`busy_timeout`, immediate transactions, and a pool of exactly one connection:
writes are serialised in-process, and other `caffe` processes are handled by
WAL. Timestamps are UTC text. Migrations are embedded SQL files applied in
order and tracked in `PRAGMA user_version`.

Rows hold metadata only. Prompts, reports, diffs, terminal output and command
logs are files under `.caffe/artifacts/<run-id>/`, referenced from `artifacts`.
`executions.output` keeps a redacted 2 KB tail.

Every run starts with `VACUUM INTO .caffe/backups/…` (newest five kept).

## Recovery

A run's owning process writes a heartbeat every 2 s. On startup every command
runs `Recover`: runs in a non-terminal state without a heartbeat for 15 s
become `interrupted`, and their running executions too. That is all it does —
establishing facts, not restarting anything.

`caffe resume <run>` claims the run with a compare-and-swap (two resumers cannot
both win) and reconciles:

| Found | Action |
|---|---|
| task finished | untouched |
| task `pending`/`ready` | untouched; scheduled normally |
| task `assigned`/`running`/`blocked` | re-queued without consuming an attempt. When dispatched, its interrupted execution is examined: **agent still alive in Herdr, or report on disk → re-attach** and wait for the report; nothing is prompted again. Otherwise the execution is recorded as `unknown` and a new one starts. |
| developer task `reviewing` | back to `completed`; the correction's agent is stopped; the reviewer re-reviews whatever is in the worktree (uncommitted work is committed first) |
| interrupted executions nothing will re-attach to | recorded as `unknown`, their agents stopped, so no orphan keeps writing |
| interrupted while planning | leftover planning agents stopped; planning runs again (it is read-only) |

Starting a new execution after an `unknown` one is safe because of where it
runs: read-only roles are repeatable, and a developer continues in its own
preserved worktree with a prompt that says a previous attempt was cut short
and to inspect `git status` first.

Agents are looked up **by name** before a workspace is closed, because Herdr
reuses workspace ids after a restart.

Known gap: a crash in the instant between Herdr creating a workspace and
Caffè recording it leaves an empty shell workspace behind. It is harmless and
visible in Herdr.

## Trust model

Agents are untrusted executors.

- **Commands**: every external process (`herdr`, `git`, validation commands)
  is started with an argument vector. Nothing goes through a shell.
  Validation commands run in their own process group, killed as a whole on
  timeout or cancellation and after the command exits, so nothing they spawn
  survives them: a process group on Unix, a kill-on-close job object on
  Windows. A killed command records no exit code, only "timed out" or
  "cancelled".
- **Agent output is data.** Reports are parsed into a fixed struct; unknown
  fields are dropped. No field of a report or plan is executed or used as an
  orchestration command. Self-reported test results are labelled "not
  verified" in the run report.
- **Success is checked, not believed**: a developer needs a real diff, a
  validator needs real exit codes (when commands are configured), a reviewer's
  approval must be explicit.
- **Paths**: task ids are validated slugs. `git.Manager` refuses to create,
  commit, reset or remove anything outside `git.worktree_directory`. Report
  files must be regular files (no symlinks) under 1 MB.
- **Dialogs**: trust, login and permission prompts are never answered.
- **Merging**: not implemented. Worktree removal never forces and never
  deletes branches.
- **Logs**: prompt text is never logged (lengths only). Stored output and error
  text pass through a pattern-based redaction of credential-looking strings.

What it is not: a sandbox. A read-only role is an instruction plus an
after-the-fact check. An agent runs with the permissions of its own CLI and of
your user. Use the agents' own sandbox/permission settings (`agents.<role>.args`)
if you need enforcement.

## Observability

- `.caffe/caffe.log`: JSON lines with `run_id`, `task_id`, `agent_id`,
  `execution_id`, `event_type`; `-v` mirrors them to stderr at debug level.
- `events` table: an append-only log of state changes, see [Events](#events).
- Scheduler dispatch logs carry `queue_size` and `concurrent_agents`.
- `caffe status` / `caffe tasks` say why each task is not moving: which
  dependencies it waits on, that it is queued behind the concurrency limit, or
  that its agent waits for a human.

## Events

Every event has `run_id`, and `task_id` / `agent_id` where they apply. The
type names are constants in `internal/contracts/events.go` and are part of the
persisted contract: never renamed or reused.

**The log is append-only.** Nothing deletes events, so the SQLite `rowid` only
grows; it is exposed as `Event.Seq`. Readers page with
`Store.EventsSince(seq, runID, limit)`, and `Store.RunSnapshot` returns the
`Seq` it reflects, so a reader continues from a snapshot without gaps or
duplicates. A snapshot is read in one transaction; use a store from
`persistence.OpenReadOnly` for that.

**Atomicity.** Run and task transitions, agent start/stop, execution start and
artifacts are written in the same transaction as the row they describe. The
other events are written right after the state change, so a crash can lose
one; the tables remain the source of truth.

| Type | Written | Payload keys |
|---|---|---|
| `run.created` | with the run row | `status` |
| `run.<status>` (`planning`, `executing`, `reviewing`, `validating`, `completed`, `failed`, `cancelled`) | with the run update | `from`, `to`, `error` |
| `run.interrupted` | when a stale run is detected | `reason` |
| `task.created` | with the task rows | `key`, `role` |
| `task.<status>` | with the task update | `key`, `from`, `to`, `attempts`, `reason` (`from` = `to` when only fields changed, e.g. the worktree was recorded) |
| `agent.started` | agent row becomes `running` | `name`, `kind`, `role`, `workspace_id`, `pane_id` |
| `agent.stopped` | agent row becomes `stopped` (`task_id` = the task it last worked on) | `name`, `kind`, `role`, `workspace_id`, `pane_id` |
| `agent.blocked` | the agent starts or stops waiting for a human | `blocked` (bool), `agent` |
| `execution.started` | first save of an execution (agent and validation-command executions) | `execution_id`, `attempt`, `status` |
| `execution.<status>` (`completed`, `failed`, `cancelled`) | an agent execution ends | `execution_id`, `role`, `kind`, `attempt`, `reattached`, `duration_ms`, `error` |
| `execution.unknown` | recovery cannot establish an interrupted execution's outcome | `execution_id`, `reason` |
| `validation.command` | a validation command ends | `execution_id`, `command`, `exit_code`, `duration_ms` |
| `artifact.added` | with the artifact row | `artifact_id`, `type`, `path`, `description` |
| `worktree.created` | a developer worktree is created | `path`, `branch`, `base` |
| `worktree.removed` | `caffe clean` removes a worktree | `path`, `branch` |
| `diff.collected` | a developer task's diff is stored | `path`, `base`, `files` |
| `warning` | something a human should see (read-only rule broken, backup failed, planning retry) | `message` |
