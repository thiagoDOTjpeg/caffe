# Runtime adapters

Caffè orchestrates; it does not run agents. Starting agent processes, managing
their terminals and detecting their state is the job of an external agent
runtime, reached through the `runtime.Runtime` interface.

One adapter exists: **Herdr**, driven through its CLI. Herdr is a separate
project and not part of Caffè; this document describes how the adapter uses it.

Everything below was verified against the installed binary, **herdr 0.9.5**
(protocol 22), by running the commands, and against the official docs
(agent-automation, cli-reference, socket-api). Nothing is assumed from older
versions.

## The interface

`internal/runtime/runtime.go`:

```go
type Runtime interface {
    Capabilities(ctx) (*Capabilities, error)
    CreateWorkspace(ctx, cwd, label string) (*Workspace, error)
    StartAgent(ctx, kind, paneID, name string, args []string) (*AgentSession, error)
    SendPrompt(ctx, agent, prompt string) error
    GetAgentStatus(ctx, agent string) (AgentStatus, error)
    ReadAgentOutput(ctx, agent string) (string, error)
    StopAgent(ctx, workspaceID string) error
}
```

Optional capabilities are separate interfaces that an adapter may also
implement, discovered with a type assertion, so `Runtime` itself never grows
features some runtimes cannot offer:

```go
type Attacher interface {
    AttachCommand(agent string) ([]string, error) // argv that attaches a human's terminal to the agent
}
```

The Herdr adapter implements `Attacher` with `herdr [--session S] agent attach
NAME`; the "attach with: …" hint for blocked agents comes from it.

It differs from a generic "agent runtime" interface where Herdr's real
semantics differ:

- **No `CreatePane`.** `workspace create` returns a root pane already at a
  shell prompt in the requested directory, which is exactly what `agent start`
  needs. Caffè uses one workspace per execution.
- **`StopAgent` takes a workspace id.** Herdr has no "stop agent" command. An
  agent ends when its pane closes; closing the workspace does that (verified:
  `agent get` then returns `agent_not_found`).
- **`StartAgent` can return a session *and* `ErrNotReady`.** See below.
- **`SendPrompt` can return `ErrUncertain`.** See below.

Errors are sentinels (`ErrNotFound`, `ErrNotReady`, `ErrBlocked`,
`ErrUncertain`, `ErrUnavailable`) wrapping a structured `herdr.Error` with the
operation, Herdr's error code and message, and the process exit code.

## Command mapping

| Runtime call | Herdr command |
|---|---|
| `Capabilities` | `herdr --version`; `herdr workspace list` (is a server reachable?); `herdr agent start --help` (supported kinds) |
| `CreateWorkspace` | `herdr workspace create --cwd PATH --label NAME --no-focus` → `.result.workspace.workspace_id`, `.result.root_pane.pane_id` |
| `StartAgent` | `herdr agent start NAME --kind KIND --pane ID --timeout 45000 [-- ARGS…]` → `.result.agent` |
| `SendPrompt` | `herdr agent prompt NAME TEXT` |
| `GetAgentStatus` | `herdr agent get NAME` → `.result.agent.agent_status` |
| `ReadAgentOutput` | `herdr agent read NAME --source recent-unwrapped` (plain text on stdout) |
| `StopAgent` | `herdr workspace close ID` (`workspace_not_found` is treated as success) |

With `runtime.session` set, every call is prefixed with `--session NAME`.

Agents are addressed by **name**, which Caffè generates per execution:
`c<8 hex of the execution id>-<role>`, e.g. `c0112dd6a-developer`. That
satisfies Herdr's `[a-z][a-z0-9_-]{0,31}` and is unique among live agents.
Pane and workspace ids are stored too, but ids are reused after a server
restart, so a name lookup always precedes closing a workspace.

`--no-focus` keeps new workspaces from stealing focus in an attached UI.

## Output format

These commands print a JSON envelope `{"id":…,"result":{…}}` on success and
`{"id":…,"error":{"code":…,"message":…}}` on failure — on stdout or stderr,
with exit code 1 (the adapter also handles an error envelope with exit 0).
`--version`, `agent start --help` and `agent read` print text.

There is no `--json` flag on workspace/pane/agent commands in 0.9.5; JSON is
simply what they print. Field-level shapes are not in the docs; they were
taken from real output and from `herdr api schema --json`.

Error codes the adapter maps:

| Herdr code | Sentinel |
|---|---|
| `agent_not_found`, `workspace_not_found`, `pane_not_found`, `not_found`, `agent_not_running` | `ErrNotFound` |
| `agent_not_ready` | `ErrNotReady` |
| `agent_blocked` | `ErrBlocked` |
| `agent_prompt_stalled`, `timeout`, or our own command timeout | `ErrUncertain` |
| `server_not_running`, executable missing | `ErrUnavailable` |

