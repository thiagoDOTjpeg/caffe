<p align="center"><img src="docs/assets/caffe.svg" alt="Caffè — Brew better software." width="480"></p>

# Caffè

### Brew better software.

A local-first orchestrator for autonomous coding agents.

Caffè coordinates specialized coding agents, manages their execution lifecycle, and turns complex software tasks into structured, validated workflows.

Caffè is an orchestrator, not an agent and not an LLM runtime. The agents are
the coding CLIs you already use; they run inside an external agent runtime that
Caffè drives through an adapter. Caffè decides what they do, in which order,
where, and whether the result counts.

## The problem

One coding agent in one terminal does one thing at a time, and you are the
scheduler: you split the work, start each agent, wait, copy results from one to
the next, check what they claim, and remember where everything was when your
laptop restarts. Running several agents by hand adds new problems — they edit
the same files, and "done" means whatever each one says it means.

Caffè takes over that coordination and keeps the decisions that matter with
you: it never merges anything.

## Core capabilities

- **Planning.** A request in natural language becomes a validated task graph
  (tasks, roles, dependencies, acceptance criteria).
- **Dependency-aware scheduling.** A task starts when its dependencies
  completed; independent tasks run concurrently, up to a configurable limit.
- **Specialized roles.** Researcher, developer, reviewer and validator, each
  with its own prompt, constraints and success check.
- **Isolation.** Every developer task gets its own Git worktree and branch.
- **Results flow downstream.** Each agent receives the reports of the tasks it
  depends on.
- **Review loop.** A rejected review sends the findings back to a developer in
  the same worktree, then reviews again.
- **Validation with real exit codes.** Caffè runs your validation commands
  itself and records what they returned.
- **Verified, not believed.** Completion is a report file, a developer needs a
  real diff, and self-reported test results are labelled as unverified.
- **Persistence and recovery.** Everything is in SQLite. If Caffè is killed,
  the run is flagged interrupted and can be resumed without repeating finished
  work or duplicating running work.
- **A consolidated report** per run, plus the event log behind it.

## Architecture

A modular monolith in Go: one process, one SQLite file, no services.

```
cmd/caffe                 entry point
internal/app              wiring and CLI commands
internal/config           .caffe/config.yaml, strictly validated
internal/contracts        domain entities, plan and report validation
internal/core             task graph, state machine, scheduler, execution
                          manager (role policies), recovery manager, report
internal/planner          planning provider abstraction and plan parsing
internal/agents           one agent execution end to end; prompt rendering
internal/runtime          runtime adapter interface, in-memory fake
internal/runtime/herdr    the Herdr adapter
internal/git              worktrees, diffs, safe cleanup
internal/persistence      SQLite store and migrations
prompts                   role prompt templates
```

The core depends only on the `runtime.Runtime` interface, never on a specific
runtime. Details: [docs/architecture.md](docs/architecture.md).

## How orchestration works

1. **Plan.** The planner agent reads the project (read-only) and writes a plan
   as JSON. Caffè validates it: ids, roles, dependencies, cycles, acceptance
   criteria. An invalid plan is retried, then the run fails. `--plan` supplies
   a plan file instead.
2. **Schedule.** Tasks whose dependencies completed become ready and are
   claimed while fewer than `max_concurrent_agents` are in flight. Each status
   change is a compare-and-swap in the database, so a task cannot be dispatched
   twice.
3. **Execute.** For each task Caffè writes the prompt to a file, asks the
   runtime for a workspace in the right directory, starts the configured agent
   and tells it where its prompt is and where to write its JSON report. The
   report file is the only completion signal; an idle agent is not a finished
   one.
4. **Apply the role's policy.**

   | Role | Runs in | Counts as success when |
   |---|---|---|
   | researcher | project root, read-only | it reports |
   | developer | its own worktree and branch | it reports **and** the branch has a real diff |
   | reviewer | the worktree under review, read-only | it explicitly approves; a rejection triggers a correction round |
   | validator | the same worktree | every configured command exits 0 |

5. **Handle failure.** Failed tasks are retried up to `max_task_retries`. When
   a task fails for good, its dependents are cancelled with the reason and
   independent tasks continue.
6. **Report.** The run ends `completed`, `failed` or `cancelled`, with a
   Markdown report and one branch per developer task. Merging is up to you.

Task lifecycle: `pending → ready → assigned → running → completed`, with
`blocked` (the agent waits for a human), `failed`, `cancelled` and `reviewing`
(a completed change being corrected).

## Supported agent runtime integrations

