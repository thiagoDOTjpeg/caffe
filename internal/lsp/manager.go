package lsp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Manager struct {
	Configs []ServerConfig
	Max     int
	OnDiag  func(root string, d PublishDiagnostics)

	mu      sync.Mutex
	clients []*entry
	failed  map[string]time.Time
}

type entry struct {
	key    string
	lang   string
	client *Client
}

const retryAfter = 30 * time.Second

func NewManager(max int, configs ...ServerConfig) *Manager {
	return &Manager{Configs: configs, Max: max, failed: map[string]time.Time{}}
}

func (m *Manager) ConfigFor(path string) (ServerConfig, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	for _, c := range m.Configs {
		for _, e := range c.Extensions {
			if e == ext {
				return c, true
			}
		}
	}
	return ServerConfig{}, false
}

func FindRoot(file, workspace string, markers []string) string {
	dir := filepath.Dir(file)
	for {
		for _, mk := range markers {
			if _, err := os.Stat(filepath.Join(dir, mk)); err == nil {
				return dir
			}
		}
		rel, err := filepath.Rel(workspace, dir)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			return workspace
		}
		dir = filepath.Dir(dir)
	}
}

var ErrNoServer = errors.New("no language server for this file")

func (m *Manager) For(ctx context.Context, file, workspace string) (*Client, string, error) {
	cfg, ok := m.ConfigFor(file)
	if !ok {
		return nil, "", ErrNoServer
	}
	root := FindRoot(file, workspace, cfg.RootMarkers)
	key := cfg.Language + "\x00" + root
	m.mu.Lock()
	for i, e := range m.clients {
		if e.key != key {
			continue
		}
		select {
		case <-e.client.Done():
			m.clients = append(m.clients[:i], m.clients[i+1:]...)
		default:
			m.clients = append(append(m.clients[:i:i], m.clients[i+1:]...), e)
			m.mu.Unlock()
			return e.client, cfg.Language, nil
		}
		break
	}
	if t, ok := m.failed[key]; ok && time.Since(t) < retryAfter {
		m.mu.Unlock()
		return nil, "", errors.New(cfg.Command[0] + " failed to start; retrying in a moment")
	}
	m.mu.Unlock()

	c, err := Start(ctx, cfg, root)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.failed[key] = time.Now()
		return nil, "", err
	}
	delete(m.failed, key)
	m.clients = append(m.clients, &entry{key: key, lang: cfg.Language, client: c})
	if m.OnDiag != nil {
		go func() {
			for {
				select {
				case d := <-c.Diagnostics():
					m.OnDiag(root, d)
				case <-c.Done():
					return
				}
			}
		}()
	}
	for m.Max > 0 && len(m.clients) > m.Max {
		old := m.clients[0]
		m.clients = m.clients[1:]
		go old.client.Shutdown(context.Background())
	}
	return c, cfg.Language, nil
}

func (m *Manager) Live() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.clients)
}

func (m *Manager) Shutdown(ctx context.Context) {
	m.mu.Lock()
	all := m.clients
	m.clients = nil
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, e := range all {
		wg.Add(1)
		go func() { defer wg.Done(); e.client.Shutdown(ctx) }()
	}
	wg.Wait()
}
