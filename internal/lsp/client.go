package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"caffe/internal/proctree"
)

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

func (l Location) Path() string { return URIToPath(l.URI) }

type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"`
	Source   string `json:"source"`
	Message  string `json:"message"`
}

type PublishDiagnostics struct {
	URI         string       `json:"uri"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

type Symbol struct {
	Name   string
	Detail string
	Kind   int
	Range  Range
	Depth  int
}

func PathToURI(p string) string {
	p = filepath.ToSlash(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

func URIToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return uri
	}
	p := u.Path
	if runtime.GOOS == "windows" && len(p) > 2 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

type ServerConfig struct {
	Language    string
	Command     []string
	RootMarkers []string
	Extensions  []string
}

var Go = ServerConfig{Language: "go", Command: []string{"gopls"}, RootMarkers: []string{"go.mod"}, Extensions: []string{".go"}}

type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 4096 {
		t.buf = t.buf[len(t.buf)-4096:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

type Client struct {
	Root   string
	stderr *tail
	cmd    *exec.Cmd
	tree   *proctree.Tree
	conn   *conn
	diags  chan PublishDiagnostics
	logs   chan string
	once   sync.Once
}

func Start(ctx context.Context, cfg ServerConfig, root string) (*Client, error) {
	if len(cfg.Command) == 0 {
		return nil, errors.New("no language server command")
	}
	cmd := exec.CommandContext(context.Background(), cfg.Command[0], cfg.Command[1:]...)
	cmd.Dir = root
	stderr := &tail{}
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	tree, err := proctree.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", cfg.Command[0], err)
	}
	c := &Client{Root: root, stderr: stderr, cmd: cmd, tree: tree, diags: make(chan PublishDiagnostics, 64), logs: make(chan string, 16)}
	c.conn = newConn(stdout, stdin, c.handle)
	go func() { cmd.Wait(); tree.Kill() }()
	init := map[string]any{
		"processId": nil,
		"rootUri":   PathToURI(root),
		"workspaceFolders": []map[string]string{
			{"uri": PathToURI(root), "name": filepath.Base(root)},
		},
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"synchronization":    map[string]any{"didSave": false},
				"publishDiagnostics": map[string]any{},
				"definition":         map[string]any{"linkSupport": false},
				"references":         map[string]any{},
				"documentSymbol":     map[string]any{"hierarchicalDocumentSymbolSupport": true},
			},
			"workspace": map[string]any{"workspaceFolders": true, "configuration": false},
		},
	}
	if err := c.conn.Call(ctx, "initialize", init, nil); err != nil {
		c.kill()
		if msg := stderr.String(); msg != "" {
			lines := strings.Split(msg, "\n")
			err = fmt.Errorf("%w: %s", err, lines[len(lines)-1])
		}
		return nil, fmt.Errorf("initialize %s: %w", cfg.Command[0], err)
	}
	if err := c.conn.Notify("initialized", map[string]any{}); err != nil {
		c.kill()
		return nil, err
	}
	return c, nil
}

func (c *Client) handle(method string, params json.RawMessage) (any, error) {
	switch method {
	case "textDocument/publishDiagnostics":
		var p PublishDiagnostics
		if json.Unmarshal(params, &p) == nil {
			select {
			case c.diags <- p:
			default:
			}
		}
	case "window/logMessage", "window/showMessage":
		var p struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(params, &p) == nil {
			select {
			case c.logs <- p.Message:
			default:
			}
		}
	case "workspace/configuration":
		var p struct {
			Items []any `json:"items"`
		}
		json.Unmarshal(params, &p)
		return make([]any, len(p.Items)), nil
	}
	return nil, nil
}

func (c *Client) Diagnostics() <-chan PublishDiagnostics { return c.diags }
func (c *Client) Done() <-chan struct{}                  { return c.conn.Done() }

func (c *Client) DidOpen(path, lang, text string, version int) error {
	return c.conn.Notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{
		"uri": PathToURI(path), "languageId": lang, "version": version, "text": text}})
}

func (c *Client) DidChange(path string, version int, text string) error {
	return c.conn.Notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": PathToURI(path), "version": version},
		"contentChanges": []map[string]any{{"text": text}}})
}

func (c *Client) DidClose(path string) error {
	return c.conn.Notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": PathToURI(path)}})
}

func position(path string, pos Position) map[string]any {
	return map[string]any{"textDocument": map[string]any{"uri": PathToURI(path)}, "position": pos}
}

func decodeLocations(raw json.RawMessage) ([]Location, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var one Location
	if raw[0] == '{' {
		if err := json.Unmarshal(raw, &one); err != nil {
			return nil, err
		}
		return []Location{one}, nil
	}
	var items []struct {
		Location
		TargetURI   string `json:"targetUri"`
		TargetRange Range  `json:"targetSelectionRange"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	out := make([]Location, 0, len(items))
	for _, it := range items {
		if it.TargetURI != "" {
			out = append(out, Location{URI: it.TargetURI, Range: it.TargetRange})
		} else {
			out = append(out, it.Location)
		}
	}
	return out, nil
}

func (c *Client) Definition(ctx context.Context, path string, pos Position) ([]Location, error) {
	var raw json.RawMessage
	if err := c.conn.Call(ctx, "textDocument/definition", position(path, pos), &raw); err != nil {
		return nil, err
	}
	return decodeLocations(raw)
}

func (c *Client) References(ctx context.Context, path string, pos Position) ([]Location, error) {
	p := position(path, pos)
	p["context"] = map[string]bool{"includeDeclaration": true}
	var raw json.RawMessage
	if err := c.conn.Call(ctx, "textDocument/references", p, &raw); err != nil {
		return nil, err
	}
	return decodeLocations(raw)
}

type docSymbol struct {
	Name           string      `json:"name"`
	Detail         string      `json:"detail"`
	Kind           int         `json:"kind"`
	Range          Range       `json:"range"`
	SelectionRange Range       `json:"selectionRange"`
	Children       []docSymbol `json:"children"`
	Location       *Location   `json:"location"`
}

func flatten(in []docSymbol, depth int, out []Symbol) []Symbol {
	for _, s := range in {
		r := s.SelectionRange
		if s.Location != nil {
			r = s.Location.Range
		}
		out = append(out, Symbol{Name: s.Name, Detail: s.Detail, Kind: s.Kind, Range: r, Depth: depth})
		out = flatten(s.Children, depth+1, out)
	}
	return out
}

func (c *Client) DocumentSymbols(ctx context.Context, path string) ([]Symbol, error) {
	var syms []docSymbol
	if err := c.conn.Call(ctx, "textDocument/documentSymbol", map[string]any{"textDocument": map[string]any{"uri": PathToURI(path)}}, &syms); err != nil {
		return nil, err
	}
	return flatten(syms, 0, nil), nil
}

func (c *Client) Shutdown(ctx context.Context) error {
	var err error
	c.once.Do(func() {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		err = c.conn.Call(ctx, "shutdown", nil, nil)
		c.conn.Notify("exit", nil)
		select {
		case <-c.conn.Done():
		case <-ctx.Done():
		}
		c.tree.Kill()
	})
	return err
}

func (c *Client) kill() { c.once.Do(func() { c.tree.Kill() }) }
