package lsp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"caffe/internal/lsp/lsptest"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv(lsptest.Env); mode != "" {
		lsptest.Serve(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fake(mode string) ServerConfig {
	return ServerConfig{Language: "go", Command: []string{os.Args[0]}, RootMarkers: []string{"go.mod"}, Extensions: []string{".go"}}
}

func TestClientAgainstAFakeServer(t *testing.T) {
	t.Setenv("CAFFE_LSP_FAKE", "ok")
	ctx := context.Background()
	c, err := Start(ctx, fake("ok"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(c.Root, "a.go")
	if err := c.DidOpen(path, "go", "package a\n", 1); err != nil {
		t.Fatal(err)
	}
	select {
	case d := <-c.Diagnostics():
		if URIToPath(d.URI) != path || len(d.Diagnostics) != 2 || d.Diagnostics[0].Message != "undefined: x" || d.Diagnostics[0].Range.Start.Line != 2 {
			t.Fatalf("diagnostics %+v", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no diagnostics")
	}
	def, err := c.Definition(ctx, path, Position{})
	if err != nil || len(def) != 1 || def[0].Range.Start.Line != 7 {
		t.Fatalf("definition %+v %v", def, err)
	}
	refs, err := c.References(ctx, path, Position{})
	if err != nil || len(refs) != 2 || refs[1].URI != "file:///elsewhere/b.go" || refs[1].Range.Start.Line != 3 {
		t.Fatalf("references %+v %v", refs, err)
	}
	syms, err := c.DocumentSymbols(ctx, path)
	if err != nil || len(syms) != 2 || syms[0].Name != "T" || syms[1].Depth != 1 || syms[1].Range.Start.Line != 3 {
		t.Fatalf("symbols %+v %v", syms, err)
	}
	if err := c.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("still connected after shutdown")
	}
	if _, err := c.Definition(ctx, path, Position{}); err == nil {
		t.Fatal("call after shutdown")
	}
}

func TestServerCrashEndsPendingCalls(t *testing.T) {
	t.Setenv("CAFFE_LSP_FAKE", "crash")
	ctx := context.Background()
	m := NewManager(3, fake("crash"))
	ws := t.TempDir()
	c, _, err := m.For(ctx, filepath.Join(ws, "a.go"), ws)
	if err != nil {
		t.Fatal(err)
	}
	c.DidOpen(filepath.Join(ws, "a.go"), "go", "", 1)
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("crash not noticed")
	}
	if _, err := c.Definition(ctx, "a.go", Position{}); err == nil {
		t.Fatal("call on a crashed server")
	}
	t.Setenv("CAFFE_LSP_FAKE", "ok")
	c2, _, err := m.For(ctx, filepath.Join(ws, "a.go"), ws)
	if err != nil || c2 == c {
		t.Fatalf("not restarted: %v", err)
	}
	m.Shutdown(ctx)
}

func TestManagerOneServerPerRootWithCap(t *testing.T) {
	t.Setenv("CAFFE_LSP_FAKE", "ok")
	ctx := context.Background()
	m := NewManager(2, fake("ok"))
	defer m.Shutdown(ctx)
	ws := t.TempDir()
	var roots []string
	for _, n := range []string{"a", "b", "c"} {
		dir := filepath.Join(ws, n)
		os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
		os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module "+n), 0o644)
		roots = append(roots, dir)
	}
	c1, _, _ := m.For(ctx, filepath.Join(roots[0], "sub", "x.go"), ws)
	again, _, _ := m.For(ctx, filepath.Join(roots[0], "y.go"), ws)
	if c1 != again || c1.Root != roots[0] {
		t.Fatalf("root %s, same %v", c1.Root, c1 == again)
	}
	m.For(ctx, filepath.Join(roots[1], "x.go"), ws)
	m.For(ctx, filepath.Join(roots[2], "x.go"), ws)
	if m.Live() != 2 {
		t.Fatalf("%d servers live", m.Live())
	}
	select {
	case <-c1.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("least recently used server not shut down")
	}
	if _, _, err := m.For(ctx, filepath.Join(ws, "README.md"), ws); err != ErrNoServer {
		t.Fatalf("markdown: %v", err)
	}
}

func TestURIRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "with space", "ü.go")
	if got := URIToPath(PathToURI(p)); got != p {
		t.Fatalf("%q -> %q -> %q", p, PathToURI(p), got)
	}
}

func TestRealGopls(t *testing.T) {
	if os.Getenv("CAFFE_LSP_REAL") != "1" {
		t.Skip("set CAFFE_LSP_REAL=1 to run against gopls")
	}
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644)
	src := "package demo\n\nfunc Hello() string { return \"hi\" }\n\nfunc Use() string { return Hello() + undefinedThing }\n"
	path := filepath.Join(dir, "a.go")
	os.WriteFile(path, []byte(src), 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c, err := Start(ctx, Go, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown(ctx)
	c.DidOpen(path, "go", src, 1)
	deadline := time.After(45 * time.Second)
	for got := false; !got; {
		select {
		case d := <-c.Diagnostics():
			for _, x := range d.Diagnostics {
				got = got || strings.Contains(x.Message, "undefinedThing")
			}
		case <-deadline:
			t.Fatal("no diagnostic from gopls")
		}
	}
	def, err := c.Definition(ctx, path, Position{Line: 4, Character: 28})
	if err != nil || len(def) != 1 || def[0].Range.Start.Line != 2 {
		t.Fatalf("definition %+v %v", def, err)
	}
	syms, err := c.DocumentSymbols(ctx, path)
	if err != nil || len(syms) != 2 {
		t.Fatalf("symbols %+v %v", syms, err)
	}
}
