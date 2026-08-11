package impactlab

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actioninspect"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/runtime"
)

type inspectionDetectorCorpusCase struct {
	Name        string                    `json:"name"`
	Categories  []action.DetectorCategory `json:"categories"`
	Text        string                    `json:"text"`
	WantRuleIDs []string                  `json:"want_rule_ids"`
}

func TestActionCorpusReplaysInspectionDetectorsContentSchemasAndBoundaries(t *testing.T) {
	t.Parallel()
	repo, evaluator := makeInspectionImpactRepo(t)
	cases := inspectionDetectorCases(t)
	cases = append(cases, inspectionContentCases()...)
	cases = append(cases, inspectionBoundaryCases()...)
	corpus, err := NewCorpus(repo, cases, AllEventClasses())
	if err != nil {
		t.Fatal(err)
	}
	report, err := Compare(repo, corpus, candidateFromEvaluator(t, evaluator), evaluator, evaluator)
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.ActionCaseCount != len(cases) || report.Summary.ActionDecisionChanges != 0 || !report.DeltaGate.Passed {
		t.Fatalf("inspection action report = %+v", report.Summary)
	}
	body, err := MarshalCorpus(corpus)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range [][]byte{
		[]byte("person@example.test"),
		[]byte("493012345678"),
		[]byte("4111 1111 1111 1111"),
		[]byte("Q7m9V2p4R8x6L3n5"),
	} {
		if bytes.Contains(body, private) {
			t.Fatalf("inspection corpus retained private detector input %q", private)
		}
	}
	decoded, err := DecodeCorpus(body)
	if err != nil || decoded.CorpusID != corpus.CorpusID {
		t.Fatalf("inspection corpus decode = %+v, %v", decoded, err)
	}

	missing := cloneCorpusForTest(t, corpus)
	missing.Cases[0].Action.State.Inspection = nil
	missing.CorpusID = mustCorpusIdentity(t, missing)
	if _, err := Compare(repo, missing, candidateFromEvaluator(t, evaluator), evaluator, evaluator); err == nil {
		t.Fatal("missing inspection evidence passed exact replay")
	}
	drifted := cloneCorpusForTest(t, corpus)
	drifted.Cases[0].Action.State.Inspection.PackIdentities[0] = driftSHAIdentity
	drifted.Cases[0].Action.State.Inspection.Identity = fixtureInspectionIdentity(drifted.Cases[0].Action.State.Inspection)
	drifted.CorpusID = mustCorpusIdentity(t, drifted)
	if _, err := Compare(repo, drifted, candidateFromEvaluator(t, evaluator), evaluator, evaluator); err == nil {
		t.Fatal("detector-pack drift passed exact replay")
	}
}

func mustActionPrivacyScanner(t testing.TB) *actioninspect.TextScanner {
	t.Helper()
	scanner, err := actioninspect.NewTextScanner()
	if err != nil {
		t.Fatal(err)
	}
	return scanner
}

func inspectionDetectorCases(t testing.TB) []Case {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "actioninspect", "testdata", "detector_corpus.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []inspectionDetectorCorpusCase
	if err := json.Unmarshal(body, &fixtures); err != nil {
		t.Fatal(err)
	}
	seenRules := make(map[string]struct{})
	cases := make([]Case, 0, len(fixtures))
	for index, fixture := range fixtures {
		payload, err := json.Marshal(map[string]string{"payload": fixture.Text})
		if err != nil {
			t.Fatal(err)
		}
		caseID := fmt.Sprintf("inspection-detector-%03d", index)
		if len(fixture.WantRuleIDs) == 0 {
			evidence := fixtureInspectionEvidence(caseID, action.PhasePreCall, action.InspectionClean, "", "", nil, nil, action.InspectionSchemaNotApplicable, "")
			cases = append(cases, inspectionCase(caseID, CaseActionPre, string(payload), evidence))
			continue
		}
		ruleID := fixture.WantRuleIDs[0]
		if _, duplicate := seenRules[ruleID]; duplicate {
			continue
		}
		seenRules[ruleID] = struct{}{}
		evidence := fixtureInspectionEvidence(
			caseID, action.PhasePreCall, action.InspectionMatched,
			action.DecisionBlock, action.ReasonRuleMatched, []string{ruleID}, fixture.Categories,
			action.InspectionSchemaNotApplicable, "",
		)
		cases = append(cases, inspectionCase(caseID, CaseActionPre, string(payload), evidence))
	}
	if len(seenRules) != 15 {
		t.Fatalf("detector corpus covers %d unique rules, want 15", len(seenRules))
	}
	return cases
}

