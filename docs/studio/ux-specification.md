# Caffè Studio — UX Specification

Status: **Proposed.** Wireframes are terminal-cell sketches, not pixel-exact.
Names of data fields refer to the existing model (`internal/contracts`).

---

## 1. Layout

### 1.1 Regions

```text
┌ caffè ─ my-project ─ run 0112dd6a · executing ── 3/4 tasks ── ● herdr 0.9.5 ┐  header
├─────────────┬────────────────────────────────────────────────────────────────┤
│ SIDEBAR     │ MAIN                                                           │
│             │                                                                │
│ ▸ Overview  │  the active screen                                             │
│   Tasks     │                                                                │
│   Agents    │                                                                │
│   Runs      │                                                                │
│   Workspace │                                                                │
│   Terminals │                                                                │
│             │                                                                │
├─────────────┴────────────────────────────────────────────────────────────────┤
│ PANEL  events │ output │ diagnostics                             (toggle: `) │  bottom panel
├──────────────────────────────────────────────────────────────────────────────┤
│ NORMAL · ws: caffe/0112dd6a/impl · ⚠ 1 blocked · ? help · : commands         │  status bar
└──────────────────────────────────────────────────────────────────────────────┘
```

- **Header**: product mark, project name, selected run (short id, status),
  progress, runtime health. Always one line.
- **Sidebar**: screen list; in the Workspace screen it becomes the explorer.
  Collapsible (`ctrl+b`).
- **Main**: the active screen.
- **Bottom panel**: events, output of the focused session, diagnostics.
  Toggle with `` ` ``; resizable with `ctrl+↑/↓`.
- **Status bar**: mode, **current workspace** (always), attention counters
  (blocked agents, failed tasks, unsaved buffers), help hints.

### 1.2 Alternatives considered

| Layout | Verdict |
|---|---|
| Single full-screen view with tab strip | Used below 80 columns. Too little context for supervision at normal sizes. |
| Dashboard grid (tiles for every agent) | Breaks down beyond ~4 agents and hides the code. Kept as the Agent Room screen only. |
| IDE layout with orchestration as a side panel | Makes orchestration secondary; contradicts the product's purpose. |
| **Sidebar + main + bottom panel** | Chosen: one focus at a time, supervision information always one key away. |

### 1.3 Responsive behaviour

| Terminal size | Behaviour |
|---|---|
| ≥ 120 × 30 | Full layout; sidebar 18–24 columns; bottom panel open by default (8 rows). |
| 80–119 × 24–29 | Sidebar collapsed to icons + letters; bottom panel closed by default. |
| < 80 columns or < 24 rows | Single pane: header, main, status bar. Sidebar and panel open as overlays. |
| < 60 × 16 | A message: "Caffè Studio needs at least 60×16" with the current size. Nothing else is drawn. |

Resizing re-lays out immediately; no state is lost.

## 2. Screens

### 2.1 Overview

```text
┌ caffè ─ my-project ─ run 0112dd6a · executing ── 2/4 tasks ── ● herdr 0.9.5 ┐
│ Overview                                                                     │
│ Request  Add a hello file to the project                                     │
│ Started  14:09:31 (2m 01s)        Phase  reviewing      Base  7f5d944         │
│                                                                              │
│ TASKS  ■■■□  2 completed · 1 running · 1 pending                              │
│  ✓ research    researcher  completed   55s                                    │
│  ✓ impl        developer   completed   39s    caffe/0112dd6a/impl             │
│  ● review      reviewer    running     12s    agent c8eccebbd                 │
│  ◌ validate    validator   pending           waiting on review (running)     │
│                                                                              │
│ AGENTS                                   HEALTH                              │
│  ● c8eccebbd  reviewer  claude  working   ● run heartbeat 1s ago             │
│                                          ● runtime reachable                 │
│                                          ○ warnings 0                        │
│ RECENT EVENTS                                                                │
│  14:11:05  task.running     review   agent c8eccebbd-reviewer                │
│  14:11:05  diff.collected   impl     1 file                                  │
│  14:11:05  task.completed   impl                                             │
├──────────────────────────────────────────────────────────────────────────────┤
│ NORMAL · ws: main · enter open · c cancel run · r report · ? help            │
└──────────────────────────────────────────────────────────────────────────────┘
```

- "Phase" is the run status (`executing`, `reviewing`, `validating`).
- Health: heartbeat age (from `runs.heartbeat_at`), runtime reachability
  (last `Capabilities` result), warning count (events of type `warning`).
- With no run selected: the most recent active run; if none, the empty state
  (§6).

### 2.2 Task Board

