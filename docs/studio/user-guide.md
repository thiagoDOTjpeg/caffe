# Caffè Studio — user guide

Caffè Studio is a terminal UI for following Caffè runs, reviewing what the
agents produced, fixing it by hand when needed, and deciding what gets merged.

```sh
caffe studio            # in a project created with `caffe init`
```

Studio is a separate process: quitting it never stops a run, and runs started
from Studio keep going after it exits.

## Moving around

| Keys | What they do |
|---|---|
| `1`–`8` | Overview, Tasks, Agents, Runs, Workspace, Terminals, Integration, Debug |
| `?` | the keys of the current view |
| `:` or `ctrl+p` | command palette: every action, by name |
| `space` | leader menu (`space g …` go to, `space r …` runs, `space w …` workspace, `space v …` view, `space a …` agents) |
| `tab` | focus the bottom panel (events) and back |
| `esc` / `q` | close the overlay or view |
| `ctrl+w` | select the workspace: the main checkout or a task's worktree |
| `ctrl+q` | quit (asks while terminal sessions run; refused while buffers are unsaved) |

The header and the status bar always show the selected workspace.

## Following runs

- **Overview** (`1`): the selected run, its tasks, agents, health and recent
  events. `n` starts a run, `c` cancels it, `R` resumes an interrupted one,
  `r` opens its report.
- **Tasks** (`2`): a board by status (`b`), the dependency graph (`g`) or a
  list (`L`). `enter` opens the Execution Inspector of the task (prompt,
  report, output, artifacts, observed vs claimed exit codes); `d` its diff.
- **Agents** (`3`): one card per agent. `o` shows the live output of the
  focused agent, `a` attaches your terminal to it (answer a trust prompt,
  then detach as the runtime says), `i` inspects its execution.
- **Runs** (`4`): every run; `/` filters, `a` shows only active runs,
  `enter` selects one, `x` cleans worktrees of finished runs.

## Workspaces

**Workspace** (`5`) shows the selected workspace: an explorer on the left,
open files on the right.

- Explorer: `enter`/`l` opens a file or a directory, `h` goes up, `f` finds a
  file by name, `space w g` searches text in the workspace, `d` shows the
  task's diff (unified or side by side with `s`). Marks: `M`/`A`/`D` for the
  task's committed changes, `●` for uncommitted ones.
- Viewer: `j`/`k`, `ctrl+d`/`ctrl+u`, `g`/`G` scroll; `[`/`]` switch tabs,
  `x` closes one, `|` splits with the previous tab, `\` switches sides.
  A file that changes on disk reloads by itself.
- `e` opens the file in your editor (`studio.editor`, else `$VISUAL`, else
  `$EDITOR`) at the line on screen; Studio comes back when it exits.

### Code intelligence

Go files talk to `gopls` (one server per module root, at most three at a
time). Lines with problems get a `●` in the margin and the title counts them.

| Keys | What they do |
|---|---|
| `!` | the file's diagnostics; `enter` jumps to one |
| `S` | the file's symbols; `enter` jumps, `r` lists references |
| `ctrl+]` (editor) | go to the definition of the name under the cursor |
| `R` (editor) | references of the name under the cursor |

Locations outside the workspace (the standard library, the module cache)
are listed but not opened. In Terminals, `enter` on a `--- FAIL: TestX` line
opens the test function.

### Editing a task's worktree

Agents and you never write to a worktree at the same time. Editing takes the
worktree's **lease**:

1. `i` in a file of a task's worktree takes the lease and opens the editor.
   It is refused while an agent works there, and in the main checkout
   (Caffè never writes there). `e` takes the lease too.
2. While you hold it, an agent that needs the worktree waits (its task shows
   `blocked: worktree held by a manual edit session`).
3. The editor: `h j k l`, `0`/`$`, `g`/`G` move; `i a A o O` insert
   (`esc` back); `x` deletes a character, `D` a line; `u` undo, `ctrl+r`
   redo; `/` finds, `n`/`N` next/previous; `ctrl+s` saves. A file changed
   on disk since you opened it is not overwritten without asking.
4. When done: `C` commits the manual changes (your Git identity, message
   prefixed `manual:`), or `X` discards them; then `L` releases the
   worktree to the agents. The lease cannot be released with uncommitted
   changes, so no agent commit ever contains your edits.

Studio renews the lease every 5 s. If Studio dies, the lease expires after
15 s; an agent takes the worktree over only if it is clean.

## Terminals

**Terminals** (`6`) runs commands in the selected workspace.

- `n` runs a command line. It is split on spaces and run without a shell.
- `v` runs the project's `validation.commands` one after the other, stopping
  at the first failure.
- `s` opens your shell (`$SHELL`, `%COMSPEC%` on Windows) in the workspace;
  Studio comes back when it exits.
- `enter` moves a cursor through the output; `enter` on a `file:line` (test
  failures, compiler errors, stack traces) opens the file there.
- `c` stops a session (its whole process tree), `x` closes it.

Output is kept in memory (10 000 lines per session) and in full in
`.caffe/studio/sessions/`. Quitting Studio stops every session.

## Integration

**Integration** (`7`) turns the branches of a finished run into decisions.
Each developer branch shows its changes against the branch checked out in
the main checkout, the review verdict, the validation result, manual commits
and the latest decision.

- `a` approves, `r` rejects, each with an optional note; decisions are kept
  as a history and shown by `caffe inspect`.
- `m` previews the merge with `git merge-tree` and asks for confirmation,
  naming the exact command. It is refused unless the run has finished, the
  task is approved, the main checkout is clean and nothing conflicts.
- `d` shows the diff.

Merges are `git merge --no-ff` into the current branch of the main checkout.
Nothing is ever pushed. The CLI does the same:

```sh
caffe integrate <run>                      # list the branches
caffe integrate <run> <task> approve [note]
caffe integrate <run> <task> merge         # asks unless --yes
```

## Debugging Go tests

Debugging uses Delve (`dlv`, installed separately: `go install
github.com/go-delve/delve/cmd/dlv@latest`).

- In a file, `b` toggles a breakpoint on the cursor line (the first line on
  screen when not editing); breakpoints show as `◆`.
- `T` debugs the test function around that line; in Terminals, `D` on a
  `--- FAIL: TestX` line debugs that test in the session's workspace.
- **Debug** (`8`) shows the call stack, the variables of the selected frame,
  the source with `▶` at the current line, the breakpoints and a console.
  `c`/F5 continue, `n`/F10 step over, `s`/F11 step into, `o` step out,
  `x` stops; `j`/`k` select a frame (or a variable after `v`), `enter`
  expands a variable, `e` evaluates an expression.

Stopping, the test ending, or quitting Studio ends the `dlv` process.

## Files Studio writes

| Path | What |
|---|---|
| `.caffe/studio/sessions/*.log` | full output of terminal sessions |
| `.caffe/studio/crash-*.log` | details of a crash (the terminal is restored) |
| `.caffe/studio/studio.log` | Studio's own log |

## Settings

The `studio:` section of `.caffe/config.yaml`:

```yaml
studio:
  theme: caffe       # caffe | high-contrast | mono (NO_COLOR forces mono)
  editor: ""         # the command for `e`; empty: $VISUAL, then $EDITOR
```
