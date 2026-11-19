package edit

import (
	"math/rand"
	"testing"
)

func TestTextRoundTrip(t *testing.T) {
	for _, s := range []string{"", "a", "a\n", "a\nb", "a\nb\n", "\tx\r\ny\r\n", "\n\n"} {
		if got := New(s).Text(); got != s {
			t.Errorf("%q -> %q", s, got)
		}
	}
}

func TestEditing(t *testing.T) {
	b := New("hello\nworld\n")
	b.MoveTo(0, 5, true)
	b.BeginGroup()
	b.Insert(", you")
	b.Newline()
	b.Insert("x")
	b.EndGroup()
	if got := b.Text(); got != "hello, you\nx\nworld\n" {
		t.Fatalf("%q", got)
	}
	b.Backspace()
	b.Backspace()
	if got := b.Text(); got != "hello, you\nworld\n" {
		t.Fatalf("backspace joins lines: %q", got)
	}
	b.MoveTo(1, 0, false)
	b.DeleteLine()
	b.OpenLine(true)
	b.Insert("end")
	b.MoveTo(0, 0, false)
	b.DeleteChar()
	if got := b.Text(); got != "ello, you\nend\n" || !b.Dirty() {
		t.Fatalf("%q dirty=%v", got, b.Dirty())
	}
	for b.Undo() {
	}
	if got := b.Text(); got != "hello\nworld\n" || b.Dirty() {
		t.Fatalf("undo all: %q dirty=%v", got, b.Dirty())
	}
	b.Redo()
	if got := b.Text(); got != "hello, you\nx\nworld\n" {
		t.Fatalf("an insert group is one undo step: %q", got)
	}
	b.MarkSaved()
	if b.Dirty() {
		t.Fatal("dirty after save")
	}
}

func TestFindWraps(t *testing.T) {
	b := New("foo bar\nbaz foo\nqux\n")
	if !b.Find("foo", true) {
		t.Fatal("not found")
	}
	if r, c := b.Cursor(); r != 1 || c != 4 {
		t.Fatalf("first: %d:%d", r, c)
	}
	b.Find("", true)
	if r, c := b.Cursor(); r != 0 || c != 0 {
		t.Fatalf("wrapped: %d:%d", r, c)
	}
	b.Find("", false)
	if r, c := b.Cursor(); r != 1 || c != 4 {
		t.Fatalf("backwards: %d:%d", r, c)
	}
	if b.Find("nope", true) {
		t.Fatal("found nothing")
	}
}

func TestUndoOfEveryOperationRestoresTheText(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	texts := []string{"", "a\n", "one\ntwo\nthree\n", "x\ty\n\nz", "é ü\n"}
	for round := range 2000 {
		b := New(texts[round%len(texts)])
		for range rng.Intn(5) {
			b.MoveTo(rng.Intn(4), rng.Intn(6), true)
			b.Insert("q\nr")
		}
		before := b.Text()
		b.MoveTo(rng.Intn(5), rng.Intn(8), rng.Intn(2) == 0)
		switch rng.Intn(6) {
		case 0:
			b.Insert(string(rune('a' + rng.Intn(26))))
		case 1:
			b.Newline()
		case 2:
			b.Backspace()
		case 3:
			b.DeleteChar()
		case 4:
			b.DeleteLine()
		case 5:
			b.OpenLine(rng.Intn(2) == 0)
		}
		after := b.Text()
		if after == before {
			continue
		}
		b.Undo()
		if got := b.Text(); got != before {
			t.Fatalf("round %d: undo gave %q, want %q", round, got, before)
		}
		b.Redo()
		if got := b.Text(); got != after {
			t.Fatalf("round %d: redo gave %q, want %q", round, got, after)
		}
	}
}
