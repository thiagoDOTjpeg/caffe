package dap

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"

	"caffe/internal/frame"
	"caffe/internal/proctree"
)

var ErrClosed = errors.New("debug adapter connection closed")

type Config struct {
	Name    string
	Command []string
}

var Delve = Config{Name: "dlv", Command: []string{"dlv", "dap", "--client-addr", "{addr}"}}

type Event struct {
	Kind     string
	Reason   string
	ThreadID int
	Category string
	Output   string
	ExitCode int
}

type Frame struct {
	ID   int
	Name string
	Path string
	Line int
}

type Scope struct {
	Name string
	Ref  int
}

type Variable struct {
	Name  string
	Type  string
	Value string
	Ref   int
}

type Breakpoint struct {
	Line     int
	Verified bool
	Message  string
}

type LaunchRequest struct {
	Mode    string
	Program string
	Cwd     string
	Args    []string
}

type message struct {
	Seq        int             `json:"seq"`
	Type       string          `json:"type"`
	Command    string          `json:"command,omitempty"`
	Event      string          `json:"event,omitempty"`
	RequestSeq int             `json:"request_seq,omitempty"`
	Success    bool            `json:"success,omitempty"`
	Message    string          `json:"message,omitempty"`
	Body       json.RawMessage `json:"body,omitempty"`
	Arguments  any             `json:"arguments,omitempty"`
}

type Session struct {
	cmd    *exec.Cmd
	tree   *proctree.Tree
	conn   net.Conn
	wmu    sync.Mutex
	mu     sync.Mutex
	seq    int
	wait   map[int]chan message
	events chan Event
	inited chan struct{}
	once   sync.Once
	done   chan struct{}
	err    error
	stderr strings.Builder
}

func Start(ctx context.Context, cfg Config, dir string) (*Session, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer ln.Close()
	args := make([]string, len(cfg.Command))
	for i, a := range cfg.Command {
		args[i] = strings.ReplaceAll(a, "{addr}", ln.Addr().String())
	}
	s := &Session{wait: map[int]chan message{}, events: make(chan Event, 256), inited: make(chan struct{}), done: make(chan struct{})}
	s.cmd = exec.CommandContext(context.Background(), args[0], args[1:]...)
	s.cmd.Dir = dir
	s.cmd.Stdout, s.cmd.Stderr = &s.stderr, &s.stderr
	if s.tree, err = proctree.Start(s.cmd); err != nil {
		return nil, fmt.Errorf("start %s: %w", args[0], err)
	}
	exited := make(chan struct{})
	go func() { s.cmd.Wait(); close(exited) }()
	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			accepted <- c
		}
	}()
	select {
	case s.conn = <-accepted:
	case <-exited:
		return nil, fmt.Errorf("%s exited before connecting: %s", args[0], strings.TrimSpace(s.stderr.String()))
	case <-ctx.Done():
		s.tree.Kill()
		return nil, ctx.Err()
	case <-time.After(30 * time.Second):
		s.tree.Kill()
		return nil, fmt.Errorf("%s did not connect", args[0])
	}
	go s.read()
	var caps json.RawMessage
	err = s.call(ctx, "initialize", map[string]any{"clientID": "caffe-studio", "adapterID": cfg.Name,
		"linesStartAt1": true, "columnsStartAt1": true, "pathFormat": "path"}, &caps)
	if err != nil {
		s.kill()
		return nil, fmt.Errorf("initialize %s: %w", cfg.Name, err)
	}
	return s, nil
}

func (s *Session) read() {
	r := bufio.NewReader(s.conn)
	var err error
	for {
		var body []byte
		if body, err = frame.Read(r); err != nil {
			break
		}
		var m message
		if json.Unmarshal(body, &m) != nil {
			continue
		}
		switch m.Type {
		case "response":
			s.mu.Lock()
			ch := s.wait[m.RequestSeq]
			delete(s.wait, m.RequestSeq)
			s.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		case "event":
			s.event(m)
		}
	}
	s.mu.Lock()
	s.err = errors.Join(ErrClosed, err)
	for k, ch := range s.wait {
		close(ch)
		delete(s.wait, k)
	}
	s.mu.Unlock()
	close(s.done)
}

func (s *Session) event(m message) {
	if m.Event == "initialized" {
		s.once.Do(func() { close(s.inited) })
		return
	}
	var b struct {
		Reason   string `json:"reason"`
		ThreadID int    `json:"threadId"`
		Category string `json:"category"`
		Output   string `json:"output"`
		ExitCode int    `json:"exitCode"`
	}
	json.Unmarshal(m.Body, &b)
	switch m.Event {
	case "stopped", "continued", "output", "terminated", "exited":
		select {
		case s.events <- Event{Kind: m.Event, Reason: b.Reason, ThreadID: b.ThreadID, Category: b.Category, Output: b.Output, ExitCode: b.ExitCode}:
		default:
		}
	}
}

