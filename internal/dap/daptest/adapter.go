package daptest

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"sync"

	"caffe/internal/frame"
)

const Env = "CAFFE_DAP_FAKE"

func Serve(args []string) {
	addr := ""
	for i, a := range args {
		if a == "--client-addr" && i+1 < len(args) {
			addr = args[i+1]
		}
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		os.Exit(2)
	}
	defer conn.Close()
	var wmu sync.Mutex
	seq := 0
	send := func(v map[string]any) {
		wmu.Lock()
		defer wmu.Unlock()
		seq++
		v["seq"] = seq
		frame.Write(conn, v)
	}
	event := func(name string, body any) { send(map[string]any{"type": "event", "event": name, "body": body}) }
	r := bufio.NewReader(conn)
	file, line := "", 0
	for {
		b, err := frame.Read(r)
		if err != nil {
			return
		}
		var m struct {
			Seq       int             `json:"seq"`
			Command   string          `json:"command"`
			Arguments json.RawMessage `json:"arguments"`
		}
		json.Unmarshal(b, &m)
		reply := func(body any) {
			send(map[string]any{"type": "response", "request_seq": m.Seq, "command": m.Command, "success": true, "body": body})
		}
		switch m.Command {
		case "initialize":
			reply(map[string]any{"supportsConfigurationDoneRequest": true})
		case "launch":
			reply(nil)
			event("initialized", nil)
		case "setBreakpoints":
			var a struct {
				Source      struct{ Path string } `json:"source"`
				Breakpoints []struct{ Line int }  `json:"breakpoints"`
			}
			json.Unmarshal(m.Arguments, &a)
			var out []map[string]any
			for _, bp := range a.Breakpoints {
				out = append(out, map[string]any{"line": bp.Line, "verified": true})
				file, line = a.Source.Path, bp.Line
			}
			reply(map[string]any{"breakpoints": out})
		case "configurationDone":
			reply(nil)
			if line > 0 {
				event("stopped", map[string]any{"reason": "breakpoint", "threadId": 1})
			} else {
				event("output", map[string]any{"category": "stdout", "output": "PASS\n"})
				event("terminated", nil)
			}
		case "threads":
			reply(map[string]any{"threads": []any{map[string]any{"id": 1, "name": "main"}}})
		case "stackTrace":
			reply(map[string]any{"stackFrames": []any{
				map[string]any{"id": 1000, "name": "main.greet", "line": line, "source": map[string]any{"path": file}},
				map[string]any{"id": 1001, "name": "main.TestHello", "line": 12, "source": map[string]any{"path": file}},
			}})
		case "scopes":
			reply(map[string]any{"scopes": []any{map[string]any{"name": "Locals", "variablesReference": 1}}})
		case "variables":
			var a struct {
				Ref int `json:"variablesReference"`
			}
			json.Unmarshal(m.Arguments, &a)
			if a.Ref == 1 {
				reply(map[string]any{"variables": []any{
					map[string]any{"name": "n", "type": "string", "value": `"from herd"`},
					map[string]any{"name": "cfg", "type": "main.Config", "value": "{...}", "variablesReference": 2},
				}})
			} else {
				reply(map[string]any{"variables": []any{map[string]any{"name": "Port", "type": "int", "value": "8080"}}})
			}
		case "next", "stepIn", "stepOut":
			reply(nil)
			line++
			event("stopped", map[string]any{"reason": "step", "threadId": 1})
		case "continue":
			reply(map[string]any{"allThreadsContinued": true})
			event("output", map[string]any{"category": "stdout", "output": "PASS\n"})
			event("terminated", nil)
		case "evaluate":
			var a struct{ Expression string }
			json.Unmarshal(m.Arguments, &a)
			reply(map[string]any{"result": "value of " + a.Expression})
		case "disconnect":
			reply(nil)
			return
		default:
			send(map[string]any{"type": "response", "request_seq": m.Seq, "command": m.Command, "success": false, "message": "unsupported"})
		}
	}
}
