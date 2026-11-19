package studio

import (
	"fmt"
	"strings"

	c "caffe/internal/contracts"
	"caffe/internal/service"
)

// The states of ux-specification §6 that are not a whole screen of their
// own are built here, so every screen words them the same way.

const noRunsLine = ` No runs yet. Press n to start one, or caffe run "…".`

// loadingText is a view's frame while its data is on the way: the title
// carries "loading…", the screen is never blank.
func loadingText(title string) string { return " " + title + " · loading…" }

// noRunsText is the empty state of the run screens.
func noRunsText(title string) string { return " " + title + "\n\n" + noRunsLine }

// noProjectView is the full screen shown when Studio is opened outside a
// Caffè project; only quitting is possible.
func noProjectView(dir string, width, height int) string {
	if width <= 0 || height <= 0 { // size not known yet (or not a terminal)
		width, height = 80, 24
	}
	lines := []string{
		"caffè studio",
		"",
		"No Caffè project here:",
		dir,
		"",
		"Run `caffe init` in the project's directory, then `caffe studio`.",
		"",
		"q or ctrl+q quits",
	}
	var b strings.Builder
	top := max((height-len(lines))/2, 0)
	for i := 0; i < height; i++ {
		text := ""
		if j := i - top; j >= 0 && j < len(lines) {
			text = clip(lines[j], width)
		}
		pad := max((width-len([]rune(text)))/2, 0)
		row := strings.Repeat(" ", pad) + text
		b.WriteString(fmt.Sprintf("%-*s", width, row))
		if i < height-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// interruptedHint says what to do with a run whose owner process died.
func interruptedHint(r service.RunSummary) string {
	if r.Status != c.RunInterrupted {
		return ""
	}
	id := short(r.ID)
	return fmt.Sprintf(" Interrupted: its process stopped · caffe resume %s continues it, caffe cancel %s stops it", id, id)
}

// gap reports whether a refresh's events do not continue the feed: event
// Seqs are SQLite rowids of an append-only table, so the first new event
// must be lastSeq+1. It cannot normally happen; when it does, the feed is
// reloaded from a fresh snapshot (ux-specification §6).
func gap(lastSeq int64, events []c.Event) bool {
	return lastSeq > 0 && len(events) > 0 && events[0].Seq > lastSeq+1
}