func inspectionContentCases() []Case {
	tests := []struct {
		name        string
		contentType action.ContentType
		raw         string
		incomplete  bool
	}{
		{name: "text", contentType: action.ContentText, raw: `{"resultType":"complete","content":[{"type":"text","text":"ordinary value"}]}`},
		{name: "image", contentType: action.ContentImage, raw: `{"resultType":"complete","content":[{"type":"image","data":"AQID","mimeType":"image/png"}]}`},
		{name: "audio", contentType: action.ContentAudio, raw: `{"resultType":"complete","content":[{"type":"audio","data":"AQID","mimeType":"audio/wav"}]}`},
		{name: "resource-text", contentType: action.ContentResourceText, raw: `{"resultType":"complete","content":[{"type":"resource","resource":{"uri":"file:///report.txt","text":"ordinary value"}}]}`},
		{name: "resource-blob", contentType: action.ContentResourceBlob, raw: `{"resultType":"complete","content":[{"type":"resource","resource":{"uri":"file:///report.bin","blob":"AQID"}}]}`},
		{name: "resource-link", contentType: action.ContentResourceLink, raw: `{"resultType":"complete","content":[{"type":"resource_link","uri":"https://example.test/report","name":"report"}]}`},
		{name: "unknown", contentType: action.ContentUnknown, raw: `{"resultType":"complete","content":[{"type":"future","payload":"bounded"}]}`, incomplete: true},
	}
	cases := make([]Case, 0, len(tests))
	for _, test := range tests {
		caseID := "inspection-content-" + test.name
		status, reason := action.InspectionClean, action.ReasonCode("")
		if test.incomplete {
			status, reason = action.InspectionIncomplete, action.ReasonUnsupportedContent
		}
		evidence := fixtureInspectionEvidence(
			caseID, action.PhasePostResult, status, action.DecisionBlock, reason, nil, nil,
			action.InspectionSchemaNotDeclared, test.contentType,
		)
		cases = append(cases, inspectionCase(caseID, CaseActionPost, test.raw, evidence))
	}
	return cases
}

func inspectionBoundaryCases() []Case {
	tests := []struct {
		name         string
		kind         CaseKind
		payload      string
		reason       action.ReasonCode
		schemaStatus action.InspectionSchemaStatus
	}{
		{name: "schema-valid", kind: CaseActionPost, payload: `{"resultType":"complete","content":[],"structuredContent":{"ok":true}}`, schemaStatus: action.InspectionSchemaValid},
		{name: "schema-invalid", kind: CaseActionPost, payload: `{"resultType":"complete","content":[],"structuredContent":{"ok":false}}`, reason: action.ReasonSchemaInvalid, schemaStatus: action.InspectionSchemaInvalid},
		{name: "schema-required", kind: CaseActionPost, payload: `{"resultType":"complete","content":[]}`, reason: action.ReasonSchemaInvalid, schemaStatus: action.InspectionSchemaRequired},
		{name: "deadline", kind: CaseActionPre, payload: `{"payload":"ordinary value"}`, reason: action.ReasonDeadlineExceeded, schemaStatus: action.InspectionSchemaNotApplicable},
		{name: "overflow", kind: CaseActionPre, payload: `{"payload":"ordinary value"}`, reason: action.ReasonLimitExceeded, schemaStatus: action.InspectionSchemaNotApplicable},
	}
	cases := make([]Case, 0, len(tests))
	for _, test := range tests {
		phase := action.PhasePreCall
		status := action.InspectionIncomplete
		if test.kind == CaseActionPost {
			phase = action.PhasePostResult
		}
		if test.reason == "" {
			status = action.InspectionClean
		}
		caseID := "inspection-boundary-" + test.name
		evidence := fixtureInspectionEvidence(
			caseID, phase, status, action.DecisionBlock, test.reason, nil, nil, test.schemaStatus, "",
		)
		cases = append(cases, inspectionCase(caseID, test.kind, test.payload, evidence))
	}
	return cases
}

