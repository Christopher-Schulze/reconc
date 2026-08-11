package impactlab

import (
	"encoding/json"
	"slices"
	"testing"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/runtime"
)

func FuzzDecodeImpactCorpusV2(f *testing.F) {
	repo := f.TempDir()
	corpus, err := NewCorpus(repo, []Case{
		newActionFixture("fuzz-action", CaseActionPre, `{"target":"staging"}`,
			actionAssertion(action.DecisionAllow, action.ReasonDeclaredTool, "database-write", nil,
				action.CacheEligible, action.OutcomeDispatchEligible, "")),
	}, AllEventClasses())
	if err != nil {
		f.Fatal(err)
	}
	body, err := MarshalCorpus(corpus)
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range [][]byte{body, []byte(`{}`), []byte(`{"format_version":"reconc-impact-corpus/v2"}`), {0xff}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > MaxCorpusBytes+1 {
			return
		}
		decoded, err := DecodeCorpus(input)
		if err != nil {
			return
		}
		canonical, err := MarshalCorpus(decoded)
		if err != nil {
			t.Fatalf("marshal accepted corpus: %v", err)
		}
		roundTrip, err := DecodeCorpus(canonical)
		if err != nil || roundTrip.CorpusID != decoded.CorpusID {
			t.Fatalf("corpus round trip = %v, %+v", err, roundTrip)
		}
	})
}

func FuzzActionAssertionValidation(f *testing.F) {
	f.Add(string(action.DecisionAllow), string(action.ReasonDeclaredTool), "database-write", string(action.CacheEligible), string(action.OutcomeDispatchEligible))
	f.Add("", string(action.ReasonDeclaredTool), "database-write", string(action.CacheEligible), string(action.OutcomeDispatchEligible))
	f.Fuzz(func(t *testing.T, decision, reason, toolID, cacheReason, phaseOutcome string) {
		if len(decision)+len(reason)+len(toolID)+len(cacheReason)+len(phaseOutcome) > 4096 {
			return
		}
		assertion := ActionAssertion{
			Decision: action.Decision(decision), Reason: action.ReasonCode(reason), ToolID: toolID,
			MatchedRuleIDs: []string{}, Cache: ActionCacheAssertion{
				Eligible: action.CacheReason(cacheReason) == action.CacheEligible,
				Reason:   action.CacheReason(cacheReason),
			},
			Completeness: action.CompleteEvidence(), PhaseOutcome: action.PhaseOutcome(phaseOutcome),
		}
		if err := validateActionAssertion(action.PhasePreCall, "database-write", assertion); err != nil {
			return
		}
		if !assertion.Decision.Valid() || !assertion.Reason.Valid() || !assertion.Cache.Reason.Valid() ||
			!assertion.PhaseOutcome.Valid() || assertion.PhaseOutcome != action.OutcomeFor(action.PhasePreCall, assertion.Decision) {
			t.Fatalf("invalid assertion passed: %+v", assertion)
		}
	})
}

func FuzzDecodeImpactDeltaManifest(f *testing.F) {
	current := actionAssertion(action.DecisionAllow, action.ReasonDeclaredTool, "database-write", nil,
		action.CacheEligible, action.OutcomeDispatchEligible, "")
	candidate := actionAssertion(action.DecisionBlock, action.ReasonRuleMatched, "database-write", []string{"block-production"},
		action.CacheEligible, action.OutcomeDispatchBlocked, "")
	manifest, err := NewDeltaManifest([]ReviewedActionDelta{{
		CaseID: "fuzz-case", CaseIdentity: driftSHAIdentity, Delta: DeltaNewlyBlocked,
		CandidateLockDigest: driftDigest, Current: current, Candidate: candidate,
		Rationale: "reviewed fuzz transition", Permanent: true,
	}})
	if err != nil {
		f.Fatal(err)
	}
	body, err := MarshalDeltaManifest(manifest)
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range [][]byte{body, []byte(`{}`), []byte(`{"entries":[]}`), {0xff}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > MaxDeltaManifestBytes+1 {
			return
		}
		decoded, err := DecodeDeltaManifest(input)
		if err != nil {
			return
		}
		canonical, err := MarshalDeltaManifest(decoded)
		if err != nil {
			t.Fatalf("marshal accepted manifest: %v", err)
		}
		roundTrip, err := DecodeDeltaManifest(canonical)
		if err != nil || roundTrip.ManifestID != decoded.ManifestID {
			t.Fatalf("manifest round trip = %v, %+v", err, roundTrip)
		}
	})
}