```text
│ Tasks · run 0112dd6a                                   [b]oard  [g]raph  [l]ist │
│ PENDING      READY        RUNNING        REVIEW        DONE         FAILED      │
│ ◌ validate                ● review                     ✓ research               │
│   ⤷ review                  reviewer                   ✓ impl                   │
│                             12s · 1/3                                           │
│                                                                                 │
│ ─ review ──────────────────────────────────────────────────────────────────    │
│ Review the change that adds hello.txt …                                         │
│ role reviewer · attempt 1/3 · depends on impl (completed)                       │
│ criteria  ☐ The diff was read and evaluated                                     │
│ [enter] execution  [d] diff of impl  [w] open worktree  [a] agent               │
```

Columns map task statuses: PENDING = `pending`; READY = `ready`, `assigned`;
RUNNING = `running`, `blocked` (blocked cards carry `⚠ blocked`); REVIEW =
`reviewing`; DONE = `completed`; FAILED = `failed`, `cancelled` (with the
reason). A column with no cards collapses to its title.

**Graph view** (`g`): dependency layers, left to right.

```text
│ research ✓ ──▶ impl ✓ ──▶ review ● ──▶ validate ◌                               │
│ docs ✓ ─────────────────────┘                                                    │
```

Layered by longest path from a root; edges drawn with box characters; cycles
cannot occur (plans are validated). Above ~30 tasks the graph view falls back
to the list view with a "depends on" column.

### 2.3 Agent Room

```text
│ Agents · run 0112dd6a                                          2 live · 1 done │
│ ┌──────────────────────────────┐ ┌──────────────────────────────┐              │
│ │ ▗▄▖  c8eccebbd               │ │ ▗▄▖  caa8bdb64               │              │
│ │ ▐█▌  reviewer · claude       │ │ ▐█▌  developer · claude      │              │
│ │      ● WORKING   12s         │ │      ✓ COMPLETED  39s        │              │
│ │ task  review                 │ │ task  impl                   │              │
│ │ ws    caffe/0112dd6a/impl    │ │ ws    caffe/0112dd6a/impl    │              │
│ │ last  task.running 14:11:05  │ │ last  execution.completed    │              │
│ │ [o]utput [a]ttach [i]nspect  │ │ [o]utput [i]nspect [d]iff    │              │
│ └──────────────────────────────┘ └──────────────────────────────┘              │
│ ─ output · c8eccebbd (live, refreshed 1s ago) ─────────────────────────────    │
│ ● Read changes.diff and checked it against the worktree …                       │
```

- Cards are sorted: blocked, working/waiting, starting, then finished.
- `o` shows the agent's output in the lower half: live (through the runtime)
  while the agent exists, the stored `output.txt` afterwards. The source is
  always labelled ("live" / "captured at end").
- `a` attach: only shown if the runtime supports it (ADR-005). Studio suspends,
  the runtime's attach runs in the full terminal, Studio resumes on exit.

### 2.4 Runs

```text
│ Runs                                                       / filter  a active │
│   RUN       STATUS       CREATED           DURATION  REQUEST                  │
│ ▸ 0112dd6a  executing    today 14:09       2m 01s    Add a hello file …       │
│   531fd2a9  completed    today 14:11       1m 17s    Create slow.txt          │
│   7b07d073  cancelled    today 14:08       18s       Add a hello file …       │
│   9c41aa02  interrupted  yesterday 18:22   —         Refactor config …        │
│ [enter] open  [n] new run  [R] resume  [c] cancel  [x] clean                  │
```

### 2.5 Execution Inspector

```text
│ Execution 8eccebbd · review · attempt 1 · reviewer · claude                    │
│ status completed · 14:11:05 → 14:11:32 (27s) · agent c8eccebbd-reviewer        │
│ runtime  workspace w3 · pane w3:p1 · terminal term_66…                         │
│ ┌ tabs: [Report] Prompt  Output  Artifacts  Events ───────────────────────┐    │
│ │ ✓ approved (reviewer recommendation)                                    │    │
│ │ Summary  Read changes.diff and checked it against the worktree …        │    │
│ │ Findings                                                                │    │
│ │  info  hello.txt:1  The impl report says the change was left …          │    │
│ └─────────────────────────────────────────────────────────────────────────┘    │
```

For validation executions the header shows `exit 0 · observed` (orchestrator
ran it). For agent-reported tests the Report tab shows
`exit 0 · claimed by agent, not verified`.

### 2.6 Workspace

```text
┌ caffè ─ my-project ─ ws: caffe/0112dd6a/impl  (task impl · completed) ───────┐
├─────────────┬─────────────────────────────────────────────────────────────────┤
│ EXPLORER    │ [hello.txt] README.md ●                                  diff ▾ │
│ ▾ impl      │   1 │ hello from herd                                           │
│   M hello…  │                                                                 │
│     READ…   │                                                                 │
│ GIT         │                                                                 │
│  1 changed  │                                                                 │
│  base 7f5d9 │                                                                 │
├─────────────┴─────────────────────────────────────────────────────────────────┤
│ NORMAL · ws: caffe/0112dd6a/impl · editable · hello.txt 1:1 · e $EDITOR       │
└───────────────────────────────────────────────────────────────────────────────┘
```

