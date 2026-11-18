package studio

import (
	"strings"
	"testing"
)

const sampleReport = "# Run 0112dd6a-1\n\n" +
	"- **Request:** Add a hello file\n" +
	"- **Status:** completed\n" +
	"- **Project:** `/p` (base commit `7f5d944`)\n\n" +
	"## Tasks\n\n" +
	"| Task | Role | Status | Attempts | Duration | Depends on |\n" +
	"|---|---|---|---|---|---|\n" +
	"| research | researcher | completed | 1/3 | 55s |  |\n" +
	"| impl | developer | completed | 1/3 | 39s | research |\n\n" +
	"### impl — Add hello.txt\n\n" +
	"developer, **completed**\n\n\n" +
	"- Branch: `caffe/0112dd6a/impl` (not merged; merging is your decision)\n" +
	"- claimed by the agent, not verified: `go test ./...` → exit 0\n"

func TestMarkdownText(t *testing.T) {
	var got []string
	var headings []string
	for _, l := range markdownText(sampleReport) {
		got = append(got, l.text)
		if l.heading {
			headings = append(headings, strings.TrimSpace(l.text))
		}
	}
	text := strings.Join(got, "\n")
	want := strings.Join([]string{
		" RUN 0112DD6A-1",
		" ",
		"  · Request: Add a hello file",
		"  · Status: completed",
		"  · Project: /p (base commit 7f5d944)",
		" ",
		" TASKS",
		" ",
		"  Task      Role        Status     Attempts  Duration  Depends on",
		"  research  researcher  completed  1/3       55s",
		"  impl      developer   completed  1/3       39s       research",
		" ",
		" impl — Add hello.txt",
		" ",
		" developer, completed",
		" ",
		"  · Branch: caffe/0112dd6a/impl (not merged; merging is your decision)",
		"  · claimed by the agent, not verified: go test ./... → exit 0",
		" ",
	}, "\n")
	if text != want {
		t.Errorf("got:\n%s\nwant:\n%s", text, want)
	}
	if strings.Join(headings, "|") != "RUN 0112DD6A-1|TASKS|impl — Add hello.txt" {
		t.Errorf("headings %q", headings)
	}
}

func TestReportViewOpensScrollsAndCloses(t *testing.T) {
	m := overviewModel(t)
	f := m.src.(*fakeSource)
	f.set(func(f *fakeSource) { f.report = sampleReport + strings.Repeat("- more\n", 80) })
	m, cmd := press(m, "r")
	if m.report == nil || m.scope() != "report" {
		t.Fatal("r does not open the report")
	}
	if !strings.Contains(m.View().Content, "loading…") {
		t.Error("no loading state")
	}
	m = drive(t, m, cmd)
	golden(t, "report-120x40", m.View().Content)
	if v := m.View().Content; !strings.Contains(v, "TASKS") || !strings.Contains(v, "impl      developer") {
		t.Error("report not rendered")
	}
	m, _ = press(m, "G")
	if v := m.View().Content; strings.Contains(v, "RUN 0112DD6A") || !strings.Contains(v, "· more") {
		t.Error("G does not reach the end")
	}
	m, _ = press(m, "g", "esc")
	if m.report != nil || m.screen != screenOverview {
		t.Error("esc does not close the report")
	}
}

func TestReportFromLeaderAndError(t *testing.T) {
	m := overviewModel(t) // the fake has no report: Report fails
	m, cmd := press(m, "space", "r", "o")
	if m.report == nil {
		t.Fatal("space r o does not open the report")
	}
	m = drive(t, m, cmd)
	if v := m.View().Content; !strings.Contains(v, "not found") {
		t.Error("report error not shown")
	}
}
