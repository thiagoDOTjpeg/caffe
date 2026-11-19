package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"caffe/internal/frame"
)

var ErrClosed = errors.New("language server connection closed")

type message struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *RPCError        `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("language server error %d: %s", e.Code, e.Message)
}

type Handler func(method string, params json.RawMessage) (any, error)

type conn struct {
	w       io.Writer
	wmu     sync.Mutex
	mu      sync.Mutex
	next    int64
	pending map[int64]chan message
	handle  Handler
	done    chan struct{}
	err     error
}

func newConn(r io.Reader, w io.Writer, h Handler) *conn {
	c := &conn{w: w, pending: map[int64]chan message{}, handle: h, done: make(chan struct{})}
	go c.read(bufio.NewReader(r))
	return c
}

func (c *conn) read(r *bufio.Reader) {
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
		switch {
		case m.Method != "" && m.ID != nil:
			go c.reply(m)
		case m.Method != "":
			if c.handle != nil {
				c.handle(m.Method, m.Params)
			}
		case m.ID != nil:
			var id int64
			if json.Unmarshal(*m.ID, &id) == nil {
				c.mu.Lock()
				ch := c.pending[id]
				delete(c.pending, id)
				c.mu.Unlock()
				if ch != nil {
					ch <- m
				}
			}
		}
	}
	c.mu.Lock()
	c.err = errors.Join(ErrClosed, err)
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
	close(c.done)
}

func (c *conn) reply(m message) {
	var result any
	var err error
	if c.handle != nil {
		result, err = c.handle(m.Method, m.Params)
	}
	out := map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": result}
	if err != nil {
		out = map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": RPCError{Code: -32601, Message: err.Error()}}
	}
	c.send(out)
}

func (c *conn) send(v any) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	select {
	case <-c.done:
		return ErrClosed
	default:
	}
	return frame.Write(c.w, v)
}

func (c *conn) Notify(method string, params any) error {
	return c.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *conn) Call(ctx context.Context, method string, params, result any) error {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return c.err
	}
	c.next++
	id := c.next
	ch := make(chan message, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return ErrClosed
		}
		if m.Error != nil {
			return m.Error
		}
		if result == nil || len(m.Result) == 0 {
			return nil
		}
		return json.Unmarshal(m.Result, result)
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		c.Notify("$/cancelRequest", map[string]any{"id": id})
		return ctx.Err()
	}
}

func (c *conn) Done() <-chan struct{} { return c.done }