func FuzzActionPrivacyTransform(f *testing.F) {
	scanner := mustActionPrivacyScanner(f)
	syntheticUserPath := "/" + "Users/example/private"
	for _, seed := range [][]byte{
		[]byte(`{"authorization":"Bearer sk-secretvalue123"}`),
		[]byte(`{"path":"` + syntheticUserPath + `"}`),
		[]byte(`{"safe":[1,true,"value"]}`),
		[]byte(`{"authorization":"sk-secretvalue123"`),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 64<<10 {
			return
		}
		first, summaries, err := sanitizeActionRawValue(scanner, ActionPayload(input), action.SourceArguments,
			action.ProvenanceAgentSupplied, "", []ActionValueSummary{})
		if err != nil {
			return
		}
		second, secondSummaries, err := sanitizeActionRawValue(scanner, ActionPayload(input), action.SourceArguments,
			action.ProvenanceAgentSupplied, "", []ActionValueSummary{})
		if err != nil || first != second || !equalValueSummaries(summaries, secondSummaries) {
			t.Fatalf("privacy transform is nondeterministic: %v", err)
		}
		if sensitiveRawActionText(scanner, json.RawMessage(first)) {
			t.Fatalf("privacy transform retained sensitive output %q", first)
		}
	})
}

func FuzzMergeLegacyAndCurrentImpactCorpora(f *testing.F) {
	repo := f.TempDir()
	inputs := runtime.Empty()
	inputs.WritePaths = []string{"src/main.go"}
	current, err := NewCorpus(repo, []Case{NewRepositoryCase("current-case", inputs)}, AllEventClasses())
	if err != nil {
		f.Fatal(err)
	}
	currentBody, err := MarshalCorpus(current)
	if err != nil {
		f.Fatal(err)
	}
	repository := current.Cases[0].Repository
	legacy := legacyCorpus{
		FormatVersion: LegacyCorpusFormatVersion,
		Completeness: legacyCompleteness{
			ObservedEventClasses: []EventClass{EventClassWrite}, CompleteEventClasses: AllEventClasses(),
			MissingEventClasses: []EventClass{}, RedactedEventClasses: []EventClass{}, CompleteReplay: true,
		},
		Cases: []legacyCase{{ID: "legacy-case", Inputs: repository.Inputs, RedactedEventClasses: []EventClass{}}},
	}
	legacy.CorpusID = mustLegacyCorpusIdentity(f, legacy)
	legacyBody, err := json.Marshal(legacy)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(legacyBody, currentBody)
	f.Add(currentBody, legacyBody)
	f.Fuzz(func(t *testing.T, left, right []byte) {
		if len(left)+len(right) > MaxCorpusBytes {
			return
		}
		leftCorpus, leftErr := DecodeCorpus(left)
		rightCorpus, rightErr := DecodeCorpus(right)
		if leftErr != nil || rightErr != nil {
			return
		}
		if sameCaseID(leftCorpus, rightCorpus) {
			return
		}
		merged, err := MergeCorpora([]Corpus{leftCorpus, rightCorpus})
		if err != nil {
			return
		}
		body, err := MarshalCorpus(merged)
		if err != nil {
			t.Fatalf("marshal merged corpus: %v", err)
		}
		if _, err := DecodeCorpus(body); err != nil {
			t.Fatalf("decode merged corpus: %v", err)
		}
	})
}

func equalValueSummaries(left, right []ActionValueSummary) bool {
	return slices.Equal(left, right)
}

func sameCaseID(left, right Corpus) bool {
	seen := make(map[string]struct{}, len(left.Cases))
	for _, replayCase := range left.Cases {
		seen[replayCase.ID] = struct{}{}
	}
	for _, replayCase := range right.Cases {
		if _, ok := seen[replayCase.ID]; ok {
			return true
		}
	}
	return false
}
