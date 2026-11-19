package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const rgOutput = `{"type":"begin","data":{"path":{"text":"./b.txt"}}}
{"type":"match","data":{"path":{"text":"./b.txt"},"lines":{"bytes":"eP8gaGVsbG8K"},"line_number":1,"absolute_offset":0,"submatches":[{"match":{"text":"hello"},"start":3,"end":8}]}}
{"type":"end","data":{"path":{"text":"./b.txt"},"binary_offset":null,"stats":{"matches":1}}}
{"type":"match","data":{"path":{"bytes":"Li9kaXIvwqDDqS50eHQ="},"lines":{"text":"say hello\r\n"},"line_number":7,"submatches":[]}}
{"type":"context","data":{"path":{"text":"./a.txt"},"lines":{"text":"near\n"},"line_number":8}}
{"type":"future-thing","data":{}}
not json
{"type":"summary","data":{"elapsed_total":{"secs":0}}}
`

func TestParseRg(t *testing.T) {
	r, err := parseRg(strings.NewReader(rgOutput), 10)
	if err != nil || len(r.Hits) != 2 || r.Truncated {
		t.Fatalf("%+v %v", r, err)
	}
	if h := r.Hits[0]; h.Path != "b.txt" || h.Line != 1 || h.Text != "x� hello" {
		t.Errorf("bytes line: %+v", h)
	}
	if h := r.Hits[1]; h.Path != "dir/ é.txt" || h.Line != 7 || h.Text != "say hello" {
		t.Errorf("bytes path: %+v", h)
	}
	if r, _ := parseRg(strings.NewReader(rgOutput), 1); !r.Truncated || len(r.Hits) != 1 {
		t.Errorf("limit: %+v", r)
	}
}

func TestSearchWithBothEngines(t *testing.T) {
	ctx := context.Background()
	svc, gm, tasks := stackedRepo(t)
	must(t, gm.ExcludeLocally(ctx, ".caffe/"))
	wt := tasks["base"].WorktreePath
	must(t, os.WriteFile(filepath.Join(wt, "notes.md"), []byte("one\nNeedle here\nneedle again\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(wt, ".gitignore"), []byte("ignored.txt\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(wt, "ignored.txt"), []byte("needle\n"), 0o644))

	engines := []string{"rg", "git grep"}
	if _, err := os.Stat("/usr/bin/rg"); err != nil {
		engines = engines[1:]
	}
	for _, engine := range engines {
		old := rgPath
		if engine == "git grep" {
			rgPath = "caffe-no-such-rg"
		}
		r, err := svc.Search(ctx, "t-base", "needle", 10)
		rgPath = old
		if err != nil {
			t.Fatalf("%s: %v", engine, err)
		}
		if r.Engine != engine || len(r.Hits) != 2 || r.Hits[0] != (SearchHit{"notes.md", 2, "Needle here"}) {
			t.Errorf("%s: %+v", engine, r)
		}
		if r, _ := svc.Search(ctx, "t-base", "Needle", 10); len(r.Hits) != 1 {
			t.Errorf("%s: smart case: %+v", engine, r)
		}
		if r, err := svc.Search(ctx, "t-base", "absent-xyz", 10); err != nil || len(r.Hits) != 0 {
			t.Errorf("%s: no match: %+v %v", engine, r, err)
		}
		if r, err := svc.Search(ctx, "t-base", "e", 1); err != nil || !r.Truncated {
			t.Errorf("%s: limit: %+v %v", engine, r, err)
		}
	}
}
