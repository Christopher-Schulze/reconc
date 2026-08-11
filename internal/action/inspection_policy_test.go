package action

import (
	"strings"
	"testing"
)

func TestCompilePlanCanonicalizesForbiddenTerms(t *testing.T) {
	t.Parallel()
	policy := validDetectorPolicy()
	policy.ForbiddenTerms = []string{"  Ｓynthetic Marker  "}
	compiled, err := CompilePlan(Plan{Tools: []Tool{gatewayTool("inspect", "inspect")}, Detectors: []DetectorPolicy{policy}})
	if err != nil {
		t.Fatal(err)
	}
	got := compiled.Detectors()[0].ForbiddenTerms
	if len(got) != 1 || got[0] != "synthetic marker" {
		t.Fatalf("canonical forbidden terms = %#v", got)
	}

	policy.ForbiddenTerms = []string{"synthetic marker", "ＳYNTHETIC MARKER"}
	if _, err := CompilePlan(Plan{Tools: []Tool{gatewayTool("inspect", "inspect")}, Detectors: []DetectorPolicy{policy}}); err == nil ||
		!strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("normalization-equivalent terms error = %v", err)
	}
}

func validDetectorPolicy() DetectorPolicy {
	return DetectorPolicy{
		ID: "inspect-content",
		Selector: Selector{
			ToolIDs: []string{"inspect"},
			Phases:  []Phase{PhasePreCall},
		},
		PackID:         BuiltinDetectorPackID,
		PackDigest:     "sha256:" + strings.Repeat("a", 64),
		Fields:         []DetectorField{{Source: SourceArguments, Pointer: "/payload"}},
		Categories:     []DetectorCategory{DetectorForbiddenData},
		ForbiddenTerms: []string{"synthetic marker"},
		SourceIdentity: ".reconc.yml",
	}
}
