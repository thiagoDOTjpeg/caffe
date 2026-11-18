package service

import (
	"context"
	"testing"
	"time"

	c "caffe/internal/contracts"
)

const tick = 20 * time.Millisecond

func expectChange(t *testing.T, w *Watcher, within time.Duration) Change {
	t.Helper()
	select {
	case ch, ok := <-w.Changes():
		if !ok {
			t.Fatal("watcher closed")
		}
		if ch.Err != nil {
			t.Fatalf("watcher error: %v", ch.Err)
		}
		return ch
	case <-time.After(within):
		t.Fatal("no change reported")
	}
	return Change{}
}

func expectQuiet(t *testing.T, w *Watcher, for_ time.Duration) {
	t.Helper()
	select {
	case ch := <-w.Changes():
		t.Fatalf("unexpected change: %+v", ch)
	case <-time.After(for_):
	}
}

func TestWatcherReportsCommitsFromOtherConnections(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newFixture(t)
	w, err := f.svc.Watch(ctx, tick)
	if err != nil {
		t.Fatal(err)
	}
	expectQuiet(t, w, 8*tick) // nothing happened: nothing reported

	// Reads by the consumer itself are not changes.
	if _, err := f.svc.RunSnapshot(ctx, "1111"); err != nil {
		t.Fatal(err)
	}
	expectQuiet(t, w, 5*tick)

	e := &c.Event{RunID: "11111111-0000-0000-0000-000000000000", Type: c.EventWarning}
	must(t, f.w.AddEvent(ctx, e))
	if ch := expectChange(t, w, 10*tick); ch.Seq != e.Seq {
		t.Errorf("change Seq = %d, want %d", ch.Seq, e.Seq)
	}
	expectQuiet(t, w, 5*tick) // one commit, one change

	// A slow consumer gets one coalesced change carrying the newest Seq.
	var last *c.Event
	for i := 0; i < 5; i++ {
		last = &c.Event{RunID: e.RunID, Type: c.EventWarning}
		must(t, f.w.AddEvent(ctx, last))
		time.Sleep(2 * tick)
	}
	if ch := expectChange(t, w, 10*tick); ch.Seq != last.Seq {
		t.Errorf("coalesced change Seq = %d, want newest %d", ch.Seq, last.Seq)
	}
	expectQuiet(t, w, 5*tick)

	cancel()
	select {
	case _, ok := <-w.Changes():
		if ok {
			t.Error("value after cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("Changes() not closed after cancel")
	}
}

func TestWatcherPauseAndResume(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newFixture(t)
	w, err := f.svc.Watch(ctx, tick)
	if err != nil {
		t.Fatal(err)
	}
	w.Pause()
	time.Sleep(2 * tick) // let an in-flight check finish
	must(t, f.w.AddEvent(ctx, &c.Event{RunID: "11111111-0000-0000-0000-000000000000", Type: c.EventWarning}))
	expectQuiet(t, w, 8*tick)
	// Resuming checks at once and reports what happened while paused.
	w.Resume()
	expectChange(t, w, 3*tick)
	expectQuiet(t, w, 5*tick)
}
