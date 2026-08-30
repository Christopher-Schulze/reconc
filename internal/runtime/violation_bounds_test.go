package runtime

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"reconc.dev/reconc/internal/policy"
)

func TestViolationFailureAggregationBoundsMaximumContexts(t *testing.T) {
	collector := newViolationTextCollector(maxViolationAggregateBytes, "; ", "failures")
	collector.add("first-actionable-" + string([]byte{0xff}) + strings.Repeat("界", maxViolationAggregateBytes))
	for range maxExecutionInputItems - 1 {
		collector.add("later failure")
	}

	text := collector.text()
	wantMarker := fmt.Sprintf("...[%d additional failures omitted]", maxExecutionInputItems-1)
	if len(text) > maxViolationAggregateBytes || !utf8.ValidString(text) {
		t.Fatalf("aggregate = %d bytes, valid=%t", len(text), utf8.ValidString(text))
	}
	if !strings.HasPrefix(text, "first-actionable-") || !strings.Contains(text, violationTextMarker) || !strings.HasSuffix(text, wantMarker) {
		t.Fatalf("aggregate lost first failure or exact omission marker: %q", text)
	}
}

func TestBatchScriptViolationBoundsMaximumOutputWithoutChangingEvidence(t *testing.T) {
	failures := []string{strings.Repeat("界", MaxScriptOutputBytes/3)}
	for range 10 {
		failures = append(failures, "later failure")
	}
	rule := &policy.Rule{ID: "script", Kind: policy.KindRequireScript, Mode: policy.ModeBlock, Message: "run audit"}
	contexts := []matchContext{{path: "src/first.go"}, {path: "src/second.go"}}
	violation := buildBatchScriptViolation(workflowAuditBatchItem{rule: rule, contexts: contexts}, "audits/run-workflow-audit", policy.ModeWarn, failures)

	for name, value := range map[string]string{
		"message": violation.Message, "explanation": violation.Explanation, "recommended_action": violation.RecommendedAction,
	} {
		if len(value) > MaxViolationTextBytes || !utf8.ValidString(value) {
			t.Fatalf("%s = %d bytes, valid=%t", name, len(value), utf8.ValidString(value))
		}
	}
	if !strings.Contains(violation.Explanation, "script audits/run-workflow-audit blocked") ||
		!strings.Contains(violation.Explanation, "...[10 additional failures omitted]") ||
		!strings.Contains(violation.RecommendedAction, "...[10 additional failures omitted]") {
		t.Fatalf("bounded script violation = %+v", violation)
	}
	if !reflect.DeepEqual(violation.MatchedPaths, []string{"src/first.go", "src/second.go"}) ||
		!reflect.DeepEqual(violation.RequiredPaths, []string{"audits/run-workflow-audit"}) ||
		violation.Mode != policy.ModeBlock {
		t.Fatalf("script evidence or decision semantics changed: %+v", violation)
	}
}

func TestCheckReportFinalizeBoundsViolationProseOnly(t *testing.T) {
	oversized := string([]byte{0xff}) + strings.Repeat("界", MaxViolationTextBytes)
	matchedPaths := []string{"src/first.go", "src/second.go"}
	requiredCommands := []string{"go test ./..."}
	report := CheckReport{Violations: []Violation{{
		RuleID: "bounded", Kind: policy.KindRequireCommand, Mode: policy.ModeBlock,
		Message: oversized, Explanation: oversized, RecommendedAction: oversized,
		MatchedPaths: matchedPaths, RequiredCommands: requiredCommands,
	}}}
	report.Finalize()

	violation := report.Violations[0]
	for name, value := range map[string]string{
		"message": violation.Message, "explanation": violation.Explanation, "recommended_action": violation.RecommendedAction,
	} {
		if len(value) > MaxViolationTextBytes || !utf8.ValidString(value) || !strings.HasSuffix(value, violationTextMarker) {
			t.Fatalf("%s = %d bytes, valid=%t, suffix=%t", name, len(value), utf8.ValidString(value), strings.HasSuffix(value, violationTextMarker))
		}
	}
	if report.Decision != DecisionBlock || report.BlockingViolationCount != 1 ||
		!reflect.DeepEqual(violation.MatchedPaths, matchedPaths) || !reflect.DeepEqual(violation.RequiredCommands, requiredCommands) {
		t.Fatalf("finalized report semantics changed: %+v", report)
	}
	body, err := json.Marshal(report)
	if err != nil || !utf8.Valid(body) || len(body) > 4*MaxViolationTextBytes+4096 {
		t.Fatalf("bounded report JSON = %d bytes, valid=%t, err=%v", len(body), utf8.Valid(body), err)
	}
}
