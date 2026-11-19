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

type State string

const (
	Running State = "running"
	Exited  State = "exited"
	Stopped State = "stopped"
	Failed  State = "failed"
)

const (
	BufferLines = 10_000

	stopGrace = 3 * time.Second

	notifyEvery = 50 * time.Millisecond
	maxLine     = 1 << 20
)

type Info struct {
	ID        int
	Kind      string
	Workspace string
	Dir       string
	Argv      []string
	State     State
	Code      int
	Err       error
	Started   time.Time
	Ended     time.Time
	LogPath   string
}

type Session struct {
	done chan struct{}

	mu       sync.Mutex
	info     Info
	lines    []string
	head     int
	dirty    bool
	stopping bool
	tree     *proctree.Tree
}

type Manager struct {
	logDir string

	Notify func(id int)

	mu       sync.Mutex
	next     int
	sessions []*Session
}

func NewManager(logDir string) *Manager { return &Manager{logDir: logDir} }

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
	s.mu.Lock()
	s.info.LogPath = log.Name()
	s.mu.Unlock()
	r, w, err := os.Pipe()
	if err != nil {
		log.Close()
		return err
	}

	cmd := exec.CommandContext(context.Background(), s.info.Argv[0], s.info.Argv[1:]...)
	cmd.Dir = s.info.Dir
	cmd.Stdout, cmd.Stderr = w, w
	tree, err := proctree.Start(cmd)
	w.Close()
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
		io.Copy(log, r)
	}()
	go func() {
		err := cmd.Wait()
		tree.Kill()
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

func (s *Session) Info() Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.info
}

func (s *Session) Lines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append(append([]string(nil), s.lines[s.head:]...), s.lines[:s.head]...)
}

func (s *Session) Done() <-chan struct{} { return s.done }

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

func (m *Manager) Sessions() []*Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*Session(nil), m.sessions...)
}

func (m *Manager) Get(id int) *Session {
	for _, s := range m.Sessions() {
		if s.info.ID == id {
			return s
		}
	}
	return nil
}

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

func (m *Manager) Running() int {
	n := 0
	for _, s := range m.Sessions() {
		if s.Info().State == Running {
			n++
		}
	}
	return n
}

func (m *Manager) StopAll() {
	var wg sync.WaitGroup
	for _, s := range m.Sessions() {
		wg.Add(1)
		go func() { defer wg.Done(); s.Stop() }()
	}
	wg.Wait()
}
