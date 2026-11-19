package studio

import (
	"strings"
	"testing"

	c "caffe/internal/contracts"
	"caffe/internal/service"
)

func integrationModel(t *testing.T) (model, *fakeSource) {
	f := newFake()
	f.integ = []service.IntegrationItem{
		{Task: c.Task{ID: "t1", Key: "api", Role: c.RoleDeveloper, BranchName: "caffe/r/api"}, Target: "main", Stat: " 2 files changed, 9 insertions(+)", Review: "approved", Validation: "passed", Manual: 1},
		{Task: c.Task{ID: "t2", Key: "ui", Role: c.RoleDeveloper, BranchName: "caffe/r/ui"}, Target: "main", Review: "rejected", Validation: "none"},
	}
	m := liveModel(f)
	m = drive(t, sized(m), m.Init())
	m, cmd := press(m, "7")
	return drive(t, m, cmd), f
}

func TestIntegrationScreenDecidesAndMerges(t *testing.T) {
	m, f := integrationModel(t)
	v := plain(m.View().Content)
	for _, want := range []string{"Integration · run run-acti", "api", "2 files changed", "approved", "passed", "ui", "rejected", "merged into main"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	m, _ = press(m, "a")
	m = typeText(m, "looks right")
	m, cmd := press(m, "enter")
	m = drive(t, m, cmd)
	if len(f.decisions) != 1 || f.decisions[0] != "t1 approve looks right" || !strings.Contains(plain(m.View().Content), "approve") {
		t.Fatalf("decisions %v", f.decisions)
	}

	f.set(func(f *fakeSource) {
		f.preview = &service.MergePreview{TaskID: "t1", TaskKey: "api", Branch: "caffe/r/api", Target: "main", Command: "git merge --no-ff --no-edit caffe/r/api"}
	})
	m, cmd = press(m, "m")
	m = drive(t, m, cmd)
	if m.overlay != overlayConfirm || !strings.Contains(plain(m.View().Content), "git merge --no-ff --no-edit caffe/r/api") {
		t.Fatalf("no confirmation with the command:\n%s", plain(m.View().Content))
	}
	m, cmd = press(m, "enter")
	if m = drive(t, m, cmd); len(f.merged) != 0 {
		t.Fatal("merged on cancel")
	}
	m, cmd = press(m, "m")
	m = drive(t, m, cmd)
	m, cmd = press(m, "l", "enter")
	m = drive(t, m, cmd)
	if len(f.merged) != 1 || !strings.Contains(plain(m.View().Content), "merged caffe/r/api into main") {
		t.Fatalf("merged %v:\n%s", f.merged, plain(m.View().Content))
	}

	f.set(func(f *fakeSource) {
		f.preview = &service.MergePreview{TaskID: "t2", TaskKey: "ui", Blockers: []string{"not approved yet", "merging conflicts in 1 file(s)"}, Conflicts: []string{"ui.go"}}
	})
	m, _ = press(m, "j")
	m, cmd = press(m, "m")
	m = drive(t, m, cmd)
	if m.overlay == overlayConfirm || !strings.Contains(plain(m.View().Content), "ui.go") || len(f.merged) != 1 {
		t.Fatalf("a blocked merge asked or ran:\n%s", plain(m.View().Content))
	}
}