func (s *Session) call(ctx context.Context, command string, args, body any) error {
	s.mu.Lock()
	if s.err != nil {
		s.mu.Unlock()
		return s.err
	}
	s.seq++
	seq := s.seq
	ch := make(chan message, 1)
	s.wait[seq] = ch
	s.mu.Unlock()
	s.wmu.Lock()
	err := frame.Write(s.conn, message{Seq: seq, Type: "request", Command: command, Arguments: args})
	s.wmu.Unlock()
	if err != nil {
		return err
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return ErrClosed
		}
		if !m.Success {
			return fmt.Errorf("%s: %s", command, m.Message)
		}
		if body != nil && len(m.Body) > 0 {
			return json.Unmarshal(m.Body, body)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Session) Events() <-chan Event  { return s.events }
func (s *Session) Done() <-chan struct{} { return s.done }

func (s *Session) Launch(ctx context.Context, req LaunchRequest, breakpoints map[string][]int) (map[string][]Breakpoint, error) {
	launch := make(chan error, 1)
	go func() {
		launch <- s.call(ctx, "launch", map[string]any{"request": "launch", "mode": req.Mode, "program": req.Program, "cwd": req.Cwd, "args": req.Args}, nil)
	}()
	select {
	case <-s.inited:
	case err := <-launch:
		if err != nil {
			return nil, err
		}
		select {
		case <-s.inited:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		launch <- nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	out := map[string][]Breakpoint{}
	for file, lines := range breakpoints {
		bps, err := s.SetBreakpoints(ctx, file, lines)
		if err != nil {
			return nil, err
		}
		out[file] = bps
	}
	if err := s.call(ctx, "configurationDone", map[string]any{}, nil); err != nil {
		return nil, err
	}
	if err := <-launch; err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Session) SetBreakpoints(ctx context.Context, file string, lines []int) ([]Breakpoint, error) {
	bps := make([]map[string]int, len(lines))
	for i, l := range lines {
		bps[i] = map[string]int{"line": l}
	}
	var body struct {
		Breakpoints []Breakpoint `json:"breakpoints"`
	}
	err := s.call(ctx, "setBreakpoints", map[string]any{"source": map[string]string{"path": file}, "breakpoints": bps}, &body)
	return body.Breakpoints, err
}

func (s *Session) Threads(ctx context.Context) ([]int, error) {
	var body struct {
		Threads []struct {
			ID int `json:"id"`
		} `json:"threads"`
	}
	if err := s.call(ctx, "threads", nil, &body); err != nil {
		return nil, err
	}
	var ids []int
	for _, t := range body.Threads {
		ids = append(ids, t.ID)
	}
	return ids, nil
}

func (s *Session) StackTrace(ctx context.Context, thread int) ([]Frame, error) {
	var body struct {
		StackFrames []struct {
			ID     int    `json:"id"`
			Name   string `json:"name"`
			Line   int    `json:"line"`
			Source struct {
				Path string `json:"path"`
			} `json:"source"`
		} `json:"stackFrames"`
	}
	if err := s.call(ctx, "stackTrace", map[string]any{"threadId": thread, "levels": 50}, &body); err != nil {
		return nil, err
	}
	var out []Frame
	for _, f := range body.StackFrames {
		out = append(out, Frame{ID: f.ID, Name: f.Name, Path: f.Source.Path, Line: f.Line})
	}
	return out, nil
}

func (s *Session) Scopes(ctx context.Context, frame int) ([]Scope, error) {
	var body struct {
		Scopes []struct {
			Name string `json:"name"`
			Ref  int    `json:"variablesReference"`
		} `json:"scopes"`
	}
	if err := s.call(ctx, "scopes", map[string]any{"frameId": frame}, &body); err != nil {
		return nil, err
	}
	var out []Scope
	for _, sc := range body.Scopes {
		out = append(out, Scope{Name: sc.Name, Ref: sc.Ref})
	}
	return out, nil
}

func (s *Session) Variables(ctx context.Context, ref int) ([]Variable, error) {
	var body struct {
		Variables []struct {
			Name  string `json:"name"`
			Type  string `json:"type"`
			Value string `json:"value"`
			Ref   int    `json:"variablesReference"`
		} `json:"variables"`
	}
	if err := s.call(ctx, "variables", map[string]any{"variablesReference": ref}, &body); err != nil {
		return nil, err
	}
	var out []Variable
	for _, v := range body.Variables {
		out = append(out, Variable{Name: v.Name, Type: v.Type, Value: v.Value, Ref: v.Ref})
	}
	return out, nil
}

func (s *Session) step(ctx context.Context, command string, thread int) error {
	return s.call(ctx, command, map[string]any{"threadId": thread}, nil)
}

func (s *Session) Continue(ctx context.Context, thread int) error {
	return s.step(ctx, "continue", thread)
}
func (s *Session) Next(ctx context.Context, thread int) error   { return s.step(ctx, "next", thread) }
func (s *Session) StepIn(ctx context.Context, thread int) error { return s.step(ctx, "stepIn", thread) }
func (s *Session) StepOut(ctx context.Context, thread int) error {
	return s.step(ctx, "stepOut", thread)
}

func (s *Session) Evaluate(ctx context.Context, expr string, frame int) (string, error) {
	var body struct {
		Result string `json:"result"`
	}
	err := s.call(ctx, "evaluate", map[string]any{"expression": expr, "frameId": frame, "context": "repl"}, &body)
	return body.Result, err
}

func (s *Session) Disconnect(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := s.call(ctx, "disconnect", map[string]any{"terminateDebuggee": true}, nil)
	select {
	case <-s.done:
	case <-ctx.Done():
	}
	s.kill()
	if errors.Is(err, ErrClosed) {
		err = nil
	}
	return err
}

func (s *Session) kill() {
	if s.conn != nil {
		s.conn.Close()
	}
	s.tree.Kill()
}
