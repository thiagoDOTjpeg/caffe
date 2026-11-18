// Command caffe is the CLI of Caffè, a local-first orchestrator for
// autonomous coding agents.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"caffe/internal/app"
)

func main() {
	// Ctrl-C / SIGTERM cancel the run in progress: its agents are stopped and
	// the run ends as cancelled. A second signal kills the process outright.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop()
	}()
	if err := app.NewRootCommand().ExecuteContext(ctx); err != nil {
		if !errors.Is(err, app.ErrRunNotCompleted) { // already reported on stdout
			fmt.Fprintln(os.Stderr, "caffe:", err)
		}
		os.Exit(1)
	}
}
