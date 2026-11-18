package core

import (
	"testing"

	"github.com/rs/zerolog"

	"caffe/internal/runtime"
	"caffe/internal/runtime/herdr"
)

type attachableFake struct{ *runtime.Fake }

func (attachableFake) AttachCommand(agent string) ([]string, error) {
	return []string{"rt", "attach", agent}, nil
}

func TestAttachHintUsesRuntimeCapability(t *testing.T) {
	hint := func(rt runtime.Runtime) string {
		return (&session{Orchestrator: &Orchestrator{RT: rt}}).attachHint("c1-developer")
	}
	// The CLI's message for Herdr is what it was before the capability existed.
	if got := hint(herdr.New("herdr", "caffe", 0, zerolog.Nop())); got != "herdr --session caffe agent attach c1-developer" {
		t.Errorf("herdr hint = %q", got)
	}
	if got := hint(herdr.New("herdr", "", 0, zerolog.Nop())); got != "herdr agent attach c1-developer" {
		t.Errorf("herdr hint without session = %q", got)
	}
	if got := hint(attachableFake{runtime.NewFake()}); got != "rt attach c1-developer" {
		t.Errorf("attachable runtime hint = %q", got)
	}
	// A runtime without the capability gets no invented command.
	if got := hint(runtime.NewFake()); got == "" || got == "rt attach c1-developer" {
		t.Errorf("non-attachable runtime hint = %q", got)
	}
}
