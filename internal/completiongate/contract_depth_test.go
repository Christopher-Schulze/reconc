package completiongate

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/policyproof"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
	"reconc.dev/reconc/internal/schema"
)

func TestCompletionRetriesTransientStateDrift(t *testing.T) {
	attempts := 0
	want := &Report{Decision: "pass"}
	got, err := evaluateWithRetries(func() (*Report, error) {
		attempts++
		if attempts == 1 {
			return nil, &RetryableStateDriftError{}
		}
		return want, nil
	})
	if err != nil || got != want || attempts != 2 {
		t.Fatalf("transient drift retry = report:%p err:%v attempts:%d, want report:%p and two attempts", got, err, attempts, want)
	}
}

func TestCompletionReturnsFirstSuccessfulAttempt(t *testing.T) {
	attempts := 0
	want := &Report{Decision: "pass"}
	got, err := evaluateWithRetries(func() (*Report, error) {
		attempts++
		return want, nil
	})
	if err != nil || got != want || attempts != 1 {
		t.Fatalf("first-attempt success = report:%p err:%v attempts:%d, want report:%p and one attempt", got, err, attempts, want)
	}
}

func TestCompletionExhaustsPersistentStateDrift(t *testing.T) {
	attempts := 0
	drift := &RetryableStateDriftError{}
	wrapped := fmt.Errorf("capture completion state: %w", drift)
	_, err := evaluateWithRetries(func() (*Report, error) {
		attempts++
		return nil, wrapped
	})
	want := "repository, policy, or active-session state changed during completion evaluation after 2 attempts; retry limit exhausted"
	if err == nil || err.Error() != want || attempts != completionEvaluationAttempts {
		t.Fatalf("persistent drift = err:%v attempts:%d, want %q after %d attempts", err, attempts, want, completionEvaluationAttempts)
	}
	var exhausted *RetryExhaustedError
	if !errors.As(err, &exhausted) || exhausted.Attempts() != completionEvaluationAttempts {
		t.Fatalf("persistent drift exhaustion identity = %#v", err)
	}
	var retainedDrift *RetryableStateDriftError
	if !errors.As(err, &retainedDrift) || !errors.Is(err, drift) || !errors.Is(err, wrapped) {
		t.Fatalf("persistent drift cause chain = %#v", err)
	}
}

func TestCompletionRetriesWrappedStateDrift(t *testing.T) {
	attempts := 0
	want := &Report{Decision: "pass"}
	got, err := evaluateWithRetries(func() (*Report, error) {
		attempts++
		if attempts == 1 {
			return nil, fmt.Errorf("capture completion state: %w", &RetryableStateDriftError{})
		}
		return want, nil
	})
	if err != nil || got != want || attempts != 2 {
		t.Fatalf("wrapped transient drift = report:%p err:%v attempts:%d", got, err, attempts)
	}
}

func TestCompletionDoesNotRetryNonRetryableFailure(t *testing.T) {
	attempts := 0
	want := errors.New("policy is malformed")
	_, err := evaluateWithRetries(func() (*Report, error) {
		attempts++
		return nil, want
	})
	if !errors.Is(err, want) || attempts != 1 {
		t.Fatalf("non-retryable failure = err:%v attempts:%d, want one attempt and original error", err, attempts)
	}
}

