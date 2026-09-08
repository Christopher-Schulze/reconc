package agentsession

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/runtime"
)

func boundReportFixture(t *testing.T) (string, SessionState, *runtime.CheckReport) {
	t.Helper()
	repo := setupStopBenchmarkRepo(t)
	state, err := InitializeSessionState(repo, "report-binding")
	if err != nil {
		t.Fatal(err)
	}
	report := runtime.NewEmptyReport(repo, filepath.Join(repo, ".reconc", "policy.lock.json"), policy.ModeBlock, runtime.Empty())
	report.Violations = []runtime.Violation{{
		RuleID: "saved-gate", Mode: policy.ModeBlock, Message: "resolve saved gate",
	}}
	report.Finalize()
	reportHash, err := hashCheckReport(&report)
	if err != nil {
		t.Fatal(err)
	}
	evidenceHash, err := stopPolicyEvidenceHash(state)
	if err != nil {
		t.Fatal(err)
	}
	state.StopPolicyReportHash = reportHash
	state.StopPolicyEvidenceHash = evidenceHash
	state.StopPolicyFingerprint = stopPolicyFingerprint(repo, state)
	return repo, state, &report
}

func TestInspectSessionReportBindingCurrentRequiresAllIdentities(t *testing.T) {
	repo, state, report := boundReportFixture(t)
	binding, err := InspectSessionReportBinding(repo, state, report)
	if err != nil {
		t.Fatal(err)
	}
	if binding.Status != SessionReportCurrent {
		t.Fatalf("status=%q reason=%q, want current", binding.Status, binding.Reason)
	}
	if binding.ReportHash != state.StopPolicyReportHash || binding.EvidenceHash != state.StopPolicyEvidenceHash || binding.CandidateFingerprint != state.StopPolicyFingerprint {
		t.Fatalf("binding identities = %+v, state=%+v", binding, state)
	}
}

func TestInspectSessionReportBindingKeepsUnboundReportHistorical(t *testing.T) {
	repo, state, report := boundReportFixture(t)
	state.StopPolicyFingerprint = ""
	state.StopPolicyEvidenceHash = ""
	state.StopPolicyReportHash = ""
	binding, err := InspectSessionReportBinding(repo, state, report)
	if err != nil {
		t.Fatal(err)
	}
	if binding.Status != SessionReportHistorical || !strings.Contains(binding.Reason, "no stable") {
		t.Fatalf("binding=%+v, want historical unbound report", binding)
	}
}

func TestInspectSessionReportBindingRejectsEvidenceAndCandidateDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string, *SessionState)
		want   string
	}{
		{
			name: "evidence drift",
			mutate: func(_ *testing.T, _ string, state *SessionState) {
				state.Commands = []string{"go test ./..."}
			},
			want: "evidence changed",
		},
		{
			name: "policy candidate drift",
			mutate: func(t *testing.T, repo string, _ *SessionState) {
				path := filepath.Join(repo, ".reconc", "policy.lock.json")
				body, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, append(body, ' '), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "candidate changed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo, state, report := boundReportFixture(t)
			test.mutate(t, repo, &state)
			binding, err := InspectSessionReportBinding(repo, state, report)
			if err != nil {
				t.Fatal(err)
			}
			if binding.Status != SessionReportHistorical || !strings.Contains(binding.Reason, test.want) {
				t.Fatalf("binding=%+v, want historical reason containing %q", binding, test.want)
			}
		})
	}
}

func TestInspectSessionReportBindingRejectsExpiredReportAndWrongRoot(t *testing.T) {
	repo, state, report := boundReportFixture(t)
	state.StopPolicyExpiresAt = time.Now().Add(-time.Second).Unix()
	binding, err := InspectSessionReportBinding(repo, state, report)
	if err != nil {
		t.Fatal(err)
	}
	if binding.Status != SessionReportHistorical || binding.Reason != "saved report expired" {
		t.Fatalf("expired binding=%+v", binding)
	}

	foreign := *report
	foreign.RepoRoot = t.TempDir()
	binding, err = InspectSessionReportBinding(repo, state, &foreign)
	if err != nil {
		t.Fatal(err)
	}
	if binding.Status != SessionReportUnavailable || binding.Reason != "saved report belongs to a different repository" {
		t.Fatalf("wrong-root binding=%+v", binding)
	}
}
