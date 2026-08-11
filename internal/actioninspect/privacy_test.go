package actioninspect

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/action"
)

func TestInspectionEvidenceWarningsAndDiagnosticsNeverEchoSelectedContent(t *testing.T) {
	t.Parallel()
	selected := "api_key=Q7m9V2p4R8x6L3n5"
	compiled := testCompiledPlan(t, action.PhasePreCall, []action.DetectorCategory{action.DetectorSecret}, nil, BuiltinPackIdentity())
	plan := compiled.Plan()
	plan.Detectors[0].PreCallDecision = action.DecisionWarn
	compiled, err := action.CompilePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	key := testIdentityKey{id: strings.Repeat("a", 32), key: []byte(strings.Repeat("k", 32))}
	engine, err := NewEngine(compiled, key)
	if err != nil {
		t.Fatal(err)
	}
	arguments := mustValue(t, `{"payload":"`+selected+`"}`)
	request := action.Request{
		Transport: action.TransportMCPStdio, ServerLabel: "server", Tool: "inspect",
		Phase: action.PhasePreCall, Arguments: &arguments,
	}
	evidence, err := engine.Inspect(context.Background(), request, nil, nil)
	if err != nil || evidence.Status != action.InspectionMatched || evidence.Decision != action.DecisionWarn {
		t.Fatalf("warning evidence = %#v, error = %v", evidence, err)
	}
	body, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), selected) {
		t.Fatal("warning evidence echoed selected content")
	}

	malformed := `{"resultType":"complete","content":[],"` + selected + `":1,"` + selected + `":2}`
	if _, err := DecodeMCPToolResult([]byte(malformed), ProtocolCurrent); err == nil || strings.Contains(err.Error(), selected) {
		t.Fatalf("malformed result diagnostic = %v", err)
	}
	if _, err := CompileOutputSchema([]byte(`{"type":"string","pattern":"(?=` + selected + `)"}`)); err == nil || strings.Contains(err.Error(), selected) {
		t.Fatalf("schema compilation diagnostic = %v", err)
	}
	duplicateSchema := `{"` + selected + `":true,"` + selected + `":false}`
	if _, err := CompileOutputSchema([]byte(duplicateSchema)); err == nil || strings.Contains(err.Error(), selected) {
		t.Fatalf("schema decoding diagnostic = %v", err)
	}
	schema, err := CompileOutputSchema([]byte(`{"const":"different"}`))
	if err != nil {
		t.Fatal(err)
	}
	value := mustValue(t, `"`+selected+`"`)
	if err := schema.Validate(value); err == nil || strings.Contains(err.Error(), selected) {
		t.Fatalf("schema validation diagnostic = %v", err)
	}
}
