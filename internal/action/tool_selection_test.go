package action

import (
	"strings"
	"testing"
)

func TestOptionalToolFingerprintIsAValidatedWildcard(t *testing.T) {
	fingerprint := "hmac-sha256:v1:key1:" + strings.Repeat("a", 64)
	tool := Tool{
		ID: "query", Transport: TransportMCPStdio, ServerLabel: "database",
		Tool: "query", Effect: Effect{Kind: EffectExternal},
		Origin: OriginActions, SourceIdentity: ".reconc.yml",
	}
	compiled, err := CompilePlan(Plan{
		Tools: []Tool{tool},
		Detectors: []DetectorPolicy{{
			ID:         "inspect-query",
			Selector:   Selector{ToolIDs: []string{"query"}, Phases: []Phase{PhasePreCall}},
			PackID:     BuiltinDetectorPackID,
			PackDigest: "sha256:" + strings.Repeat("b", 64),
			Fields:     []DetectorField{{Source: SourceArguments, Pointer: "/query"}},
			Categories: []DetectorCategory{DetectorSecret}, SourceIdentity: ".reconc.yml",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{
		Transport: TransportMCPStdio, ServerLabel: "database",
		ServerFingerprint: fingerprint, Tool: "query", Phase: PhasePreCall,
	}
	evaluator, err := NewEvaluator(compiled)
	if err != nil {
		t.Fatal(err)
	}
	selected, id := evaluator.selectTool(request)
	detectors := compiled.DetectorPolicies(request)
	views := compiled.DetectorPolicyViews(request)
	if selected == nil || id != "query" || len(detectors) != 1 || len(views) != 1 {
		t.Fatalf("wildcard selection = tool %#v, id %q, detectors %d", selected, id, len(detectors))
	}
	detectors[0].Policy.PackDigest = "sha256:" + strings.Repeat("f", 64)
	if views[0].PackDigest() == detectors[0].Policy.PackDigest || views[0].FieldCount() != 1 {
		t.Fatal("read-only detector view exposed or followed a detached clone mutation")
	}
	if _, _, err := views[0].ResolveField(Value{}, -1); err == nil {
		t.Fatal("detector view accepted an invalid field index")
	}
}

func TestToolFingerprintDeclarationsCannotOverlap(t *testing.T) {
	base := Tool{
		Transport: TransportMCPStdio, ServerLabel: "database", Tool: "query",
		Effect: Effect{Kind: EffectExternal}, Origin: OriginActions, SourceIdentity: ".reconc.yml",
	}
	unbound, pinned := base, base
	unbound.ID = "unbound"
	pinned.ID = "pinned"
	pinned.ServerFingerprint = "hmac-sha256:v1:key1:" + strings.Repeat("a", 64)
	if _, err := CompilePlan(Plan{Tools: []Tool{unbound, pinned}}); err == nil || !strings.Contains(err.Error(), "overlapping") {
		t.Fatalf("overlapping declaration error = %v", err)
	}

	second := pinned
	second.ID = "pinned-other"
	second.ServerFingerprint = "hmac-sha256:v1:key1:" + strings.Repeat("b", 64)
	if _, err := CompilePlan(Plan{Tools: []Tool{pinned, second}}); err != nil {
		t.Fatalf("distinct pinned declarations rejected: %v", err)
	}
}