- The **workspace selector** (`ctrl+w`) lists: `main` (project checkout) and
  every existing task worktree as `caffe/<run>/<task>` with task status and
  lease holder.
- The header and the status bar both show the selected workspace. Switching
  workspace with unsaved buffers in the old one asks first.
- Editability badge: `editable`, `read-only: agent c… is writing (task impl
  running)`, `read-only: file too large`.
- Diff mode (`d`): unified or side by side (≥ 120 columns), against the task's
  base; a file list on the left.

### 2.7 Terminal Manager

```text
│ Terminals                                                                      │
│  #  KIND     WORKSPACE               COMMAND             STATE       TIME     │
│  1  command  caffe/0112dd6a/impl     go test ./...       exit 1      4.2s     │
│  2  command  main                    make check          running     12s      │
│  3  shell    caffe/0112dd6a/impl     $SHELL              running     3m       │
│ [n] new command  [v] validation  [s] shell  [c] stop  [x] close  [enter] out │
│ ─ #1 go test ./... ── exit 1 ───────────────────────────────────────────────  │
│ --- FAIL: TestHello (0.00s)                                                    │
│     hello_test.go:12: got "x", want "hello from herd"     ← enter opens file  │
```

Agent sessions do **not** appear here; they live in the Agent Room.
`s` hands the whole terminal to `$SHELL` in the selected workspace (ADR-004)
and returns on exit; until M3b, shells are not rows of this list. `enter`
moves a cursor through the output; `enter` on a `file:line` opens it.

### 2.8 Debugger (M5)

```text
│ Debug · dlv · TestHello · paused at hello.go:14                                │
│ ┌ CALL STACK ───────────┐┌ SOURCE ────────────────────────────────────────┐   │
│ │▸ main.greet  hello:14 ││ 13 │ func greet(n string) string {             │   │
│ │  main.TestH… _test:12 ││▶14 │     return "hello " + n                  │   │
│ ├ VARIABLES ────────────┤│ 15 │ }                                          │   │
│ │ n  string  "from herd"││                                                 │   │
│ ├ BREAKPOINTS ──────────┤└─────────────────────────────────────────────────┘   │
│ │ ● hello.go:14         │ F5 continue · F10 over · F11 into · ⇧F11 out · ⇧F5 stop │
```

## 3. Navigation model

### 3.1 Hierarchy

1. **Global keys**: work everywhere except in insert mode and in a focused
   interactive shell (only the escape chord is reserved there).
2. **Leader** (`space` in normal mode): opens a which-key menu grouped by area.
3. **Screen keys**: shown in the status bar for the focused screen.
4. **Command palette** (`:` or `ctrl+p`): every action, searchable, with its key.

Every action is registered once in an **action registry** (id, title, scope,
keys, enabled predicate). Keys, the palette, the leader menu and `?` help are
all generated from it, so they cannot disagree.

### 3.2 Global keys

