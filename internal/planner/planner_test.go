package planner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"caffe/internal/agents"
	"caffe/internal/runtime"
)

const validPlan = `{
  "goal": "Implement JWT authentication",
  "tasks": [
    {"id": "research", "title": "Analyze", "description": "Look at auth", "role": "researcher", "dependencies": [],
     "acceptance_criteria": ["Identify middleware"]},
    {"id": "implementation", "title": "Implement", "description": "Write it", "role": "developer", "dependencies": ["research"],
     "acceptance_criteria": ["Tokens work"], "complexity": "high"},
    {"id": "review", "title": "Review", "description": "Check it", "role": "reviewer", "dependencies": ["implementation"],
     "acceptance_criteria": ["Findings documented"]}
  ]
}`

func TestParsePlanAcceptsValidPlan(t *testing.T) {
	plan, err := ParsePlan([]byte("Here is the plan:\n```json\n"+validPlan+"\n```\n"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Tasks) != 3 || plan.Tasks[1].Dependencies[0] != "research" || plan.Goal == "" {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestParsePlanReportEnvelope(t *testing.T) {
	withStatus := `{"status":"completed","summary":"s",` + validPlan[1:]
	if _, err := ParsePlan([]byte(withStatus), 10); err != nil {
		t.Fatal(err)
	}
	_, err := ParsePlan([]byte(`{"status":"failed","summary":"repo unreadable"}`), 10)
	if err == nil || !strings.Contains(err.Error(), "repo unreadable") {
		t.Fatalf("err = %v", err)
	}
}

func TestParsePlanRejections(t *testing.T) {
	task := func(fields string) string { return `{"goal":"g","tasks":[` + fields + `]}` }
	ok := `"title":"T","description":"D","role":"developer","dependencies":[],"acceptance_criteria":["c"]`
	cases := map[string]struct{ plan, want string }{
		"missing id":           {task(`{` + ok + `}`), "missing id"},
		"duplicate id":         {task(`{"id":"a",` + ok + `},{"id":"a",` + ok + `}`), "duplicate id"},
		"invalid role":         {task(`{"id":"a","title":"T","description":"D","role":"hacker","dependencies":[],"acceptance_criteria":["c"]}`), "invalid role"},
		"missing dependency":   {task(`{"id":"a","title":"T","description":"D","role":"developer","dependencies":["ghost"],"acceptance_criteria":["c"]}`), "unknown task"},
		"self dependency":      {task(`{"id":"a","title":"T","description":"D","role":"developer","dependencies":["a"],"acceptance_criteria":["c"]}`), "depends on itself"},
		"cycle":                {task(`{"id":"a","title":"T","description":"D","role":"developer","dependencies":["b"],"acceptance_criteria":["c"]},{"id":"b","title":"T","description":"D","role":"developer","dependencies":["a"],"acceptance_criteria":["c"]}`), "cyclic"},
		"empty description":    {task(`{"id":"a","title":"T","description":"  ","role":"developer","dependencies":[],"acceptance_criteria":["c"]}`), "empty description"},
		"no criteria":          {task(`{"id":"a","title":"T","description":"D","role":"developer","dependencies":[],"acceptance_criteria":[]}`), "no acceptance criteria"},
		"blank criterion":      {task(`{"id":"a","title":"T","description":"D","role":"developer","dependencies":[],"acceptance_criteria":[" "]}`), "empty acceptance criterion"},
		"path traversal id":    {task(`{"id":"../../etc",` + ok + `}`), "must match"},
		"shell in id":          {task(`{"id":"a;rm -rf",` + ok + `}`), "must match"},
		"no tasks":             {`{"goal":"g","tasks":[]}`, "no tasks"},
		"unknown field":        {task(`{"id":"a","dependancies":["x"],` + ok + `}`), "unknown field"},
		"not json":             {"I could not plan this.", "no JSON object"},
		"too many tasks":       {task(`{"id":"a",` + ok + `},{"id":"b",` + ok + `}`), "limit is 1"},
		"orchestrator command": {`{"goal":"g","tasks":[],"run":"rm -rf /"}`, "unknown field"},
	}
	for name, tc := range cases {
		max := 10
		if name == "too many tasks" {
			max = 1
		}
		_, err := ParsePlan([]byte(tc.plan), max)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want error containing %q, got %v", name, tc.want, err)
		}
	}
}

func TestAgentProviderPlansThroughRuntime(t *testing.T) {
	rt := runtime.NewFake()
	project := t.TempDir()
	rt.OnPrompt = func(_, kind, cwd, prompt string) {

		fields := strings.Fields(prompt)
		reportPath := fields[len(fields)-1]
		if kind != "codex" || cwd != project || filepath.Dir(reportPath) != project {
			t.Errorf("kind=%s cwd=%s report=%s", kind, cwd, reportPath)
		}
		os.WriteFile(reportPath, []byte(validPlan), 0o644)
	}
	var sessions int
	p := &AgentProvider{
		Agents: &agents.Manager{RT: rt, Poll: 5 * time.Millisecond, Log: zerolog.Nop(), IdleNudgeAfter: 15 * time.Millisecond, IdleGiveUpAfter: 80 * time.Millisecond}, Kind: "codex",
		OnSession: func(runID string, _ agents.Session) {
			if runID == "r1" {
				sessions++
			}
		},
	}
	dir := t.TempDir()
	plan, err := p.GeneratePlan(context.Background(), PlanningInput{RunID: "r1", UserRequest: "Add JWT", ProjectPath: project, Context: "ctx", MaxTasks: 8, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Tasks) != 3 || sessions == 0 {
		t.Fatalf("tasks=%d sessions=%d", len(plan.Tasks), sessions)
	}
	prompt, _ := os.ReadFile(filepath.Join(dir, "prompt.md"))
	for _, want := range []string{"Add JWT", "ctx", "at most 8 tasks"} {
		if !strings.Contains(string(prompt), want) {
			t.Errorf("planner prompt lacks %q", want)
		}
	}
	if len(rt.Live()) != 0 {
		t.Error("planning agent left running")
	}
}

func TestCollectContext(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"cmd/app/main.go", "internal/deep/er/x.go", "node_modules/pkg/index.js", ".git/config", "go.mod"} {
		os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755)
		os.WriteFile(filepath.Join(dir, p), []byte("x"), 0o644)
	}
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# My Project\nhello\n"), 0o644)
	out, err := CollectContext(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"cmd/app/", "go.mod", "internal/deep/", "# My Project"} {
		if !strings.Contains(out, want) {
			t.Errorf("context lacks %q:\n%s", want, out)
		}
	}
	for _, not := range []string{"node_modules", ".git", "main.go", "x.go"} {
		if strings.Contains(out, not) {
			t.Errorf("context must not contain %q:\n%s", not, out)
		}
	}
}
