package studio

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// file:line links in session output (EX-04): Go test failures
// ("hello_test.go:12: got …"), compiler errors ("./x.go:3:7: …"), stack
// traces ("\t/abs/path/x.go:42 +0x1d"). Windows drive paths too.
var linkRe = regexp.MustCompile(`((?:[A-Za-z]:)?[\w.\-/\\]*[\w\-]\.[A-Za-z]\w*):(\d+)(?::(\d+))?`)

type link struct {
	path      string
	line, col int
}

// findLink returns the first file:line in s.
func findLink(s string) (link, bool) {
	m := linkRe.FindStringSubmatch(s)
	if m == nil {
		return link{}, false
	}
	l := link{path: m[1]}
	l.line, _ = strconv.Atoi(m[2])
	l.col, _ = strconv.Atoi(m[3])
	return l, l.line > 0
}

// resolveLink finds the workspace file a link names. The path comes from
// command output, so it is untrusted: ReadFile refuses anything outside the
// workspace. A relative path that is not relative to the workspace root (go
// test prints paths relative to the package) is matched by suffix against
// the workspace's files and used when exactly one file matches.
func resolveLink(ctx context.Context, src source, ws string, l link) (string, error) {
	p := filepath.ToSlash(l.path)
	_, err := src.ReadFile(ctx, ws, p, 1)
	if err == nil {
		return p, nil
	}
	if filepath.IsAbs(l.path) || strings.HasPrefix(p, "/") {
		return "", err
	}
	list, lerr := src.Files(ctx, ws)
	if lerr != nil {
		return "", err
	}
	want := "/" + strings.TrimPrefix(p, "./")
	var found []string
	for _, f := range list.Paths {
		if strings.HasSuffix("/"+f, want) {
			found = append(found, f)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return "", fmt.Errorf("%s is not a file of the workspace", l.path)
	default:
		return "", fmt.Errorf("%s matches %d files of the workspace (%s, …)", l.path, len(found), found[0])
	}
}
