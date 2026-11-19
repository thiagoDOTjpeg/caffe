package studio

import (
	"strconv"
	"strings"
	"testing"

	"caffe/internal/service"
)

const sampleDiff = `diff --git a/hello.txt b/hello.txt
new file mode 100644
index 0000000..ce01362
--- /dev/null
+++ b/hello.txt
@@ -0,0 +1,2 @@
+hello from herd
+second
diff --git a/main.go b/main.go
index 1111111..2222222 100644
--- a/main.go
+++ b/main.go
@@ -10,4 +10,4 @@ func main() {
 	a := 1
-	b := 2
+	b := 3
 	c := 4
\ No newline at end of file`

func TestParseDiff(t *testing.T) {
	files, rows := parseDiff(sampleDiff)
	if len(files) != 2 || !files[0].added || files[0].adds != 2 || files[1].path != "main.go" || files[1].adds != 1 || files[1].dels != 1 {
		t.Fatalf("files = %+v", files)
	}
	var got []string
	for _, r := range rows {
		got = append(got, string(r.kind)+itoa(r.old)+"/"+itoa(r.new))
	}
	want := "f0/0 h0/0 +0/1 +0/2 f0/0 h0/0  10/10 -11/0 +0/11  12/12 m0/0"
	if strings.Join(got, " ") != want {
		t.Errorf("rows %s\nwant %s", strings.Join(got, " "), want)
	}
	pairs := pairRows(rows, files)
	if files[1].pair != 4 || len(pairs) != 10 || pairs[7] != (pair{7, 8}) {
		t.Errorf("pairs %+v, main.go starts at %d", pairs, files[1].pair)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func diffModel(t *testing.T) model {
	t.Helper()
	m := overviewModel(t)
	f := m.src.(*fakeSource)
	f.set(func(f *fakeSource) {
		f.diffs = map[string]*service.Diff{"t2": {TaskKey: "impl", Base: "7f5d944aa", Text: sampleDiff, Source: service.DiffLive}}
		f.files = map[string]string{"t2:main.go": strings.Repeat("x\n", 30)}
		f.lists = map[string]*service.FileList{"t2": {Paths: []string{"hello.txt", "main.go"}, Marks: map[string]string{}}}
	})
	m, _ = press(m, "2")
	m.tasks.cursor = "t2"
	m, cmd := press(m, "d")
	return drive(t, m, cmd)
}

func TestDiffViewFromTaskToFile(t *testing.T) {
	m := diffModel(t)
	if m.scope() != "diff" {
		t.Fatalf("d does not open the diff (scope %s)", m.scope())
	}
	golden(t, "diff-unified-120x40", m.View().Content)
	v := plain(m.View().Content)
	for _, want := range []string{"Diff · impl · vs 7f5d944", "▸A hello.txt", "+2 -0", "   1 │ +hello from herd", "  11 │ -    b := 2"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
	m, _ = press(m, "s")
	golden(t, "diff-side-120x40", m.View().Content)
	if v := plain(m.View().Content); !strings.Contains(v, "-    b := 2") || !strings.Contains(v, "│   11 │ +    b := 3") {
		t.Errorf("side by side:\n%s", v)
	}
	m, _ = press(m, "n")
	m, cmd := press(m, "enter")
	m = drive(t, m, cmd)
	if m.screen != screenWorkspace || m.ws.selected.ID != "t2" || m.file == nil || m.file.path != "main.go" || m.file.scroll != 9 {
		t.Fatalf("not opened at the hunk: screen %d ws %q file %+v", m.screen, m.ws.selected.ID, m.file)
	}
	if v := plain(m.View().Content); !strings.Contains(v, "── ws impl") || !strings.Contains(v, "10 │ x") {
		t.Errorf("worktree not in the header or wrong line:\n%s", v)
	}
	m, cmd = press(m, "d")
	if m = drive(t, m, cmd); m.diff == nil || !m.diff.loaded {
		t.Error("d in the workspace does not open its diff")
	}
}

func TestDiffRefusals(t *testing.T) {
	m := overviewModel(t)
	m, _ = press(m, "2")
	if m, cmd := press(m, "d"); cmd != nil || !strings.Contains(plain(m.View().Content), "only developer tasks") {
		t.Error("diff of a researcher task not refused")
	}
	m, _ = press(m, "esc")
	m.width = 100
	m.diff = &diffView{loaded: true, diff: &service.Diff{Source: service.DiffStored}}
	m.diff.files, m.diff.rows = parseDiff(sampleDiff)
	if m, _ = press(m, "s"); m.diff.side || !strings.Contains(plain(m.View().Content), "needs 120 columns") {
		t.Error("side by side allowed at 100 columns")
	}
	if m, _ = press(m, "enter"); m.diff == nil || !strings.Contains(plain(m.View().Content), "only its stored diff") {
		t.Error("opening from a stored diff not refused")
	}
}
