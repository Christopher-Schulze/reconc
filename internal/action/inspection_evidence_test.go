package action

import (
	"strings"
	"testing"
)

func TestEvaluatorRequiresExactInspectionContractEvidence(t *testing.T) {
	base, input := testActionEvaluator(t, nil, Defaults{}, testExternalEffect())
	const packIdentity = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	compiled, err := CompilePlan(Plan{
		Tools: base.plan.Tools,
		Detectors: []DetectorPolicy{{
			ID: "inspect-path",
			Selector: Selector{
				ToolIDs: []string{"database-write"}, Phases: []Phase{PhasePreCall},
			},
			PackID: BuiltinDetectorPackID, PackDigest: packIdentity,
			Fields:         []DetectorField{{Source: SourceArguments, Pointer: "/path"}},
			Categories:     []DetectorCategory{DetectorSecret},
			SourceIdentity: ".reconc.yml",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := NewEvaluator(compiled)
	if err != nil {
		t.Fatal(err)
	}
	identity := func(fill string) string {
		return "hmac-sha256:v1:key1:" + strings.Repeat(fill, 64)
	}
	input.Inspection = &InspectionEvidence{
		Status: InspectionClean, Identity: identity("1"),
		PackIdentities: []string{packIdentity},
		SchemaStatus:   InspectionSchemaNotApplicable, SchemaIdentity: "absent",
		Fields: []InspectionFieldEvidence{{
			Source: SourceArguments, PointerIdentity: identity("2"), ValueIdentity: identity("3"),
			ByteLength: 11, ItemCount: 1,
		}},
		ScannedBytes: 11, ScannedItems: 1,
		RuleIDs: []string{}, Categories: []DetectorCategory{},
		UnsupportedContent: []InspectionContentEvidence{},
	}
	refreshTestIdentities(evaluator, &input)
	if result := evaluator.Evaluate(input); result.Failure != nil || result.Decision != DecisionAllow {
		t.Fatalf("valid inspection evidence failed: %+v", result)
	}

	mutations := []struct {
		name   string
		mutate func(*InspectionEvidence)
	}{
		{name: "pack absent", mutate: func(e *InspectionEvidence) { e.PackIdentities = []string{} }},
		{name: "pack drift", mutate: func(e *InspectionEvidence) {
			e.PackIdentities = []string{"sha256:" + strings.Repeat("b", 64)}
		}},
		{name: "field absent", mutate: func(e *InspectionEvidence) {
			e.Fields, e.ScannedBytes, e.ScannedItems = []InspectionFieldEvidence{}, 0, 0
		}},
		{name: "field totals forged", mutate: func(e *InspectionEvidence) { e.ScannedBytes++ }},
		{name: "schema outside result", mutate: func(e *InspectionEvidence) {
			e.SchemaStatus = InspectionSchemaValid
			e.SchemaIdentity = "sha256:" + strings.Repeat("c", 64)
		}},
		{name: "binary outside result", mutate: func(e *InspectionEvidence) {
			e.UnsupportedContent = []InspectionContentEvidence{{
				ContentType: ContentImage, Identity: identity("4"), ByteLength: 3,
			}}
		}},
		{name: "result outcome before call", mutate: func(e *InspectionEvidence) {
			e.Status, e.Decision, e.Reason = InspectionMatched, DecisionBlock, ReasonResultWithheld
			e.RuleIDs, e.Categories = []string{"secret-assignment"}, []DetectorCategory{DetectorSecret}
		}},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			candidate := input
			candidate.Inspection = cloneInspectionEvidence(input.Inspection)
			test.mutate(candidate.Inspection)
			refreshTestIdentities(evaluator, &candidate)
			result := evaluator.Evaluate(candidate)
			if result.Failure == nil || result.Failure.Code != ReasonInvalidRequest {
				t.Fatalf("mutation passed: %+v", result)
			}
		})
	}
}
