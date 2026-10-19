package assurance

import (
	"testing"

	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/stackdetect"
)

func TestModuleRootsAreDetectedOnlyForApplicabilityScopedGates(t *testing.T) {
	root := t.TempDir()
	writeAssuranceFile(t, root, "allowed/file.txt", "ok")
	writeAssuranceFile(t, root, "go.mod", "module example\n")
	detections := 0
	previous := detectModuleRoots
	t.Cleanup(func() { detectModuleRoots = previous })
	detectModuleRoots = func(root string) (stackdetect.Result, error) {
		detections++
		return previous(root)
	}
	layout := policy.AssuranceGate{ID: "layout", Type: policy.AssuranceRepositoryLayout, AllowedRootEntries: []string{"allowed", "go.mod"}}
	if _, err := Evaluate(root, []policy.AssuranceGate{layout}, Inputs{}); err != nil {
		t.Fatal(err)
	}
	if detections != 0 {
		t.Fatalf("unscoped gate walked module roots %d times, want 0", detections)
	}
	live := policy.AssuranceGate{ID: "go-live", Type: policy.AssuranceLiveVerification, ApplicableIf: []string{"go.mod"}, Commands: []string{"go test ./..."}, CommandPolicy: "all"}
	second := live
	second.ID = "go-live-2"
	findings, err := Evaluate(root, []policy.AssuranceGate{layout, live, second}, Inputs{})
	if err != nil {
		t.Fatal(err)
	}
	if detections != 1 {
		t.Fatalf("scoped gates walked module roots %d times, want once per evaluation", detections)
	}
	if len(findings) != 2 {
		t.Fatalf("both scoped gates must still evaluate: %+v", findings)
	}
}
