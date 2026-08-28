package completiongate

import (
	"reflect"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
	"reconc.dev/reconc/internal/schema"
)

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