Anything else is returned as a plain `herdr.Error`.

## Capability discovery

Caffè does not assume which agent kinds exist. `Capabilities` reads the
`possible values` of `--kind` from `herdr agent start --help` (the binary's own
list; 24 kinds in 0.9.5) and keeps those whose executable is on `PATH`, since
Herdr launches an agent by its canonical executable name. If the help output
has no such list, the adapter reports an unsupported Herdr version instead of
guessing.

Before a run starts and before planning, every configured kind that will be
needed is checked against that list; a mismatch fails the run with the
available kinds in the message, before any agent is started. `caffe agents`
shows the same information.

What discovery cannot tell: whether an agent is **signed in**. See below.

## Semantics that matter

**Idle is not done.** `agent_status` is `idle | working | blocked | done |
unknown`. `idle` and `done` both mean "accepts input". Caffè never treats
either as task completion; only the report file counts
(see [architecture](architecture.md#what-done-means)). Observed with a real
agent: it backgrounded a command and was `idle` for several seconds mid-task.

**Blocked at startup.** A new folder makes Claude Code and Antigravity show a
trust dialog. `agent start` then fails with `agent_not_ready`, but the agent
exists and is `blocked`. The adapter confirms that with `agent get` and returns
the session together with `ErrNotReady`. The agent manager waits until the
agent accepts input; the task shows as `blocked` with the attach command.
Caffè never sends keys to answer a dialog.

**Blocked mid-task.** A permission prompt turns the status to `blocked`. The
task follows (`running → blocked → running`). `agent prompt` to a blocked agent
is rejected by Herdr with `agent_blocked` and sends nothing.

**A login screen is `idle`.** Verified with a signed-out Codex: Herdr reports
`idle` and `interactive_ready`, so the prompt is typed into the login screen.
Caffè cannot tell the difference up front. The execution ends with *agent
stopped without writing its report* after the idle thresholds, and the error
names the file holding the agent's last screen.

**Timeouts prove nothing.** If `agent prompt` times out or stalls, the text may
have been delivered. Before resending, the manager checks whether the agent is
`working`, and whether the prompt (which names a unique report file) is on its
screen. It resends at most once.

**Prompts are short.** The full prompt is a file; the agent is sent one line
pointing at it. This avoids terminal input limits, keeps prompt content out of
`ps` and logs, and makes "was it delivered?" checkable.

**Trust is per repository for Claude Code.** After the dialog was accepted in
one worktree of a repository, other worktrees of it and the root did not ask
again.

## Process handling

`internal/runtime/herdr/cli.go` runs every command with `os/exec`:

- argument vector only, never a shell;
- stdout and stderr captured separately; exit code kept;
- `runtime.command_timeout` per call (agent start: Herdr's own 45 s wait plus
  10 s), and the caller's context for cancellation; the process is killed on
  either, with a 2 s grace for its pipes;
- each call logged at debug level with operation, duration, exit code and
  Herdr error code. Prompt arguments are logged as a byte count. Captured
  stderr is redacted before it is put in an error or a log.

## Sessions

By default agents are created in Herdr's default session, next to your own
workspaces, without taking focus. To isolate them:

```sh
herdr --session caffe server      # headless server for a named session
```

and set `runtime.session: caffe`. Attach to an agent with
`herdr --session caffe agent attach <name>`; Caffè prints the exact command
when an agent is blocked. Stop the session with `herdr session stop caffe`.

A server must already be running: Caffè does not start one. If it is not,
commands fail with `ErrUnavailable` and Herdr's own hint.

## Socket API (not implemented)

The CLI talks to the Herdr server over a Unix socket
(`~/.config/herdr/herdr.sock`, or `sessions/<name>/herdr.sock`) using
newline-delimited JSON. The same operations exist as methods
(`workspace.create`, `agent.start`, `agent.prompt`, `agent.get`, `agent.read`,
`workspace.close`), plus `events.subscribe` and a server-side `agent.wait`.

A socket adapter would implement the same `runtime.Runtime` interface and
could replace polling with `pane.agent_status_changed` events. It was left out
because the CLI adapter covers everything the orchestrator needs, and because
status events would not change the completion rule: the report file still has
to be checked.

## Herdr features deliberately not used

- `herdr worktree create/open/remove`: Herdr can manage worktrees and open
  them as workspaces. Caffè uses plain `git worktree` instead, because it
  needs control over branch names, base commits, collision checks and removal
  rules, and needs worktrees to exist independently of whether a workspace is
  open.
- `agent prompt --wait` / `agent wait`: they wait for a status, and a status is
  not completion.
- `pane run`, `pane send-text`, `pane send-keys`: Caffè only talks to agents
  through `agent prompt`.