func inspectionCase(id string, kind CaseKind, payload string, evidence *action.InspectionEvidence) Case {
	expected := inspectionAssertion(kind, evidence)
	fixture := newActionFixture(id, kind, payload, expected)
	fixture.Action.State.Inspection = evidence
	return fixture
}

func inspectionAssertion(kind CaseKind, evidence *action.InspectionEvidence) ActionAssertion {
	phase := action.PhasePreCall
	if kind == CaseActionPost {
		phase = action.PhasePostResult
	}
	if evidence.Status == action.InspectionIncomplete {
		result := actionAssertion(
			action.DecisionBlock, evidence.Reason, "", nil,
			action.CacheEvidenceIncomplete, action.OutcomeFor(phase, action.DecisionBlock), evidence.Reason,
		)
		result.Completeness.PhaseComplete = false
		result.Completeness.Missing = []action.MissingEvidence{{Field: action.EvidencePhase, Reason: evidence.Reason}}
		return result
	}
	if evidence.Status == action.InspectionMatched {
		return actionAssertion(
			evidence.Decision, evidence.Reason, "database-write", evidence.RuleIDs,
			action.CacheEligible, action.OutcomeFor(phase, evidence.Decision), "",
		)
	}
	return actionAssertion(
		action.DecisionAllow, action.ReasonDeclaredTool, "database-write", nil,
		action.CacheEligible, action.OutcomeFor(phase, action.DecisionAllow), "",
	)
}

func fixtureInspectionEvidence(
	caseID string,
	phase action.Phase,
	status action.InspectionStatus,
	decision action.Decision,
	reason action.ReasonCode,
	ruleIDs []string,
	categories []action.DetectorCategory,
	schemaStatus action.InspectionSchemaStatus,
	contentType action.ContentType,
) *action.InspectionEvidence {
	evidence := &action.InspectionEvidence{
		Status: status, Decision: decision, Reason: reason,
		RuleIDs: append([]string{}, ruleIDs...), Categories: append([]action.DetectorCategory{}, categories...),
		PackIdentities: []string{actioninspect.BuiltinPackIdentity()},
		SchemaStatus:   schemaStatus, SchemaIdentity: "absent",
		Fields: []action.InspectionFieldEvidence{}, UnsupportedContent: []action.InspectionContentEvidence{},
	}
	if status == action.InspectionClean {
		evidence.Decision, evidence.Reason = "", ""
	}
	if schemaStatus == action.InspectionSchemaValid || schemaStatus == action.InspectionSchemaInvalid {
		evidence.SchemaIdentity = fixtureToolDigest
	}
	if status != action.InspectionIncomplete {
		source := action.SourceArguments
		if phase == action.PhasePostResult {
			source = action.SourceResult
		}
		evidence.Fields = []action.InspectionFieldEvidence{{
			Source: source, PointerIdentity: fixtureKeyedIdentity(caseID, "pointer"),
			ValueIdentity: fixtureKeyedIdentity(caseID, "value"), ByteLength: 16, ItemCount: 1,
		}}
		evidence.ScannedBytes, evidence.ScannedItems = 16, 1
	}
	if contentType == action.ContentImage || contentType == action.ContentAudio ||
		contentType == action.ContentResourceBlob || contentType == action.ContentUnknown {
		evidence.UnsupportedContent = []action.InspectionContentEvidence{{
			ContentType: contentType, Identity: fixtureKeyedIdentity(caseID, "binary"), ByteLength: 3,
		}}
	}
	evidence.Identity = fixtureInspectionIdentity(evidence)
	return evidence
}

func fixtureInspectionIdentity(evidence *action.InspectionEvidence) string {
	copy := *evidence
	copy.Identity = ""
	body, _ := json.Marshal(copy)
	return fixtureKeyedIdentity(string(body))
}

