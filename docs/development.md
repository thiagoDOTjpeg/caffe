# Development

## Build and check

```sh
make build     # bin/caffe
make check     # go vet + go test -race + gofmt check — must pass before a change is done
make test      # go test ./... (no race detector)
make e2e       # real Herdr, real agents; opt-in, see below
```

Go 1.27 or newer (the `go` line of `go.mod`). No cgo: SQLite is `modernc.org/sqlite`.

Dependencies: `cobra` (CLI), `zerolog` (logs), `google/uuid`, `yaml.v3`
(config), `modernc.org/sqlite`. Tests use only the standard `testing` package.

## Tests

No test in `make check` needs Herdr, a network or an agent.

| Package | What is covered | How |
|---|---|---|
| `contracts` | report validation per role; reports cannot carry commands | pure |
| `config` | defaults round-trip, the documented example loads, every invalid setting is an error | temp files |
| `persistence` | round trip across reopen, compare-and-swap updates, foreign keys, stale-run detection, backup | real SQLite in a temp dir |
| `git` | worktree create/list/commit/diff/remove, collisions, path escapes, isolation, dirty worktrees are not removed | real `git` in a temp repo |
| `runtime/herdr` | argv passed intact and unsplit (including hostile prompts), JSON envelopes, error mapping, blocked start, timeout, cancellation, missing executable, redaction | a **fake `herdr` executable** (shell script) that records its argv |
| `agents` | report delivery and cleanup, idle ≠ done, reminder, brief idleness not nudged, blocked at startup, uncertain prompt not resent, cancellation, re-attach, symlinked report refused, prompt rendering for every role | `runtime.Fake` |
| `planner` | valid plan, 16 rejection cases, planning through the runtime, context collection | `runtime.Fake` |
| `core` (scheduler) | transition table, cycle detection, dependency order, concurrency bound, retries, permanent failures, cascading cancellation, timeouts, panics, no duplicate dispatch across two schedulers | real SQLite |
| `core` (orchestrator) | full run with planner, review correction loop, rejected review, real validation exit codes, concurrent isolated developers, stacked developers, developer without changes, invalid plan/report, unavailable kind, cross-process cancel, **restart without loss or duplication**, vanished agent on resume, cancel of an interrupted run, clean | real SQLite + real Git + `runtime.Fake` |
| `app` | `init`, idempotency, config never overwritten, commands from a subdirectory, invalid config stops every command | in-process CLI |

`runtime.Fake` (`internal/runtime/fake.go`) implements `Runtime` in memory. Its
`OnPrompt` hook plays the agent: tests read the prompt file the agent was
pointed at and write a report (and, for developers, files). It tracks the peak
number of concurrently working agents.

The crash tests build the exact database and Git state a killed orchestrator
leaves behind (`env.crash` in `core/orchestrator_test.go`) rather than killing
a goroutine, then run `Recover` and `Resume` against it.

Run one package with the race detector:

```sh
go test -race -count=1 ./internal/core/
```

## End-to-end test

`tests/integration/e2e_test.go` drives the real CLI against a real Herdr: it
creates a Git repository, initialises Caffè, runs a three-task plan (research →
create a file → validate) and checks the branch content, the untouched main
checkout, the order of state transitions and the recorded exit codes.

It is skipped unless `CAFFE_E2E=1`, because it spends model tokens and needs a
human once: a new repository is not trusted by any agent, and Caffè does not
answer trust dialogs. Run it with `-v` (the Makefile does), and when the log
prints `agent … is waiting for a human — attach with: …`, run that command and
accept the dialog.

```sh
herdr --session caffe-e2e server &        # isolated headless session
CAFFE_E2E_SESSION=caffe-e2e CAFFE_E2E_RESEARCHER=agy make e2e
herdr session stop caffe-e2e
```

`CAFFE_E2E_RESEARCHER` / `CAFFE_E2E_DEVELOPER` choose the agent kinds (default
`claude`), which is how two different kinds are exercised in one run. The
kinds must be installed and signed in.

## Testing on Windows

Windows is a target. Without Go on the Windows machine:

```sh
make windows-kit      # dist/caffe-windows-kit.zip, built on Linux
```

Unzip it on Windows and follow `LEIA-ME.md` inside: `run-tests.ps1` runs
every test package (needs Git for Windows) and writes `results.txt`; the rest
are manual checks (TUI spike, a real run through Herdr, detached runs). Test
binaries are built without `-race`, which needs cgo on Windows.

## Conventions

- Every external process is started with an argument vector. No `sh -c`.
- Every I/O function takes a `context.Context`. Cleanup that must happen
  *because* a context was cancelled uses `context.WithoutCancel`.
- Task status changes go through `core.transition` (or
  `Scheduler.Transition`), never a direct store update.
- Handlers read a task's identity fields (`ID`, `Key`, `Role`,
  `Dependencies`) directly; anything that changes during a run is read through
  `Scheduler.Snapshot`.
- With one SQLite connection, never start a query while iterating another
  one's rows: collect, close, then query.
- Agent and planner output is parsed into fixed structs and validated before
  use. Do not add a field whose value gets executed.
- Comments say why. A deliberate shortcut is marked `ponytail:` with its
  ceiling and the upgrade path.

## Adding things

**A role prompt change**: edit `prompts/<role>.md`. Templates are parsed with
`missingkey=error`; `TestRenderEveryRole` renders all of them.

**A database change**: add `internal/persistence/migrations/00N_name.sql`.
Files are applied in name order, each in a transaction; never edit an applied
one.

**Another runtime adapter** (e.g. the Herdr socket API): implement
`runtime.Runtime`, map failures to the sentinel errors in `runtime.go`, and
select it in `internal/app/app.go`. `core`, `agents` and `planner` need no
change. See [runtime-integration.md](runtime-integration.md#socket-api-not-implemented).

**Another planner backend** (a direct LLM API): implement
`planner.LLMProvider` and return plans through `planner.ParsePlan` so the same
validation applies.

## Where things are at runtime

```
<project>/.caffe/
  config.yaml
  caffe.db (+ -wal, -shm)
  caffe.log                         JSON lines
  backups/caffe-<ts>.db     one per run, newest 5
  worktrees/<run8>/<task>/         developer worktrees
  artifacts/<run-id>/
    plan.json  report.md
    planner/attempt-N/{prompt.md,report.json,output.txt}
    <task>/<exec8>/{prompt.md,report.json,output.txt}
    <task>/changes.diff
    <task>/attempt-N/{command-N.log,report.json}     validation commands
```

`.caffe/` and `.caffe-report-*.json` are listed in the repository's
`.git/info/exclude` by `caffe init`; the tracked `.gitignore` is not touched.
