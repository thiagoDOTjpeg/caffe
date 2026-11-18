// Package herdr drives Herdr through its CLI (verified against herdr 0.9.5).
package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"caffe/internal/runtime"
)

// Error is a failed Herdr command. Code is Herdr's own error code when it
// printed a JSON error envelope, otherwise empty.
type Error struct {
	Op       string
	Code     string
	Message  string
	ExitCode int
	Err      error // underlying cause: context error, exec error
}

func (e *Error) Error() string {
	msg := e.Message
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	if e.Code != "" {
		return fmt.Sprintf("herdr %s: %s: %s", e.Op, e.Code, msg)
	}
	return fmt.Sprintf("herdr %s: exit %d: %s", e.Op, e.ExitCode, msg)
}

func (e *Error) Unwrap() error { return e.Err }

// cli runs the herdr executable. Arguments are always passed as a vector;
// nothing ever goes through a shell.
type cli struct {
	exe     string
	session string
	timeout time.Duration
	log     zerolog.Logger
}

type envelope struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// run executes `herdr [--session S] args...` and returns raw stdout.
// sensitive lists argument indexes (prompt text) that must not be logged.
func (c *cli) run(ctx context.Context, op string, args []string, sensitive ...int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	full := args
	if c.session != "" {
		full = append([]string{"--session", c.session}, args...)
	}
	cmd := exec.CommandContext(ctx, c.exe, full...)
	cmd.WaitDelay = 2 * time.Second // do not hang on inherited pipes after a kill
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	start := time.Now()
	err := cmd.Run()
	logged := append([]string(nil), args...)
	for _, i := range sensitive {
		if i < len(logged) {
			logged[i] = fmt.Sprintf("<%d bytes>", len(logged[i]))
		}
	}
	ev := c.log.Debug().Str("op", op).Strs("args", logged).Dur("duration", time.Since(start))

	if err == nil {
		ev.Msg("herdr ok")
		return stdout.Bytes(), nil
	}
	herr := &Error{Op: op, ExitCode: -1, Err: err}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		herr.ExitCode = exitErr.ExitCode()
	}
	// Herdr reports failures as a JSON envelope, on stdout or stderr.
	for _, buf := range [][]byte{stderr.Bytes(), stdout.Bytes()} {
		var env envelope
		if json.Unmarshal(bytes.TrimSpace(buf), &env) == nil && env.Error != nil {
			herr.Code, herr.Message = env.Error.Code, env.Error.Message
			break
		}
	}
	if herr.Message == "" {
		herr.Message = runtime.Redact(tail(stderr.String(), 500))
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		herr.Err = ctxErr // timeout or cancellation, not a plain exit
	}
	ev.Str("code", herr.Code).Int("exit_code", herr.ExitCode).Str("error", herr.Message).Msg("herdr failed")
	return nil, herr
}

// result runs a command and decodes the `result` object of its JSON envelope.
func (c *cli) result(ctx context.Context, op string, out any, args []string, sensitive ...int) error {
	raw, err := c.run(ctx, op, args, sensitive...)
	if err != nil {
		return err
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return &Error{Op: op, Message: "unparseable output: " + runtime.Redact(tail(string(raw), 200)), Err: err}
	}
	if env.Error != nil { // some failures exit 0
		return &Error{Op: op, Code: env.Error.Code, Message: env.Error.Message}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return &Error{Op: op, Message: "unexpected result shape", Err: err}
	}
	return nil
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}
