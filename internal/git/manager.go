// Package git isolates agent changes in worktrees. It never merges and never
// deletes a branch: branches are the output of a run.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Manager struct {
	repo        string // main checkout
	worktreeDir string // every worktree this manager creates or removes lives under it
}

// New verifies that repo is a Git work tree with at least one commit.
func New(ctx context.Context, repo, worktreeDir string) (*Manager, error) {
	m := &Manager{repo: repo, worktreeDir: filepath.Clean(worktreeDir)}
	top, err := m.git(ctx, repo, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("%s is not a Git repository: %w", repo, err)
	}
	// Agents must not be able to wander into a parent repository's files.
	if resolved, _ := filepath.EvalSymlinks(repo); filepath.Clean(top) != filepath.Clean(resolved) {
		return nil, fmt.Errorf("%s is inside the Git repository %s; caffe must run at the repository root", repo, top)
	}
	if _, err := m.Head(ctx); err != nil {
		return nil, fmt.Errorf("%s has no commits yet; worktrees need one: %w", repo, err)
	}
	return m, nil
}

// git runs one git command in dir, without a shell.
func (m *Manager) git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	// Agent-controlled repositories must not be able to prompt or page.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// Head returns the commit the main checkout is on.
func (m *Manager) Head(ctx context.Context) (string, error) {
	return m.git(ctx, m.repo, "rev-parse", "--verify", "HEAD^{commit}")
}