func TestPolicyDecisionPublicationRequiresStableCandidate(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	fingerprint := strings.Repeat("a", 64)
	base := agentsession.CompletionStateSnapshot{RepoRoot: repo, Fingerprint: fingerprint}
	drifted := base
	drifted.Fingerprint = strings.Repeat("b", 64)
	blocking := completionRetryReport(repo, true)
	passing := completionRetryReport(repo, false)

	t.Run("mutation before publication", func(t *testing.T) {
		err := persistDecisionAtStableCandidate(repo, "done", fingerprint, blocking, sequenceCapture(drifted))
		var drift *RetryableStateDriftError
		if !errors.As(err, &drift) {
			t.Fatalf("pre-publication mutation error = %v, want typed drift", err)
		}
		if _, found, loadErr := policyproof.LoadLatest(repo); loadErr != nil || found {
			t.Fatalf("pre-publication mutation left a receipt: found=%t err=%v", found, loadErr)
		}
	})

	t.Run("mutation after publication", func(t *testing.T) {
		err := persistDecisionAtStableCandidate(repo, "done", fingerprint, blocking, sequenceCapture(base, drifted))
		var drift *RetryableStateDriftError
		if !errors.As(err, &drift) {
			t.Fatalf("post-publication mutation error = %v, want typed drift", err)
		}
		record, found, loadErr := policyproof.LoadLatest(repo)
		if loadErr != nil || !found || record.CandidateFingerprint != fingerprint {
			t.Fatalf("post-publication receipt = found:%t record:%#v err:%v, want bound old candidate", found, record, loadErr)
		}
	})

	t.Run("stable block remains published", func(t *testing.T) {
		if err := persistDecisionAtStableCandidate(repo, "done", fingerprint, blocking, sequenceCapture(base, base)); err != nil {
			t.Fatalf("stable block publication: %v", err)
		}
		if _, found, err := policyproof.LoadLatest(repo); err != nil || !found {
			t.Fatalf("stable block receipt: found=%t err=%v", found, err)
		}
	})

	t.Run("stale success never becomes receipt", func(t *testing.T) {
		if err := persistDecisionAtStableCandidate(repo, "done", fingerprint, passing, sequenceCapture(base, drifted)); err == nil {
			t.Fatal("stale success publication unexpectedly succeeded")
		}
		if _, found, err := policyproof.LoadLatest(repo); err != nil || found {
			t.Fatalf("stale success left a receipt: found=%t err=%v", found, err)
		}
	})
}

func sequenceCapture(states ...agentsession.CompletionStateSnapshot) completionStateCapture {
	index := 0
	return func(string) (agentsession.CompletionStateSnapshot, error) {
		if index >= len(states) {
			return states[len(states)-1], nil
		}
		state := states[index]
		index++
		return state, nil
	}
}

func completionRetryReport(repo string, blocking bool) *runtime.CheckReport {
	report := runtime.NewEmptyReport(repo, filepath.Join(repo, ".reconc", "policy.lock.json"), policy.ModeBlock, runtime.Empty())
	if blocking {
		report.Violations = append(report.Violations, runtime.Violation{
			RuleID: "retry-block", Kind: policy.KindDenyWrite, Mode: policy.ModeBlock, Message: "blocked",
		})
	}
	report.Finalize()
	return &report
}

func TestVerifyReportRejectsEveryEnvelopeFailure(t *testing.T) {
	if err := VerifyReport(nil); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("nil report was accepted: %v", err)
	}
	report := &Report{
		Schema: schema.Resolve(schema.CompletionReport), FormatVersion: FormatVersion,
		RepoRoot: "/repo", Checks: []Check{}, Candidate: CandidateBinding{DirtyPaths: []string{}},
	}
	if err := finalize(report); err != nil {
		t.Fatalf("finalize valid report: %v", err)
	}
	if err := VerifyReport(report); err != nil {
		t.Fatalf("valid report rejected: %v", err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*Report)
		err    string
	}{
		{name: "schema", mutate: func(r *Report) { r.Schema = "other" }, err: "unsupported"},
		{name: "format", mutate: func(r *Report) { r.FormatVersion = "2" }, err: "unsupported"},
		{name: "digest", mutate: func(r *Report) { r.Digest = strings.Repeat("0", 64) }, err: "digest mismatch"},
		{name: "malformed digest", mutate: func(r *Report) { r.Digest = "xyz" }, err: "digest mismatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			copyReport := *report
			test.mutate(&copyReport)
			if err := VerifyReport(&copyReport); err == nil || !strings.Contains(err.Error(), test.err) {
				t.Fatalf("expected %q error, got %v", test.err, err)
			}
		})
	}
}

