package studio

import (
	"os/exec"
	"strings"
	"testing"
)

// Studio is a presentation layer: it reaches the engine only through
// internal/service (docs/studio/architecture.md §3). Direct imports of the
// engine packages from any internal/studio package fail this test.
func TestStudioDoesNotImportTheEngine(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", `{{.ImportPath}}: {{join .Imports " "}}`, "caffe/internal/studio/...").CombinedOutput()
	if err != nil {
		t.Skipf("go list unavailable (%v): %s", err, out) // e.g. a test binary run without the source tree
	}
	forbidden := []string{"caffe/internal/persistence", "caffe/internal/core", "caffe/internal/runtime", "caffe/internal/git", "caffe/internal/agents", "caffe/internal/planner"}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pkg, imports, _ := strings.Cut(line, ": ")
		for _, imp := range strings.Fields(imports) {
			for _, f := range forbidden {
				if imp == f || strings.HasPrefix(imp, f+"/") {
					t.Errorf("%s imports %s; go through internal/service instead", pkg, imp)
				}
			}
		}
	}
}
