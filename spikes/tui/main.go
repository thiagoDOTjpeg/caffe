// Command tui-spike is the throwaway program of backlog item F-17: it checks,
// on a real terminal, the Bubble Tea v2 features Caffè Studio depends on
// (docs/studio/technical-decisions.md, ADR-001). Run it, press the keys shown,
// quit with q, and send back the summary it prints.
//
// It lives in its own Go module so Caffè's go.mod does not depend on Bubble
// Tea before the decision is confirmed.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

type tickMsg int                   // sent from a goroutine through Program.Send
type editorDoneMsg struct{ error } // the editor or shell hand-off returned

type model struct {
	width, height int
	resizes       int
	ticks         int
	lastKey       string
	mouse         bool
	lastMouse     string
	editorRuns    int
	shellRuns     int
	handoffErr    string
	dark          string
	file          string
}

func (m model) Init() tea.Cmd { return tea.RequestBackgroundColor }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resizes++
	case tea.BackgroundColorMsg:
		m.dark = fmt.Sprint(msg.IsDark())
	case tickMsg:
		m.ticks = int(msg)
	case tea.MouseMsg:
		mm := msg.Mouse()
		m.lastMouse = fmt.Sprintf("%v at %d,%d", mm.Button, mm.X, mm.Y)
	case editorDoneMsg:
		if msg.error != nil {
			m.handoffErr = msg.error.Error()
		}
	case tea.KeyPressMsg:
		m.lastKey = msg.String()
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "m":
			m.mouse = !m.mouse
		case "e":
			m.editorRuns++
			return m, tea.ExecProcess(editorCmd(m.file), func(err error) tea.Msg { return editorDoneMsg{err} })
		case "s":
			m.shellRuns++
			return m, tea.ExecProcess(shellCmd(), func(err error) tea.Msg { return editorDoneMsg{err} })
		case "p":
			panic("spike: deliberate panic to check the terminal is restored")
		}
	}
	return m, nil
}

func (m model) View() tea.View {
	var b strings.Builder
	fmt.Fprintf(&b, "Caffè Studio — TUI spike (F-17)   %s/%s\n\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "  size        %dx%d   (resize events: %d)  ← resize the window\n", m.width, m.height, m.resizes)
	fmt.Fprintf(&b, "  ticks       %d   ← must keep counting (sent from another goroutine)\n", m.ticks)
	fmt.Fprintf(&b, "  last key    %q   ← try arrows, ctrl+x, alt+x, F1, shift+tab\n", m.lastKey)
	fmt.Fprintf(&b, "  mouse       %v   last: %s   ← m toggles; click/scroll\n", m.mouse, m.lastMouse)
	fmt.Fprintf(&b, "  dark bg     %s\n", m.dark)
	fmt.Fprintf(&b, "  editor      %d run(s)   ← e opens %s; save, close, come back\n", m.editorRuns, strings.Join(editorCmd(m.file).Args, " "))
	fmt.Fprintf(&b, "  shell       %d run(s)   ← s opens a shell; type exit to come back\n", m.shellRuns)
	if m.handoffErr != "" {
		fmt.Fprintf(&b, "  hand-off error: %s\n", m.handoffErr)
	}
	b.WriteString("\n  p = deliberate panic (the terminal must come back usable)   q = quit\n")
	v := tea.NewView(b.String())
	v.AltScreen = true
	v.WindowTitle = "caffe tui spike"
	if m.mouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

func editorCmd(file string) *exec.Cmd {
	for _, env := range []string{"VISUAL", "EDITOR"} {
		if f := strings.Fields(os.Getenv(env)); len(f) > 0 {
			return exec.Command(f[0], append(f[1:], file)...)
		}
	}
	if runtime.GOOS == "windows" {
		return exec.Command("notepad", file)
	}
	return exec.Command("vi", file)
}

func shellCmd() *exec.Cmd {
	if runtime.GOOS == "windows" {
		if c := os.Getenv("ComSpec"); c != "" {
			return exec.Command(c)
		}
		return exec.Command("cmd.exe")
	}
	if s := os.Getenv("SHELL"); s != "" {
		return exec.Command(s)
	}
	return exec.Command("sh")
}

func main() {
	f, err := os.CreateTemp("", "caffe-spike-*.txt")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	f.WriteString("Edit me, save, and close the editor to return to the spike.\n")
	f.Close()
	defer os.Remove(f.Name())

	p := tea.NewProgram(model{file: f.Name()})
	go func() { // external events, the way the watcher will feed Studio
		for i := 1; ; i++ {
			time.Sleep(500 * time.Millisecond)
			p.Send(tickMsg(i))
		}
	}()
	final, err := p.Run()

	fmt.Println("=== tui-spike summary (send this back) ===")
	fmt.Printf("platform: %s/%s, TERM=%q, WT_SESSION set: %v\n", runtime.GOOS, runtime.GOARCH, os.Getenv("TERM"), os.Getenv("WT_SESSION") != "")
	if err != nil {
		fmt.Printf("program ended with error: %v\n", err)
		fmt.Println("(if you pressed p: is this terminal usable again — echo, cursor, colours? answer yes/no)")
	}
	if m, ok := final.(model); ok {
		fmt.Printf("resize events: %d (last %dx%d)\nticks received: %d\nlast key: %q\nmouse last: %q\neditor runs: %d, shell runs: %d, hand-off error: %q\ndark background: %s\n",
			m.resizes, m.width, m.height, m.ticks, m.lastKey, m.lastMouse, m.editorRuns, m.shellRuns, m.handoffErr, m.dark)
	}
	if b, err := os.ReadFile(f.Name()); err == nil {
		fmt.Printf("edited file now starts with: %q\n", firstLine(string(b)))
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
