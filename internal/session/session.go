// Package session runs command sessions for Studio: an argv (never a shell)
// in a workspace, as its own process tree, with its output in a bounded ring
// buffer and a log file. Sessions live for the Manager's lifetime; StopAll
// ends every one of them.
package session

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"caffe/internal/proctree"
)

// State is where a session is in its life.
type State string

const (
	Running State = "running"
	Exited  State = "exited"  // the command ended by itself; see Code
	Stopped State = "stopped" // ended by Stop
	Failed  State = "failed"  // never started; see Err
)

const (
	// BufferLines bounds each session's in-memory output (NFR-P3); the log
	// file keeps everything.
	BufferLines = 10_000
	// stopGrace is how long Stop waits after SIGTERM before SIGKILL.
	stopGrace = 3 * time.Second
	// notifyEvery batches output notifications (≤ 20/s per session).
	notifyEvery = 50 * time.Millisecond
	maxLine     = 1 << 20
)

// Info is a consistent copy of a session's state.
type Info struct {
	ID        int
	Kind      string // what started it: "command", "validation", …
	Workspace string // label shown next to the session
	Dir       string
	Argv      []string
	State     State
	Code      int // exit code; -1 when killed, stopped or never started
	Err       error
	Started   time.Time
	Ended     time.Time // zero while running
	LogPath   string
}

// Session is one command run.
type Session struct {
	done chan struct{}

	mu       sync.Mutex
	info     Info
	lines    []string // ring buffer of the last BufferLines lines
	head     int      // index of the oldest line once the ring is full
	dirty    bool     // output since the last notification
	stopping bool
	tree     *proctree.Tree
}

// Manager owns the sessions of one Studio.
type Manager struct {
	logDir string
	// Notify, if set, is called from a session goroutine when a session got
	// new output (batched) or changed state. It must not block.
	Notify func(id int)

	mu       sync.Mutex
	next     int
	sessions []*Session
}

// NewManager writes session logs under logDir (created on demand).
func NewManager(logDir string) *Manager { return &Manager{logDir: logDir} }

// Start runs argv in dir. A command that cannot start still becomes a
// session, in state Failed, so the UI can show why.
func (m *Manager) Start(kind, workspace, dir string, argv []string) *Session {
	m.mu.Lock()
	m.next++
	s := &Session{done: make(chan struct{}), info: Info{
		ID: m.next, Kind: kind, Workspace: workspace, Dir: dir, Argv: argv,
		State: Running, Code: -1, Started: time.Now(),
	}}
	m.sessions = append(m.sessions, s)
	m.mu.Unlock()

	if err := s.start(m.logDir); err != nil {
		s.mu.Lock()
		s.info.State, s.info.Err, s.info.Ended = Failed, err, time.Now()
		s.mu.Unlock()
		close(s.done)
		m.notify(s.info.ID)
	} else {
		go m.watch(s)
	}
	return s
}

func (s *Session) start(logDir string) error {
	if len(s.info.Argv) == 0 {
		return fmt.Errorf("empty command")
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}
	log, err := os.CreateTemp(logDir, fmt.Sprintf("%d-*.log", s.info.ID))
	if err != nil {
		return err
	}
	s.info.LogPath = log.Name()
	r, w, err := os.Pipe()
	if err != nil {
		log.Close()
		return err
	}
	// proctree.Start sets cmd.Cancel, which needs CommandContext.
	cmd := exec.CommandContext(context.Background(), s.info.Argv[0], s.info.Argv[1:]...)
	cmd.Dir = s.info.Dir
	cmd.Stdout, cmd.Stderr = w, w // one stream: keeps stdout/stderr interleaving
	tree, err := proctree.Start(cmd)
	w.Close() // the children hold their own copy
	if err != nil {
		r.Close()
		log.Close()
		return err
	}
	s.mu.Lock()
	s.tree = tree
	s.mu.Unlock()

	read := make(chan struct{})
	go func() {
		defer close(read)
		defer r.Close()
		defer log.Close()
		s.readLines(io.TeeReader(r, log))
		io.Copy(log, r) // after a line too long to split: keep logging
	}()
	go func() {
		err := cmd.Wait()
		tree.Kill() // background children would keep the pipe open
		<-read
		s.mu.Lock()
		s.info.Ended = time.Now()
		var exitErr *exec.ExitError
		switch {
		case s.stopping:
			s.info.State = Stopped
		case err == nil:
			s.info.State, s.info.Code = Exited, 0
		case errors.As(err, &exitErr):
			s.info.State, s.info.Code = Exited, exitErr.ExitCode()
		default:
			s.info.State, s.info.Err = Exited, err
		}
		s.mu.Unlock()
		close(s.done)
	}()
	return nil
}

