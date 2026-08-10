package action

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

type evaluatorGoldenFile struct {
	FormatVersion string                  `json:"format_version"`
	Vectors       []evaluatorGoldenVector `json:"vectors"`
}

type evaluatorGoldenVector struct {
	ID        string            `json:"id"`
	Rules     []Rule            `json:"rules"`
	Arguments json.RawMessage   `json:"arguments"`
	Context   []RawContextValue `json:"context"`
	Expected  goldenDecision    `json:"expected"`
}

type goldenDecision struct {
	Decision       Decision     `json:"decision"`
	Reason         ReasonCode   `json:"reason_code"`
	ToolID         string       `json:"tool_id"`
	MatchedRuleIDs []string     `json:"matched_rule_ids"`
	Candidates     []Candidate  `json:"candidates"`
	Trace          []TraceEntry `json:"trace"`
	TraceComplete  bool         `json:"trace_complete"`
	TraceOmitted   int          `json:"trace_omitted"`
	Completeness   Completeness `json:"completeness"`
	CacheEligible  bool         `json:"cache_eligible"`
	CacheReason    CacheReason  `json:"cache_reason"`
	PhaseOutcome   PhaseOutcome `json:"phase_outcome"`
}

func TestEvaluatorGoldenDecisionAndTraceVectors(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/evaluator-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture evaluatorGoldenFile
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.FormatVersion != "1" || len(fixture.Vectors) == 0 {
		t.Fatalf("invalid evaluator golden fixture header: %#v", fixture)
	}
	for _, vector := range fixture.Vectors {
		t.Run(vector.ID, func(t *testing.T) {
			evaluator, input := testActionEvaluator(t, vector.Rules, Defaults{}, testExternalEffect())
			raw := testRawRequest(vector.Arguments)
			raw.Context = vector.Context
			request, normalizeErr := NormalizeRequest(raw)
			if normalizeErr != nil {
				t.Fatal(normalizeErr)
			}
			input.Request = request
			refreshTestIdentities(evaluator, &input)
			got := projectGoldenDecision(evaluator.Evaluate(input))
			if !reflect.DeepEqual(got, vector.Expected) {
				gotJSON, _ := json.MarshalIndent(got, "", "  ")
				wantJSON, _ := json.MarshalIndent(vector.Expected, "", "  ")
				t.Fatalf("golden mismatch\ngot:\n%s\nwant:\n%s", gotJSON, wantJSON)
			}
		})
	}
}

func projectGoldenDecision(result EvaluationResult) goldenDecision {
	return goldenDecision{
		Decision: result.Decision, Reason: result.Reason, ToolID: result.ToolID,
		MatchedRuleIDs: result.MatchedRuleIDs, Candidates: result.Candidates,
		Trace: result.Trace, TraceComplete: result.TraceComplete,
		TraceOmitted: result.TraceOmitted, Completeness: result.Completeness,
		CacheEligible: result.Cache.Eligible, CacheReason: result.Cache.Reason,
		PhaseOutcome: result.PhaseOutcome,
	}
}