| Key | Action |
|---|---|
| `?` | help for the focused view (from the registry) |
| `:` / `ctrl+p` | command palette |
| `space` | leader menu |
| `1`…`6` | Overview, Tasks, Agents, Runs, Workspace, Terminals |
| `tab` / `shift+tab` | next / previous pane |
| `ctrl+w` | workspace selector |
| `` ` `` | toggle bottom panel |
| `ctrl+b` | toggle sidebar |
| `esc` | leave mode / close overlay |
| `q` | close view or overlay (never quits from a screen) |
| `ctrl+q` | quit (confirms if buffers are unsaved or sessions are running) |
| `ctrl+g` | escape chord inside a focused interactive shell |

### 3.3 Leader groups

| Sequence | Group |
|---|---|
| `space r` | run: `n` new, `c` cancel, `R` resume, `x` clean, `o` open report |
| `space w` | workspace: `s` select, `f` find file, `g` grep, `d` diff, `c` commit |
| `space t` | terminal: `c` command, `s` shell, `v` validation commands, `k` stop |
| `space a` | agent: `o` output, `a` attach, `i` inspect |
| `space c` | code: `d` definition, `r` references, `s` symbols, `e` diagnostics |
| `space d` | debug: `t` debug test, `b` toggle breakpoint, `x` stop |

### 3.4 List and editor keys

Lists and trees: `j`/`k` or arrows, `g`/`G` top/bottom, `enter` open, `/`
filter, `h`/`l` collapse/expand. Viewer: `j`/`k`, `ctrl+d`/`ctrl+u`, `/` search,
`n`/`N`, `:<line>` go to line, `e` open in `$EDITOR`.

Built-in editor (M2b) has two modes, **normal** (viewer keys) and **insert**
(`i` to enter, `esc` to leave); standard keys in insert mode (`ctrl+s` save,
`ctrl+z` undo, `ctrl+y` redo). Full Vim emulation is a non-goal.

### 3.5 Mouse

Optional and off by default (`studio.mouse: true`). When on: click to focus a
pane, select list items, scroll, click tabs. No drag-to-resize in M1.

### 3.6 Focus behaviour

- Exactly one focused pane; its border is double (`═`) and its title is
  inverted; others are single (`─`). This works without colour.
- Focus moves only on explicit keys, never because data changed.
- Overlays (palette, confirm dialog, selector) capture focus and restore it on
  close.
- New data never moves the cursor of a list: selection is kept by id, not by
  index.

## 4. Agent visualization

### 4.1 Agent visual state

Agent visual state is **derived**, separately from task state, from the
agent row, the latest execution and the last runtime status:

| Visual state | Symbol | Rule (first match wins) |
|---|---|---|
| Cancelled | `⊘` | execution `cancelled` |
| Failed | `✗` | execution `failed` or `unknown` (the latter labelled "outcome unknown") |
| Completed | `✓` | execution `completed` |
| Lost | `?` | execution `running` or `interrupted` but the runtime reports the agent not found |
| Blocked | `⚠` | runtime status `blocked` |
| Working | `●` | runtime status `working` |
| Waiting | `◐` | execution `running`/`interrupted`, runtime `idle`/`done`, no report yet ("idle, report not delivered") |
| Interrupted | `⏸` | execution `interrupted` (the orchestrator process died) and no runtime information; `caffe resume` decides what happens |
| Starting | `◌` | agent row `starting`, or execution `running` and no runtime status (or `unknown`) yet |
| Stopped | `■` | agent row `stopped` and the agent has no execution of its own (planning agents: their result is the plan) |
| Idle | `○` | agent exists, no running execution |

Implemented as `service.DeriveVisualState` (table-tested in
`internal/service/service_test.go`).

"Waiting" exists because idle ≠ done: an agent waiting on a background command
is idle. The card shows how long it has been idle.

### 4.2 Avatars

Optional, off in monochrome mode and in compact layouts.

- One small pixel-art glyph per **role**, drawn with half-block characters
  (`▀ ▄ █ ▌ ▐`), 3–4 cells wide, 2 rows high, in the brand palette (Cream,
  Caramel on Espresso).
- Avatars identify the **role** only. State is always the badge (symbol +
  text). An avatar is never the only carrier of meaning.
- The only animation: a two-frame "steam" flicker above Working agents, at
  most 2 fps, disabled by `studio.reduce_motion: true` or `NO_COLOR`.

Example glyphs (illustrative):

```text
researcher  developer   reviewer    validator
 ▗▄▖         ▄█▄         ▐▀▌         ▗█▖
 ▐◉▌         ▐▄▌         ▐▄▌         ▝▀▘
```

### 4.3 Palette

From [docs/brand.md](../brand.md): Espresso `#241A16` background, Secondary
`#30231D` panels, Cream `#F3E7D5` text, Caramel `#C5A27B` accent and focus,
Muted `#A99583` secondary text. State colours are added for meaning and must
pass contrast on Espresso: success (green), warning (amber), error (red),
neutral (Muted). In the high-contrast theme, text is pure white on black and
the accent is used only for focus.

## 5. Confirmations

Confirm dialogs name the target and the consequence; the default button is
"Cancel".

```text
┌ Cancel run 0112dd6a? ───────────────────────────────────┐
│ 1 agent will be stopped (c8eccebbd-reviewer).           │
│ Completed work and worktrees are kept.                  │
│                         [ Cancel ]   [ Stop the run ]   │
└─────────────────────────────────────────────────────────┘
```

Confirmed actions: cancel run, clean worktrees, close a running session, quit
with running sessions, discard unsaved changes, overwrite a file changed on
disk, commit, approve/merge (M6).

## 6. Empty, loading, error and disconnected states

| State | Shown as |
|---|---|
| Not in a Caffè project | full-screen message with the directory and "run `caffe init`" |
| No runs yet | Overview: "No runs yet. Press `n` to start one, or `caffe run "…"`." |
| Loading | the view's frame with "loading…" in its title; never a blank screen |
| Database unreadable | header indicator `✗ database`, last good data kept and marked "stale since 14:12:03", retry with backoff |
| Runtime unreachable | header `✗ herdr: server not running` (adapter's error text); Agent Room shows stored state only; attach and live output disabled with the reason |
| Run owner gone | run shown as `interrupted` once detected, with `resume`/`cancel` |
| Event cursor gap | (cannot normally happen) a full snapshot reload, logged |
| Action failed | a toast with the error; detail in the events panel; state re-read |
| Terminal too small | §1.3 |

Toasts disappear after 5 s; errors stay until dismissed.