func (s *Session) readLines(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), maxLine)
	for sc.Scan() {
		s.mu.Lock()
		if len(s.lines) < BufferLines {
			s.lines = append(s.lines, sc.Text())
		} else {
			s.lines[s.head] = sc.Text()
			s.head = (s.head + 1) % BufferLines
		}
		s.dirty = true
		s.mu.Unlock()
	}
	if sc.Err() != nil {
		s.mu.Lock()
		s.lines = append(s.lines, fmt.Sprintf("[line longer than %d bytes: the rest of the output is only in %s]", maxLine, s.info.LogPath))
		s.dirty = true
		s.mu.Unlock()
	}
}

// watch sends batched notifications until the session ends, then one more.
func (m *Manager) watch(s *Session) {
	t := time.NewTicker(notifyEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			s.mu.Lock()
			dirty := s.dirty
			s.dirty = false
			s.mu.Unlock()
			if dirty {
				m.notify(s.info.ID)
			}
		case <-s.done:
			m.notify(s.info.ID)
			return
		}
	}
}

func (m *Manager) notify(id int) {
	if m.Notify != nil {
		m.Notify(id)
	}
}

// Info returns a copy of the session's state.
func (s *Session) Info() Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.info
}

// Lines returns the buffered output, oldest first.
func (s *Session) Lines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append(append([]string(nil), s.lines[s.head:]...), s.lines[:s.head]...)
}

// Done is closed once the session ended and its output was read.
func (s *Session) Done() <-chan struct{} { return s.done }

// Stop terminates the session's process tree: SIGTERM, then SIGKILL after a
// grace period. It returns once the session ended.
func (s *Session) Stop() {
	s.mu.Lock()
	tree := s.tree
	running := s.info.State == Running
	if running {
		s.stopping = true
	}
	s.mu.Unlock()
	if !running || tree == nil {
		<-s.done
		return
	}
	tree.Terminate()
	select {
	case <-s.done:
	case <-time.After(stopGrace):
		tree.Kill()
		<-s.done
	}
}

// Sessions returns every session, oldest first.
func (m *Manager) Sessions() []*Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*Session(nil), m.sessions...)
}

// Get returns the session with id, or nil.
func (m *Manager) Get(id int) *Session {
	for _, s := range m.Sessions() {
		if s.info.ID == id {
			return s
		}
	}
	return nil
}

// Remove forgets a session that is no longer running; it reports whether it
// did. Its log file stays.
func (m *Manager) Remove(id int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.sessions {
		if s.info.ID == id && s.Info().State != Running {
			m.sessions = append(m.sessions[:i], m.sessions[i+1:]...)
			return true
		}
	}
	return false
}

// Running counts the sessions still running.
func (m *Manager) Running() int {
	n := 0
	for _, s := range m.Sessions() {
		if s.Info().State == Running {
			n++
		}
	}
	return n
}

// StopAll stops every running session concurrently and waits for all.
func (m *Manager) StopAll() {
	var wg sync.WaitGroup
	for _, s := range m.Sessions() {
		wg.Add(1)
		go func() { defer wg.Done(); s.Stop() }()
	}
	wg.Wait()
}