func TestCompletionInputsCloneCapturedCandidateEvidence(t *testing.T) {
	repo := t.TempDir()
	base := agentsession.CompletionStateSnapshot{
		RepoRoot: repo,
		Inputs: runtime.ExecutionInputs{
			ReadPaths: []string{"README.md"}, WritePaths: []string{"session.go"},
			WriteEpochs: map[string]uint64{"session.go": 2}, Commands: []string{"go test"},
			Claims: []string{"approved"}, CommandResults: []runtime.CommandResult{{Command: "go test", Outcome: "success"}},
		},
		EvidenceEpoch: 4,
	}

	inputs, err := completionInputs(base)
	if err != nil {
		t.Fatalf("completionInputs(non-Git): %v", err)
	}
	if !reflect.DeepEqual(inputs.WritePaths, []string{"session.go"}) || inputs.WriteEpochs["session.go"] != 2 {
		t.Fatalf("non-Git evidence changed: %+v", inputs)
	}
	inputs.WritePaths[0] = "changed"
	inputs.WriteEpochs["session.go"] = 99
	if base.Inputs.WritePaths[0] != "session.go" || base.Inputs.WriteEpochs["session.go"] != 2 {
		t.Fatal("completionInputs aliased caller evidence")
	}

	state := base
	state.GitAvailable = true
	state.GitStatusOK = true
	state.DirtyPaths = []string{"src/main.go", "docs/tasks.md"}
	state.WorktreeMatchesIndex = false
	state.Inputs.WritePaths = append([]string{}, state.DirtyPaths...)
	state.Inputs.WriteEpochs = map[string]uint64{"src/main.go": 3, "docs/tasks.md": 5}
	inputs, err = completionInputs(state)
	if err != nil {
		t.Fatalf("completionInputs(Git): %v", err)
	}
	if !reflect.DeepEqual(inputs.WritePaths, state.DirtyPaths) ||
		inputs.WriteEpochs["src/main.go"] != 3 ||
		inputs.WriteEpochs["docs/tasks.md"] != 5 {
		t.Fatalf("captured Git candidate evidence changed: %+v", inputs)
	}
}

func TestCompletionReportHelperContracts(t *testing.T) {
	if got := blockingViolations(nil); got != nil {
		t.Fatalf("nil report produced violations: %+v", got)
	}
	policyReport := runtime.NewEmptyReport("/repo", "/repo/policy.lock.json", policy.ModeBlock, runtime.Empty())
	policyReport.Violations = []runtime.Violation{
		{RuleID: "warn", Mode: policy.ModeWarn},
		{RuleID: "block", Mode: policy.ModeBlock},
	}
	policyReport.Finalize()
	got := blockingViolations(&policyReport)
	if len(got) != 1 || got[0].RuleID != "block" {
		t.Fatalf("blockingViolations = %+v", got)
	}

	if action := exactPolicyAction(runtime.Violation{RuleID: "custom", RecommendedAction: " fix it "}); action != "fix it" {
		t.Fatalf("custom action = %q", action)
	}
	if action := exactPolicyAction(runtime.Violation{RuleID: "default"}); !strings.Contains(action, "`default`") {
		t.Fatalf("default action = %q", action)
	}

	state := agentsession.CompletionStateSnapshot{
		Fingerprint: "fingerprint", PolicyLockHash: "policy", GitAvailable: true,
		GitHead: "head", GitIndexHash: "index", WorktreeHash: "worktree", WorktreeTrusted: true,
		DirtyPaths: []string{"a.go"}, SessionEvidenceHash: "evidence", SessionReportHash: "report",
	}
	candidate := candidateFromState(state)
	if candidate.Fingerprint != state.Fingerprint || candidate.DirtyPaths[0] != "a.go" {
		t.Fatalf("candidateFromState = %+v", candidate)
	}
	candidate.DirtyPaths[0] = "changed"
	if state.DirtyPaths[0] != "a.go" {
		t.Fatal("candidate binding aliased state dirty paths")
	}
}

func TestFinalizeSelectsFirstFailureAction(t *testing.T) {
	report := &Report{
		Schema: schema.Resolve(schema.CompletionReport), FormatVersion: FormatVersion,
		RepoRoot: "/repo", Candidate: CandidateBinding{DirtyPaths: []string{}},
		Checks: []Check{{ID: "warning", Status: StatusWarn}},
	}
	if err := finalize(report); err != nil {
		t.Fatalf("finalize passing report: %v", err)
	}
	if !report.OK || report.Decision != "pass" || report.NextAction != "" || report.Digest == "" {
		t.Fatalf("passing finalize = %+v", report)
	}

	report.Checks = append(report.Checks,
		Check{ID: "first", Status: StatusFail, nextAction: "first action"},
		Check{ID: "second", Status: StatusFail, nextAction: "second action"},
	)
	if err := finalize(report); err != nil {
		t.Fatalf("finalize blocking report: %v", err)
	}
	if report.OK || report.Decision != "block" || report.NextAction != "first action" {
		t.Fatalf("blocking finalize = %+v", report)
	}
}

func TestHashJSONRejectsUnmarshalablePayload(t *testing.T) {
	if _, err := hashJSON(make(chan struct{})); err == nil || !strings.Contains(err.Error(), "marshal report payload") {
		t.Fatalf("unmarshalable payload error = %v", err)
	}
}
