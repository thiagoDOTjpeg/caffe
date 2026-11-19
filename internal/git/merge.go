package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func (m *Manager) Repo() string { return m.repo }

func (m *Manager) Identity(ctx context.Context) string {
	name, _ := m.git(ctx, m.repo, "config", "user.name")
	email, _ := m.git(ctx, m.repo, "config", "user.email")
	switch {
	case name != "" && email != "":
		return name + " <" + email + ">"
	case email != "":
		return email
	}
	return name
}

func (m *Manager) ShortStat(ctx context.Context, target, branch string) (string, error) {
	return m.git(ctx, m.repo, "diff", "--shortstat", target+"..."+branch)
}

func (m *Manager) Subjects(ctx context.Context, base, branch string) ([]string, error) {
	out, err := m.git(ctx, m.repo, "log", "--format=%s", base+".."+branch)
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

func (m *Manager) IsAncestor(ctx context.Context, a, b string) bool {
	_, err := m.git(ctx, m.repo, "merge-base", "--is-ancestor", a, b)
	return err == nil
}

func (m *Manager) MergeTree(ctx context.Context, target, branch string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "merge-tree", "--write-tree", "--name-only", "--no-messages", target, branch)
	cmd.Dir = m.repo
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return nil, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
		if len(lines) < 2 {
			return []string{"(conflicts)"}, nil
		}
		return lines[1:], nil
	}
	return nil, fmt.Errorf("git merge-tree: %w: %s", err, strings.TrimSpace(stderr.String()))
}

func MergeArgs(branch string) []string {
	return []string{"merge", "--no-ff", "--no-edit", branch}
}

func (m *Manager) Merge(ctx context.Context, branch string) error {
	if _, err := m.git(ctx, m.repo, MergeArgs(branch)...); err != nil {
		_, _ = m.git(context.WithoutCancel(ctx), m.repo, "merge", "--abort")
		return err
	}
	return nil
}