| Runtime | Adapter | Status |
|---|---|---|
| [Herdr](https://herdr.dev) (CLI) | `internal/runtime/herdr` | implemented; developed against Herdr 0.9.5 |
| Herdr socket API | — | not implemented |

Herdr is a separate project. Caffè asks it to open workspaces, start agents,
deliver prompts and report agent state, and discovers at runtime which agent
kinds it can start; `caffe agents` shows the result. Any kind Herdr supports
whose executable is installed can be assigned to a role. End-to-end runs so far
used Claude Code and Antigravity (`agy`).

How the adapter works, and the runtime behaviours it has to account for:
[docs/runtime-integration.md](docs/runtime-integration.md).

## Installation

Download the archive for your system from the
[releases](https://github.com/thiagoDOTjpeg/caff-/releases) (Linux, macOS and
Windows, amd64 and arm64; `checksums.txt` has the SHA-256 of each), extract it
and put `caffe` on your `PATH`. `caffe --version` prints the version.

Or build from a clone:

```sh
make build            # produces bin/caffe
# or
go build -o bin/caffe ./cmd/caffe
```

Requirements:

- Go 1.27+ to build from source (no cgo) — what `go.mod` requires
- Git
- Herdr with a running server (`herdr status server`)
- at least one agent CLI that Herdr can start, installed **and signed in**

## Quick start

```sh
cd /path/to/your/repo           # root of a Git repository with at least one commit
caffe init                      # creates .caffe/ (config, database); nothing is committed
caffe agents                    # what the runtime can start, and whether your config matches
$EDITOR .caffe/config.yaml      # choose agent kinds; set validation.commands
caffe run "Implement JWT authentication"
```

From another terminal while it runs:

```sh
caffe status                    # active runs, each task's state and what it waits for
caffe inspect <run> --events    # the full report and event log
caffe cancel <run>              # stop it and close its agents
```

Before the first run, know that:

- **Agents ask whether they trust a folder.** Caffè never answers for you. The
  task goes to `blocked` and the CLI prints the command to attach to the
  agent's pane. Answer, and the run continues. The same holds for any
  permission prompt mid-task.
- **An agent that is not signed in looks idle.** The prompt lands on its login
  screen and nothing happens; the execution eventually fails with *agent
  stopped without writing its report* and the path of a file showing the
  agent's last screen. Start each agent once by hand first.
- **Agents run with the permissions you give them.** Caffè adds no flags. Use
  `agents.<role>.args` for a model or a permission mode.
- **Agents appear in your Herdr session**, one workspace per execution, closed
  when it ends. To keep them apart, run a dedicated headless session
  (`herdr --session caffe server`) and set `runtime.session: caffe`.

## CLI reference

| Command | What it does |
|---|---|
| `caffe init` | Create `.caffe/config.yaml` and the database. Idempotent; never overwrites config. |
| `caffe run "<request>" [--plan file.json] [--run-id <uuid>] [--detach]` | Plan and execute. `--plan` skips the planner; `--run-id` sets the run id (for launchers); `--detach` starts the run as a background process and returns its id at once. Ctrl-C cancels a foreground run. Exits 0 only if the run completed (or, with `--detach`, started). |
| `caffe status` | Active runs with task states and the reason each task is not moving. |
| `caffe runs` | All runs. |
| `caffe tasks <run>` | Task graph and statuses of a run. |
| `caffe inspect <run> [--events]` | Everything recorded: report, executions, exit codes, artifacts, events. |
| `caffe agents` | Runtime version, startable agent kinds, configured roles, live sessions of active runs. |
| `caffe cancel <run>` | Cancel a running or interrupted run and stop its agents. |
| `caffe resume <run>` | Continue an interrupted run. |
| `caffe clean [--failed]` | Remove worktrees of completed runs. Branches are kept. |
| `caffe config` | Print the effective, validated configuration. |
| `caffe integrate <run> [<task> approve\|reject [note] \| <task> merge [--yes]]` | List a run's branches with review, validation and decisions; record a decision; merge an approved branch into the current branch of the main checkout after a `git merge-tree` preview. Never pushes. |
| `caffe studio` | Open Caffè Studio, the terminal UI: follow runs live, start/cancel/resume them, attach to agents, review and edit worktrees, run commands, and integrate branches. See the [user guide](docs/studio/user-guide.md). |

Global flags: `-C <dir>` (the project directory or any directory below it) and
`-v` (debug logs to stderr). Run ids can be shortened to any unambiguous
prefix. Output is plain text without colours or decoration.

## Configuration reference

`.caffe/config.yaml`, created by `caffe init` with these defaults:

```yaml
project:
  name: my-project
  path: .
runtime:
  provider: herdr             # the runtime adapter
  executable: herdr
  session: ""                 # named Herdr session; empty = default session
  command_timeout: 1m         # per runtime CLI call
  poll_interval: 3s           # how often agent state is checked
  idle_nudge_after: 45s       # idle without a report this long → one reminder
  idle_give_up_after: 5m      # still idle this long after the reminder → execution fails
orchestration:
  max_concurrent_agents: 3
  max_task_retries: 2         # a task runs at most 3 times
  max_review_rounds: 2        # corrections after a rejected review
  default_timeout: 30m        # per task attempt
agents:                       # role → agent kind, plus extra CLI args for that agent
  planner:    { kind: claude }
  researcher: { kind: claude }
  developer:  { kind: claude }   # e.g. args: ["--model", "sonnet"]
  reviewer:   { kind: claude }
  validator:  { kind: claude }
validation:
  commands: []                # e.g. ["go vet ./...", "go test ./..."]
  timeout: 15m                # per command
git:
  worktree_directory: .caffe/worktrees
  branch_prefix: caffe        # branches are caffe/<run>/<task>
persistence:
  driver: sqlite
  path: .caffe/caffe.db
security:
  require_confirmation_for_merge: true   # caffe integrate … merge asks first (Studio always asks)
  preserve_failed_worktrees: true
studio:                       # Caffè Studio (docs/studio/user-guide.md)
  theme: caffe                # caffe | high-contrast | mono
  mouse: false                # reserved, not used yet
  reduce_motion: false        # reserved, not used yet
  avatars: true               # reserved, not used yet
  editor: ""                  # empty: $VISUAL, then $EDITOR
```

The file is read and validated by every command, so edits apply to the next
command. Unknown keys, wrong types and out-of-range values are errors, reported
together; nothing is silently ignored.

`validation.commands` decides how validator tasks work. **Set:** Caffè runs
each command in the task's worktree, without a shell (split on whitespace — put
pipes and globs in a script), and records the real exit code. **Empty:** a
validator agent finds and runs the checks, and its exit codes are reported as
unverified claims.