// Branch returns the branch checked out in dir ("HEAD" when detached).
func (m *Manager) Branch(ctx context.Context, dir string) (string, error) {
	return m.git(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
}

func (m *Manager) branchExists(ctx context.Context, branch string) bool {
	_, err := m.git(ctx, m.repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// owned reports whether path is strictly inside the managed worktree directory.
func (m *Manager) owned(path string) bool {
	rel, err := filepath.Rel(m.worktreeDir, filepath.Clean(path))
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

// CreateWorktree creates branch at base and checks it out in a new directory
// worktreeDir/name. It refuses to reuse an existing branch or directory.
func (m *Manager) CreateWorktree(ctx context.Context, name, branch, base string) (string, error) {
	path := filepath.Join(m.worktreeDir, name)
	if !m.owned(path) {
		return "", fmt.Errorf("worktree name %q escapes %s", name, m.worktreeDir)
	}
	if _, err := m.git(ctx, m.repo, "check-ref-format", "--branch", branch); err != nil {
		return "", fmt.Errorf("invalid branch name %q: %w", branch, err)
	}
	if m.branchExists(ctx, branch) {
		return "", fmt.Errorf("branch %s already exists", branch)
	}
	if _, err := os.Lstat(path); err == nil {
		return "", fmt.Errorf("worktree path %s already exists", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if _, err := m.git(ctx, m.repo, "worktree", "add", "-b", branch, path, base); err != nil {
		return "", err
	}
	return path, nil
}

type Worktree struct {
	Path   string
	Branch string
	Head   string
}

// ListWorktrees returns the worktrees under the managed directory.
func (m *Manager) ListWorktrees(ctx context.Context) ([]Worktree, error) {
	out, err := m.git(ctx, m.repo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var all []Worktree
	for _, block := range strings.Split(out, "\n\n") {
		var w Worktree
		for _, line := range strings.Split(block, "\n") {
			k, v, _ := strings.Cut(line, " ")
			switch k {
			case "worktree":
				w.Path = v
			case "HEAD":
				w.Head = v
			case "branch":
				w.Branch = strings.TrimPrefix(v, "refs/heads/")
			}
		}
		if w.Path != "" && m.owned(w.Path) {
			all = append(all, w)
		}
	}
	return all, nil
}

// IsDirty reports uncommitted changes (including untracked files) in dir.
func (m *Manager) IsDirty(ctx context.Context, dir string) (bool, error) {
	out, err := m.git(ctx, dir, "status", "--porcelain")
	return out != "", err
}

// Status is a fingerprint of a checkout's uncommitted state, used to notice a
// read-only agent that modified files anyway.
func (m *Manager) Status(ctx context.Context, dir string) (string, error) {
	return m.git(ctx, dir, "status", "--porcelain")
}

// CommitAll commits everything in a worktree, so that an agent's work is on
// its branch even if it forgot to commit. Returns false when there was
// nothing to commit.
func (m *Manager) CommitAll(ctx context.Context, worktree, message string) (bool, error) {
	if !m.owned(worktree) {
		return false, fmt.Errorf("refusing to commit outside managed worktrees: %s", worktree)
	}
	dirty, err := m.IsDirty(ctx, worktree)
	if err != nil || !dirty {
		return false, err
	}
	if _, err := m.git(ctx, worktree, "add", "-A"); err != nil {
		return false, err
	}
	args := []string{"commit", "--no-verify", "-m", message}
	if _, err := m.git(ctx, worktree, "config", "user.email"); err != nil {
		// No identity configured: commit as the orchestrator instead of failing.
		args = append([]string{"-c", "user.name=caffe", "-c", "user.email=caffe@localhost"}, args...)
	}
	_, err = m.git(ctx, worktree, args...)
	return err == nil, err
}

// Diff returns the committed changes of a worktree's branch relative to base.
func (m *Manager) Diff(ctx context.Context, worktree, base string) (string, error) {
	return m.git(ctx, worktree, "diff", "--no-color", "--no-ext-diff", base, "HEAD")
}

// ChangedFiles lists the files that differ between base and the worktree HEAD.
func (m *Manager) ChangedFiles(ctx context.Context, worktree, base string) ([]string, error) {
	out, err := m.git(ctx, worktree, "diff", "--name-only", base, "HEAD")
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

// ListFiles lists the tracked and untracked, not ignored, files of a
// checkout, slash-separated and relative to dir.
func (m *Manager) ListFiles(ctx context.Context, dir string) ([]string, error) {
	out, err := m.git(ctx, dir, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	return splitZ(out), err
}

// CommittedChanges maps each file changed between base and HEAD to its
// status letter (A, M, D, T).
func (m *Manager) CommittedChanges(ctx context.Context, dir, base string) (map[string]string, error) {
	out, err := m.git(ctx, dir, "diff", "-z", "--name-status", "--no-renames", base, "HEAD")
	if err != nil {
		return nil, err
	}
	f := splitZ(out)
	ch := make(map[string]string, len(f)/2)
	for i := 0; i+1 < len(f); i += 2 {
		ch[f[i+1]] = f[i][:1]
	}
	return ch, nil
}

// StatusEntry is one file of `git status --porcelain=v2`.
type StatusEntry struct {
	XY   string // index and worktree status ("M.", ".M", "??", "UU", …)
	Path string
}

// WorkStatus is a checkout's branch and uncommitted files, untracked
// included.
type WorkStatus struct {
	Branch  string // "(detached)" when detached
	Entries []StatusEntry
}

// Status2 parses `git status --porcelain=v2 --branch -z`. Renames are off,
// so every entry has a single path.
func (m *Manager) Status2(ctx context.Context, dir string) (*WorkStatus, error) {
	out, err := m.git(ctx, dir, "status", "-z", "--porcelain=v2", "--branch", "--no-renames", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	ws := &WorkStatus{}
	for _, e := range splitZ(out) {
		switch {
		case strings.HasPrefix(e, "# branch.head "):
			ws.Branch = strings.TrimPrefix(e, "# branch.head ")
		case strings.HasPrefix(e, "1 "):
			if f := strings.SplitN(e, " ", 9); len(f) == 9 {
				ws.Entries = append(ws.Entries, StatusEntry{f[1], f[8]})
			}
		case strings.HasPrefix(e, "u "):
			if f := strings.SplitN(e, " ", 11); len(f) == 11 {
				ws.Entries = append(ws.Entries, StatusEntry{f[1], f[10]})
			}
		case strings.HasPrefix(e, "? "):
			ws.Entries = append(ws.Entries, StatusEntry{"??", e[2:]})
		}
	}
	return ws, nil
}

func splitZ(s string) []string {
	s = strings.TrimRight(s, "\x00")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\x00")
}

// MergeBase returns the commit where two refs of a worktree diverged.
func (m *Manager) MergeBase(ctx context.Context, worktree, a, b string) (string, error) {
	return m.git(ctx, worktree, "merge-base", a, b)
}

// Restore discards uncommitted changes in a managed worktree. Used only to
// undo edits by an agent that was supposed to be read-only, on a tree whose
// legitimate content is already committed.
func (m *Manager) Restore(ctx context.Context, worktree string) error {
	if !m.owned(worktree) {
		return fmt.Errorf("refusing to restore %s: not under %s", worktree, m.worktreeDir)
	}
	if _, err := m.git(ctx, worktree, "reset", "--hard", "HEAD"); err != nil {
		return err
	}
	_, err := m.git(ctx, worktree, "clean", "-fd")
	return err
}

// RemoveWorktree deletes a worktree checkout. Git refuses if it holds
// uncommitted changes, which is the safety net: nothing is forced, and the
// branch is kept.
func (m *Manager) RemoveWorktree(ctx context.Context, path string) error {
	if !m.owned(path) {
		return fmt.Errorf("refusing to remove %s: not under %s", path, m.worktreeDir)
	}
	_, err := m.git(ctx, m.repo, "worktree", "remove", path)
	return err
}

// ExcludeLocally adds patterns to .git/info/exclude so that orchestrator files
// never show up as untracked, without touching the tracked .gitignore.
func (m *Manager) ExcludeLocally(ctx context.Context, patterns ...string) error {
	p, err := m.git(ctx, m.repo, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(m.repo, p)
	}
	existing, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var add []string
	for _, pat := range patterns {
		if !strings.Contains("\n"+string(existing)+"\n", "\n"+pat+"\n") {
			add = append(add, pat)
		}
	}
	if len(add) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		add = append([]string{""}, add...)
	}
	_, werr := f.WriteString(strings.Join(add, "\n") + "\n")
	return errors.Join(werr, f.Close())
}