func fixtureKeyedIdentity(parts ...string) string {
	mac := hmac.New(sha256.New, []byte("reconc-impact-inspection-fixture-key"))
	for _, part := range parts {
		_, _ = mac.Write([]byte{byte(len(part) >> 24), byte(len(part) >> 16), byte(len(part) >> 8), byte(len(part))})
		_, _ = mac.Write([]byte(part))
	}
	return "hmac-sha256:v1:fixture:" + hex.EncodeToString(mac.Sum(nil))
}

func makeInspectionImpactRepo(t testing.TB) (string, *runtime.CompiledPolicyEvaluator) {
	t.Helper()
	repo := t.TempDir()
	body := `default_mode: warn
actions:
  tools:
    - id: database-write
      transport: mcp_stdio
      server_label: database
      server_fingerprint: ` + fixtureServerIdentity + `
      tool: execute
      effect:
        kind: external
  rules: []
  detectors:
    - id: inspect-content
      selector:
        tool_ids: [database-write]
        phases: [pre_call, post_result]
      pack_id: reconc-core-v1
      pack_digest: ` + actioninspect.BuiltinPackIdentity() + `
      fields:
        - source: arguments
          pointer: /payload
        - source: result
          pointer: ""
      categories: [credential, secret, pii_email, pii_phone, pii_payment_card, forbidden_data, prompt_injection, role_override, privilege_claim, indirect_instruction, delimiter_attack, exfiltration]
      forbidden_terms: [synthetic forbidden marker]
      pre_call_decision: block
      post_result_disposition: withhold
      allowed_content_types: [image, audio, resource_blob]
rules: []
`
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatalf("compile inspection action repo:\n%s\n%v", body, err)
	}
	evaluator, _, err := runtime.NewEvaluator().CurrentCompiledPolicyEvaluator(repo)
	if err != nil {
		t.Fatal(err)
	}
	return repo, evaluator
}

func TestInspectionIdentityDriftIsAnExactActionDimension(t *testing.T) {
	t.Parallel()
	_, evaluator := makeInspectionImpactRepo(t)
	compiled, err := evaluator.ActionRuntime()
	if err != nil {
		t.Fatal(err)
	}
	evidence := fixtureInspectionEvidence(
		"inspection-identity-drift", action.PhasePreCall, action.InspectionClean,
		"", "", nil, nil, action.InspectionSchemaNotApplicable, "",
	)
	fixture := inspectionCase("inspection-identity-drift", CaseActionPre, `{"payload":"ordinary value"}`, evidence)
	fixture.Action.State.ResampleDrift = []ActionIdentityComponent{IdentityInspection}
	observation, err := evaluateActionScenario(*fixture.Action, compiled)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Outcome.Decision != action.DecisionBlock ||
		observation.Outcome.Cache.Reason != action.CacheStateStale ||
		observation.Outcome.FailureCode != action.ReasonStateUnavailable {
		t.Fatalf("inspection identity drift outcome = %+v", observation.Outcome)
	}
}

func TestInspectionFixtureRejectsRawMetadataAndNullEvidenceCollections(t *testing.T) {
	t.Parallel()
	evidence := fixtureInspectionEvidence(
		"inspection-invalid-fixture", action.PhasePreCall, action.InspectionClean,
		"", "", nil, nil, action.InspectionSchemaNotApplicable, "",
	)
	fixture := inspectionCase("inspection-invalid-fixture", CaseActionPre, `{"payload":"ordinary value"}`, evidence)
	tests := []struct {
		name   string
		mutate func(*action.InspectionEvidence)
	}{
		{name: "null fields", mutate: func(value *action.InspectionEvidence) { value.Fields = nil }},
		{name: "unsafe rule", mutate: func(value *action.InspectionEvidence) {
			value.Status = action.InspectionMatched
			value.Decision = action.DecisionBlock
			value.Reason = action.ReasonRuleMatched
			value.RuleIDs = []string{"/private/path"}
			value.Categories = []action.DetectorCategory{action.DetectorSecret}
		}},
		{name: "totals", mutate: func(value *action.InspectionEvidence) { value.ScannedBytes++ }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := cloneActionCase(*fixture.Action)
			test.mutate(mutated.State.Inspection)
			if _, err := validateActionCase(fixture.Kind, mutated); err == nil {
				t.Fatal("invalid inspection fixture passed")
			}
		})
	}
}
