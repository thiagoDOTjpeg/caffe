package lsptest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

const Env = "CAFFE_LSP_FAKE"

func read(r *bufio.Reader) ([]byte, error) {
	n := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(line, "Content-Length:"); ok {
			n, _ = strconv.Atoi(strings.TrimSpace(v))
		}
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return b, err
}

func write(w io.Writer, v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintf(w, "Content-Length: %d\r\n\r\n%s", len(b), b)
}

func pos(line int) map[string]any {
	return map[string]any{"start": map[string]int{"line": line}, "end": map[string]int{"line": line}}
}

func Serve(mode string) {
	r := bufio.NewReader(os.Stdin)
	root := ""
	for {
		body, err := read(r)
		if err != nil {
			return
		}
		var m struct {
			ID     *json.RawMessage `json:"id"`
			Method string           `json:"method"`
			Params json.RawMessage  `json:"params"`
		}
		json.Unmarshal(body, &m)
		reply := func(result any) { write(os.Stdout, map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": result}) }
		switch m.Method {
		case "initialize":
			var p struct {
				RootURI string `json:"rootUri"`
			}
			json.Unmarshal(m.Params, &p)
			root = p.RootURI
			write(os.Stdout, map[string]any{"jsonrpc": "2.0", "id": 99, "method": "workspace/configuration", "params": map[string]any{"items": []any{1, 2}}})
			reply(map[string]any{"capabilities": map[string]any{}})
		case "textDocument/didOpen", "textDocument/didChange":
			if mode == "crash" {
				os.Exit(3)
			}
			var p struct {
				TextDocument struct{ URI string } `json:"textDocument"`
			}
			json.Unmarshal(m.Params, &p)
			write(os.Stdout, map[string]any{"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics", "params": map[string]any{
				"uri": p.TextDocument.URI, "diagnostics": []any{
					map[string]any{"range": pos(2), "severity": 1, "message": "undefined: x"},
					map[string]any{"range": pos(0), "severity": 2, "message": "unused import"},
				}}})
		case "textDocument/definition":
			reply(map[string]any{"uri": root + "/a.go", "range": pos(7)})
		case "textDocument/references":
			reply([]any{
				map[string]any{"uri": root + "/a.go", "range": pos(4)},
				map[string]any{"targetUri": "file:///elsewhere/b.go", "targetSelectionRange": pos(3)},
			})
		case "textDocument/documentSymbol":
			reply([]any{map[string]any{"name": "T", "kind": 23, "range": pos(1), "selectionRange": pos(1),
				"children": []any{map[string]any{"name": "M", "kind": 6, "range": pos(3), "selectionRange": pos(3)}}}})
		case "shutdown":
			reply(nil)
		case "exit":
			return
		}
	}
}
