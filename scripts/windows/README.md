# Caffè — Windows test kit

This kit is built on Linux (`make windows-kit`). **Windows needs no Go.** Each
step says what to send back.

CI already runs every test package on `windows-latest`. The kit covers what
CI cannot: a real machine, real consoles and a real runtime.

Requirements:

- **Git for Windows** with `git` on the `PATH` (the tests create repositories).
- For steps 3 and 4: **Herdr for Windows** with its server running, and at
  least one agent (for example Claude Code) installed **and signed in**.
- Put the kit folder in a path **without spaces** (for example `C:\caffe-kit`).

---

## 1. Automated tests

In PowerShell, inside the kit folder:

```powershell
powershell -ExecutionPolicy Bypass -File .\run-tests.ps1
```

**Send back:** `results.txt`. If a package fails, also
`tests\<package>\output.txt`.

This checks on a real Windows machine what CI checks on a runner: process
trees killed through a job object (`TestValidationCommandLeavesNoProcessesBehind`
in `core`, the session tests), the Herdr adapter, the full orchestrator on the
fake runtime, the CLI and the golden files.

## 2. TUI spike (F-17)

Run `tui-spike.exe` in **two** terminals:

- **Windows Terminal**;
- the **classic console**: `Win+R` → `conhost.exe C:\caffe-kit\tui-spike.exe`.

In each one:

1. Resize the window: the size on screen must change.
2. Check that `ticks` keeps counting by itself.
3. Press arrows, `ctrl+x`, `alt+x`, `F1`, `shift+tab`: `last key` must show each.
4. `m` turns the mouse on: click and scroll the wheel.
5. `e` opens Notepad: type something, save, close → back to the spike.
6. `s` opens `cmd`: type `exit` → back to the spike.
7. `q` quits. Copy the summary it prints.
8. Run it again and press `p` (a deliberate panic): the terminal must be
   usable again (cursor, key echo, colours).

**Send back:** both summaries and what you saw in items 1, 4 and 8.

> On Linux, run `dist/linux/tui-spike` and do the same (with `$EDITOR`, or `vi`).

## 3. A real run through Herdr (W-06)

In a throwaway repository (not a real project):

```powershell
mkdir C:\caffe-test; cd C:\caffe-test
git init -b main
git commit --allow-empty -m init
C:\caffe-kit\caffe.exe init
C:\caffe-kit\caffe.exe agents
C:\caffe-kit\caffe.exe run --plan C:\caffe-kit\plans\hello.json "Add a hello file"
```

If an agent asks whether it trusts the folder, the run prints the *attach*
command; run it in another window, accept, and come back.

**Send back:** the output of `caffe agents`, the output of the run and of
`C:\caffe-kit\caffe.exe inspect <id>`.

## 4. A detached run survives its console (W-04)

Still in `C:\caffe-test`, edit `.caffe\config.yaml` and replace
`commands: []` (under `validation:`) with:

```yaml
    commands:
        - ping -n 40 127.0.0.1
```

Then, in a new **cmd**:

```bat
C:\caffe-kit\caffe.exe -C C:\caffe-test run --detach --plan C:\caffe-kit\plans\slow.json "detach test"
```

1. As soon as `started in the background` appears, **close the window** (the X).
2. Open another cmd and run `C:\caffe-kit\caffe.exe -C C:\caffe-test status`:
   the run must be `executing`/`validating`.
3. After about 40 s, `caffe runs` must show `completed`.
4. Repeat, but this time press **Ctrl-C** in the window right after starting:
   the run must keep going too.

**Send back:** what `status` and `runs` showed in both cases, and
`.caffe\artifacts\<id>\console.log` if something fails.

## 5. Embedded PTY spike (EX-07)

In PowerShell, in **Windows Terminal**, inside the kit folder:

```powershell
.\pty-spike.exe | Tee-Object pty-results.txt
```

It runs everything by itself (about a minute) inside a ConPTY and prints one
line per criterion of ADR-004: PowerShell renders and takes input, resize
propagates, `less` and `vim` (from Git for Windows) work, 10 MB of output stays
responsive, and no process survives closing the pane. `htop`/`top` are
skipped on Windows. Do not touch the window while it runs.

**Send back:** `pty-results.txt`.
