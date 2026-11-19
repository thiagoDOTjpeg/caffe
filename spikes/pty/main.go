package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aymanbagabas/go-pty"
	"github.com/charmbracelet/x/vt"
)

type term struct {
	p   pty.Pty
	cmd *pty.Cmd
	mu  sync.Mutex
	emu *vt.Emulator
	n   int64
}

func start(w, h int, name string, args ...string) (*term, error) {
	p, err := pty.New()
	if err != nil {
		return nil, err
	}
	if err := p.Resize(w, h); err != nil {
		return nil, err
	}
	t := &term{p: p, emu: vt.NewEmulator(w, h)}
	t.cmd = p.Command(name, args...)
	t.cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	if err := t.cmd.Start(); err != nil {
		return nil, err
	}
	go io.Copy(p, t.emu)
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := p.Read(buf)
			if n > 0 {
				t.mu.Lock()
				t.emu.Write(buf[:n])
				t.n += int64(n)
				t.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return t, nil
}

func (t *term) screen() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.emu.String()
}

func (t *term) wait(want string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if strings.Contains(t.screen(), want) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func (t *term) send(s string) { t.p.Write([]byte(s)) }

func (t *term) resize(w, h int) {
	t.mu.Lock()
	t.emu.Resize(w, h)
	t.mu.Unlock()
	t.p.Resize(w, h)
}

func sessionMembers(sid int) []int {
	entries, _ := os.ReadDir("/proc")
	var out []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		f := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
		if len(f) > 3 {
			if s, _ := strconv.Atoi(f[3]); s == sid {
				out = append(out, pid)
			}
		}
	}
	return out
}

func (t *term) close() {
	t.p.Close()
	if t.cmd.Process == nil {
		return
	}
	sid := t.cmd.Process.Pid
	time.Sleep(200 * time.Millisecond)
	for _, pid := range sessionMembers(sid) {
		syscall.Kill(pid, syscall.SIGKILL)
	}
	syscall.Kill(-sid, syscall.SIGKILL)
}

var failed bool

func check(name string, ok bool, detail string) {
	mark := "PASS"
	if !ok {
		mark, failed = "FAIL", true
	}
	fmt.Printf("%-34s %s  %s\n", name, mark, detail)
}

func shell() {
	t, err := start(80, 24, "bash", "--norc", "--noprofile", "-i")
	if err != nil {
		check("shell", false, err.Error())
		return
	}
	defer t.close()
	t.send("echo hi-$((40+2))\r")
	check("shell renders and takes input", t.wait("hi-42", 5*time.Second), "bash --norc -i")
	t.send("stty size\r")
	ok := t.wait("24 80", 5*time.Second)
	t.resize(120, 40)
	time.Sleep(100 * time.Millisecond)
	t.send("clear; stty size\r")
	check("resize propagates", ok && t.wait("40 120", 5*time.Second), "24x80 then 40x120 seen by stty")
}

func less(dir string) {
	f := filepath.Join(dir, "lines.txt")
	var b strings.Builder
	for i := 1; i <= 200; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	os.WriteFile(f, []byte(b.String()), 0o644)
	t, err := start(80, 24, "less", f)
	if err != nil {
		check("less", false, err.Error())
		return
	}
	defer t.close()
	ok := t.wait("line 23", 5*time.Second) && !strings.Contains(t.screen(), "line 30")
	t.send(" ")
	ok = ok && t.wait("line 40", 5*time.Second)
	t.send("q")
	check("less pages in the alternate screen", ok, "200 lines, page down")
}

func editor(dir string) {
	name := "vim"
	if _, err := exec.LookPath(name); err != nil {
		name = "nvim"
	}
	if _, err := exec.LookPath(name); err != nil {
		check("vim", false, "neither vim nor nvim installed")
		return
	}
	f := filepath.Join(dir, "edit.txt")
	t, err := start(80, 24, name, "-u", "NONE", "-i", "NONE", f)
	if err != nil {
		check(name, false, err.Error())
		return
	}
	defer t.close()
	ok := t.wait("~", 10*time.Second)
	t.send("ihello from the pane\x1b:wq\r")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, _ := os.ReadFile(f); strings.Contains(string(b), "hello from the pane") {
			check(name+" edits and saves", ok, "insert, :wq")
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	check(name+" edits and saves", false, "file not written; screen:\n"+t.screen())
}

func top() {
	name := "htop"
	if _, err := exec.LookPath(name); err != nil {
		name = "top"
	}
	t, err := start(100, 30, name)
	if err != nil {
		check(name, false, err.Error())
		return
	}
	defer t.close()
	ok := t.wait("PID", 10*time.Second)
	t.send("q")
	check(name+" draws a full-screen UI", ok, "header with PID")
}

func flood() {
	t, err := start(100, 30, "sh", "-c", "head -c 10000000 /dev/zero | tr '\\0' x | fold -w 99; echo DONE-FLOOD")
	if err != nil {
		check("10 MB of output", false, err.Error())
		return
	}
	defer t.close()
	began := time.Now()
	var worst time.Duration
	ok := false
	for time.Since(began) < 60*time.Second {
		at := time.Now()
		scr := t.screen()
		worst = max(worst, time.Since(at))
		if strings.Contains(scr, "DONE-FLOOD") {
			ok = true
			break
		}
		time.Sleep(16 * time.Millisecond)
	}
	took := time.Since(began)
	t.mu.Lock()
	n := t.n
	t.mu.Unlock()
	check("10 MB of output stays responsive", ok && worst < 50*time.Millisecond,
		fmt.Sprintf("%.1f MB in %s; slowest frame %s", float64(n)/1e6, took.Round(time.Millisecond), worst.Round(time.Millisecond)))
}

func orphans(dir string) {
	pidFile := filepath.Join(dir, "child.pid")
	t, err := start(80, 24, "bash", "--norc", "--noprofile", "-i")
	if err != nil {
		check("no orphan after closing", false, err.Error())
		return
	}
	t.send("sleep 300 & echo $! > " + pidFile + "\r")
	var pid int
	for i := 0; i < 100 && pid == 0; i++ {
		b, _ := os.ReadFile(pidFile)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		time.Sleep(20 * time.Millisecond)
	}
	t.close()
	dead := false
	for i := 0; i < 100 && !dead; i++ {
		dead = syscall.Kill(pid, 0) != nil
		if !dead {
			if st, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); strings.Contains(string(st), ") Z") {
				dead = true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !dead && pid > 0 {
		syscall.Kill(pid, syscall.SIGKILL)
	}
	check("no orphan after closing", pid > 0 && dead, fmt.Sprintf("background sleep %d", pid))
}

func main() {
	dir, _ := os.MkdirTemp("", "pty-spike")
	defer os.RemoveAll(dir)
	shell()
	less(dir)
	editor(dir)
	top()
	flood()
	orphans(dir)
	if failed {
		os.Exit(1)
	}
}
