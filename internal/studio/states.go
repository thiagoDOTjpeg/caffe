package studio

import (
	"fmt"
	"strings"

	c "caffe/internal/contracts"
	"caffe/internal/service"
)

const noRunsLine = ` No runs yet. Press n to start one, or caffe run "…".`

func loadingText(title string) string { return " " + title + " · loading…" }

func noRunsText(title string) string { return " " + title + "\n\n" + noRunsLine }

func noProjectView(dir string, width, height int) string {
	if width <= 0 || height <= 0 {
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

func interruptedHint(r service.RunSummary) string {
	if r.Status != c.RunInterrupted {
		return ""
	}
	id := short(r.ID)
	return fmt.Sprintf(" Interrupted: its process stopped · caffe resume %s continues it, caffe cancel %s stops it", id, id)
}

func gap(lastSeq int64, events []c.Event) bool {
	return lastSeq > 0 && len(events) > 0 && events[0].Seq > lastSeq+1
}
