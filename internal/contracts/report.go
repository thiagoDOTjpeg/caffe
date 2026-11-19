package contracts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	LabelObserved = "executed by the orchestrator"
	LabelClaimed  = "claimed by the agent, not verified"
)

type Report struct {
	Status  string `json:"status"`
	Summary string `json:"summary"`

	RelevantFiles  []string `json:"relevant_files,omitempty"`
	Risks          []string `json:"risks,omitempty"`
	Recommendation string   `json:"recommendation,omitempty"`

	ModifiedFiles []string     `json:"modified_files,omitempty"`
	Tests         []CommandRun `json:"tests,omitempty"`
	Limitations   []string     `json:"limitations,omitempty"`

	Approved *bool     `json:"approved,omitempty"`
	Findings []Finding `json:"findings,omitempty"`
}

type CommandRun struct {
	Command  string `json:"command"`
	ExitCode int    `json:"exit_code"`
	Summary  string `json:"summary,omitempty"`
}

type Finding struct {
	Severity    string `json:"severity"`
	File        string `json:"file,omitempty"`
	Line        int    `json:"line,omitempty"`
	Description string `json:"description"`
}

var severities = map[string]bool{"critical": true, "high": true, "medium": true, "low": true, "info": true}

func ExtractJSON(data []byte) ([]byte, error) {
	start := bytes.IndexByte(data, '{')
	if start < 0 {
		return nil, fmt.Errorf("no JSON object found")
	}
	dec := json.NewDecoder(bytes.NewReader(data[start:]))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("malformed JSON: %w", err)
	}
	return raw, nil
}

func ParseReport(role Role, data []byte) (*Report, error) {
	raw, err := ExtractJSON(data)
	if err != nil {
		return nil, fmt.Errorf("invalid report: %w", err)
	}
	var r Report
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("invalid report: %w", err)
	}
	if r.Status != "completed" && r.Status != "failed" {
		return nil, fmt.Errorf("invalid report: status %q must be completed or failed", r.Status)
	}
	if strings.TrimSpace(r.Summary) == "" {
		return nil, fmt.Errorf("invalid report: empty summary")
	}
	if role == RoleReviewer && r.Status == "completed" {
		if r.Approved == nil {
			return nil, fmt.Errorf("invalid report: reviewer must set approved")
		}
		for i, f := range r.Findings {
			if !severities[f.Severity] {
				return nil, fmt.Errorf("invalid report: finding %d has invalid severity %q", i, f.Severity)
			}
			if strings.TrimSpace(f.Description) == "" {
				return nil, fmt.Errorf("invalid report: finding %d has no description", i)
			}
		}
		if !*r.Approved && len(r.Findings) == 0 {
			return nil, fmt.Errorf("invalid report: changes rejected without any finding")
		}
	}
	return &r, nil
}
