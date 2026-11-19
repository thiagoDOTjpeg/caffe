package contracts

import (
	"strings"
	"testing"
)

func TestParseReport(t *testing.T) {
	ok := map[Role]string{
		RoleResearcher: `{"status":"completed","summary":"s","relevant_files":["a.go"],"risks":["r"],"recommendation":"do x"}`,
		RoleDeveloper:  "Sure! Here it is:\n```json\n{\"status\":\"completed\",\"summary\":\"s\",\"modified_files\":[\"a.go\"],\"tests\":[{\"command\":\"go test ./...\",\"exit_code\":0}],\"limitations\":[]}\n```",
		RoleReviewer:   `{"status":"completed","summary":"s","approved":false,"findings":[{"severity":"high","file":"a.go","line":3,"description":"bug"}]}`,
		RoleValidator:  `{"status":"failed","summary":"tests fail","tests":[{"command":"make test","exit_code":2}]}`,
	}
	for role, in := range ok {
		r, err := ParseReport(role, []byte(in))
		if err != nil {
			t.Errorf("%s: %v", role, err)
			continue
		}
		if r.Summary == "" {
			t.Errorf("%s: empty summary", role)
		}
	}
	if r, _ := ParseReport(RoleValidator, []byte(ok[RoleValidator])); r.Tests[0].ExitCode != 2 {
		t.Errorf("exit code lost: %+v", r.Tests)
	}

	bad := map[string]struct {
		role     Role
		in, want string
	}{
		"prose only":            {RoleDeveloper, "All done, tests pass!", "no JSON object"},
		"truncated":             {RoleDeveloper, `{"status":"completed","summ`, "malformed"},
		"bad status":            {RoleDeveloper, `{"status":"success","summary":"s"}`, "status"},
		"no summary":            {RoleDeveloper, `{"status":"completed","summary":" "}`, "empty summary"},
		"reviewer no verdict":   {RoleReviewer, `{"status":"completed","summary":"s"}`, "must set approved"},
		"reject without reason": {RoleReviewer, `{"status":"completed","summary":"s","approved":false,"findings":[]}`, "without any finding"},
		"bad severity":          {RoleReviewer, `{"status":"completed","summary":"s","approved":true,"findings":[{"severity":"meh","description":"d"}]}`, "severity"},
		"finding without text":  {RoleReviewer, `{"status":"completed","summary":"s","approved":true,"findings":[{"severity":"low","description":""}]}`, "no description"},
		"wrong type":            {RoleDeveloper, `{"status":"completed","summary":["s"]}`, "invalid report"},
	}
	for name, tc := range bad {
		if _, err := ParseReport(tc.role, []byte(tc.in)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want error containing %q, got %v", name, tc.want, err)
		}
	}
}

func TestReportCannotCarryCommands(t *testing.T) {
	r, err := ParseReport(RoleDeveloper, []byte(`{"status":"completed","summary":"s","run":"rm -rf /","delegate":[{"role":"developer"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "completed" || r.Summary != "s" {
		t.Fatalf("report = %+v", r)
	}
}