Raise `idle_nudge_after` and `idle_give_up_after` if your agents wait on long
builds: an agent that backgrounds a command is idle while it runs.

What a run leaves in `.caffe/`: the database, `caffe.log` (JSON lines), a
database backup per run, worktrees under `worktrees/<run>/<task>`, and under
`artifacts/<run-id>/` the plan, the report, and per task the prompt, the
agent's report, its terminal output, the diff and validation logs.

## Development

```sh
make check     # go vet + go test -race + gofmt check
make build
make e2e       # opt-in: real Herdr, real agents, costs tokens
```

No test in `make check` needs a runtime, a network or an agent: the Herdr
adapter is tested against a fake executable and everything above it against an
in-memory runtime. See [docs/development.md](docs/development.md).

CI (`.github/workflows/ci.yml`) runs the same checks on Linux and Windows for
every push and pull request.

### Releases

Versions come from the commit messages
([Conventional Commits](https://www.conventionalcommits.org/)), through
[release-please](https://github.com/googleapis/release-please):

1. Every push to `main` updates a release pull request with the next version
   and the changelog. Before 1.0, `feat:` bumps the minor version and
   `fix:` the patch; `feat!:` or a `BREAKING CHANGE:` footer is a breaking
   change.
2. Merging that pull request tags `vX.Y.Z`, publishes the GitHub release with
   `CHANGELOG.md`, and `.github/workflows/release.yml` attaches the binaries
   and `checksums.txt`.

Nothing else is needed: no manual tags, no version file to edit.

## Current limitations

- Merging is manual (`caffe integrate`, Studio's Integration screen) and
  never resolves conflicts: a conflicting branch is refused. A developer task
  that depends on another starts from that task's branch; with several
  developer dependencies it starts from the nearest one only.
- One runtime adapter (Herdr, through its CLI).
- Only rejected reviews trigger corrections. A failed validation ends the run.
- One orchestrator process per run. Several runs can coexist.
- Read-only roles are a policy stated in the prompt and checked afterwards, not
  a sandbox. See [the trust model](docs/architecture.md#trust-model).
- Sign-in state of an agent cannot be detected up front.
- `caffe config` is read-only; edit the YAML.
- Token usage is not recorded.

## Roadmap

Not commitments; the order reflects what is known to be missing.

1. Token accounting per execution, to see where a run spends.
2. A project memory file injected into every prompt, and reuse of research
   from earlier runs when the code did not change.
3. Corrections triggered by failed validation, not only by rejected reviews.
4. A Herdr socket API adapter, replacing polling with status events.

## Contributing

Caffè is an independent project. Contributions are welcome:

1. Open an issue describing the problem or the change before large work.
2. Keep the architecture: Go, SQLite, a modular monolith, runtime adapters
   behind `runtime.Runtime`, no new services and no dependency that the
   standard library can replace.
3. Add tests with the change, and make `make check` pass.
4. Update the documentation in the same change; it describes what is
   implemented, nothing else.
5. Follow the conventions in [docs/development.md](docs/development.md) and the
   naming and voice in [docs/brand.md](docs/brand.md).

No license has been chosen yet. Until a `LICENSE` file is added, the code is
not licensed for reuse; choosing one is a prerequisite for accepting outside
contributions.
