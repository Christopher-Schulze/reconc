package actioninspect

import (
	"encoding/json"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/action"
)

func TestWithheldMCPResultUsesBoundedMetadataNotToolStructuredContent(t *testing.T) {
	t.Parallel()
	evidence := &action.InspectionEvidence{
		Status: action.InspectionMatched, Decision: action.DecisionBlock,
		Reason:  action.ReasonResultWithheld,
		RuleIDs: []string{"pii-email"}, Categories: []action.DetectorCategory{action.DetectorPIIEmail},
	}
	body, err := WithheldMCPResult("act_aaaaaaaaaaaaaaaaaaaaaaaaaa", evidence)
	if err != nil {
		t.Fatal(err)
	}
	var result withheldToolResult
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	root, err := action.ParseObjectJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := root.Lookup("structuredContent"); present {
		t.Fatal("withheld result collides with the downstream tool output schema")
	}
	if result.Meta.Reconc.Outcome != "withheld" || !result.IsError {
		t.Fatalf("withheld result = %#v", result)
	}
	decoded, err := DecodeMCPToolResult(body, ProtocolCurrent)
	if err != nil {
		t.Fatal(err)
	}
	defer decoded.Release()
	if decoded.HasStructuredContent || !decoded.IsError {
		t.Fatalf("decoded withheld result = %#v", decoded)
	}
}

func TestWithheldMCPResultRejectsUnboundedOrDuplicateEvidence(t *testing.T) {
	t.Parallel()
	base := action.InspectionEvidence{
		Status: action.InspectionMatched, Decision: action.DecisionBlock,
		Reason:  action.ReasonResultWithheld,
		RuleIDs: []string{"pii-email"}, Categories: []action.DetectorCategory{action.DetectorPIIEmail},
	}
	tests := []struct {
		name   string
		mutate func(*action.InspectionEvidence)
	}{
		{name: "duplicate rule", mutate: func(value *action.InspectionEvidence) { value.RuleIDs = []string{"pii-email", "pii-email"} }},
		{name: "duplicate category", mutate: func(value *action.InspectionEvidence) {
			value.Categories = []action.DetectorCategory{action.DetectorPIIEmail, action.DetectorPIIEmail}
		}},
		{name: "too many rules", mutate: func(value *action.InspectionEvidence) {
			value.RuleIDs = make([]string, action.MaxDetectors+1)
			for index := range value.RuleIDs {
				value.RuleIDs[index] = "rule-" + strings.Repeat("a", 50)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := base
			test.mutate(&evidence)
			if _, err := WithheldMCPResult("act_aaaaaaaaaaaaaaaaaaaaaaaaaa", &evidence); err == nil {
				t.Fatal("invalid evidence produced a withheld result")
			}
		})
	}
}
