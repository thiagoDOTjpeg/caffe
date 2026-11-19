package service

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type SearchHit struct {
	Path string
	Line int
	Text string
}

type SearchResult struct {
	Hits      []SearchHit
	Truncated bool
	Engine    string
}

var rgPath = "rg"

func (s *Service) Search(ctx context.Context, workspaceID, query string, limit int) (*SearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("empty search")
	}
	root, err := s.workspaceRoot(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if _, err := exec.LookPath(rgPath); err == nil {
		cmd := exec.CommandContext(ctx, rgPath, "--json", "--fixed-strings", "--smart-case", "--max-columns", "500", "--", query, ".")
		r, err := run(cmd, root, func(out io.Reader) (*SearchResult, error) { return parseRg(out, limit) })
		if r != nil {
			r.Engine = "rg"
		}
		return r, err
	}
	icase := []string{}
	if strings.ToLower(query) == query {
		icase = []string{"-i"}
	}
	args := append([]string{"grep", "-z", "-n", "-I", "--untracked", "--exclude-standard", "-F"}, icase...)
	cmd := exec.CommandContext(ctx, "git", append(args, "-e", query)...)
	r, err := run(cmd, root, func(out io.Reader) (*SearchResult, error) { return parseGitGrep(out, limit) })
	if r != nil {
		r.Engine = "git grep"
	}
	return r, err
}

func run(cmd *exec.Cmd, dir string, parse func(io.Reader) (*SearchResult, error)) (*SearchResult, error) {
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	res, perr := parse(out)
	if res != nil && res.Truncated {
		_ = cmd.Process.Kill()
	}
	io.Copy(io.Discard, out)
	werr := cmd.Wait()
	if perr != nil {
		return nil, perr
	}
	var exit *exec.ExitError
	if werr != nil && !res.Truncated && !(errors.As(werr, &exit) && exit.ExitCode() == 1) {
		return nil, fmt.Errorf("%s: %w: %s", filepath.Base(cmd.Path), werr, strings.TrimSpace(stderr.String()))
	}
	return res, nil
}

type rgText struct {
	Text  *string `json:"text"`
	Bytes string  `json:"bytes"`
}

func (t rgText) String() string {
	if t.Text != nil {
		return *t.Text
	}
	b, _ := base64.StdEncoding.DecodeString(t.Bytes)
	return strings.ToValidUTF8(string(b), "�")
}

func parseRg(r io.Reader, limit int) (*SearchResult, error) {
	res := &SearchResult{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	for sc.Scan() {
		var msg struct {
			Type string `json:"type"`
			Data struct {
				Path  rgText `json:"path"`
				Lines rgText `json:"lines"`
				Line  int    `json:"line_number"`
			} `json:"data"`
		}
		if json.Unmarshal(sc.Bytes(), &msg) != nil || msg.Type != "match" {
			continue
		}
		if len(res.Hits) == limit {
			res.Truncated = true
			break
		}
		p := strings.TrimPrefix(filepath.ToSlash(msg.Data.Path.String()), "./")
		res.Hits = append(res.Hits, SearchHit{Path: p, Line: msg.Data.Line, Text: strings.TrimRight(msg.Data.Lines.String(), "\r\n")})
	}
	return res, sc.Err()
}

func parseGitGrep(r io.Reader, limit int) (*SearchResult, error) {
	res := &SearchResult{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	for sc.Scan() {
		f := strings.SplitN(sc.Text(), "\x00", 3)
		if len(f) != 3 {
			continue
		}
		n, err := strconv.Atoi(f[1])
		if err != nil {
			continue
		}
		if len(res.Hits) == limit {
			res.Truncated = true
			break
		}
		res.Hits = append(res.Hits, SearchHit{Path: f[0], Line: n, Text: strings.ToValidUTF8(strings.TrimRight(f[2], "\r"), "�")})
	}
	return res, sc.Err()
}
