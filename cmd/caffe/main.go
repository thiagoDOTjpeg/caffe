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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop()
	}()
	if err := app.NewRootCommand().ExecuteContext(ctx); err != nil {
		if !errors.Is(err, app.ErrRunNotCompleted) {
			fmt.Fprintln(os.Stderr, "caffe:", err)
		}
		os.Exit(1)
	}
}
