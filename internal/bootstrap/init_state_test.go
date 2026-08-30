package bootstrap

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordedInitInspectionClassifiesEveryCandidateState(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		inspection, err := inspectRecordedInitPlan(t.TempDir())
		if err != nil || inspection.State != recordedInitAbsent || inspection.Plan != nil {
			t.Fatalf("absent inspection = %+v err=%v", inspection, err)
		}
	})

	t.Run("read failure", func(t *testing.T) {
		repo := recordedInitCandidateFixture(t, []byte("{}\n"))
		inspection, err := inspectRecordedInitPlanWith(repo, func(string) (*Plan, error) {
			return nil, fs.ErrPermission
		})
		if !errors.Is(err, fs.ErrPermission) || inspection.State != recordedInitInvalid {
			t.Fatalf("read failure inspection = %+v err=%v", inspection, err)
		}
	})

	t.Run("digest tampering", func(t *testing.T) {
		bootstrapTestHome(t)
		repo := t.TempDir()
		plan, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileMinimal}, "test-version")
		if err != nil {
			t.Fatal(err)
		}
		plan.PlanDigest = strings.Repeat("0", 64)
		writeRecordedInitCandidate(t, repo, plan)
		inspection, err := inspectRecordedInitPlan(repo)
		if err == nil || !strings.Contains(err.Error(), "digest mismatch") || inspection.State != recordedInitInvalid {
			t.Fatalf("tampered inspection = %+v err=%v", inspection, err)
		}
	})

	t.Run("foreign repository", func(t *testing.T) {
		bootstrapTestHome(t)
		repo := t.TempDir()
		foreign := t.TempDir()
		plan, err := BuildPlan(Request{RepoRoot: foreign, Profile: ProfileMinimal}, "test-version")
		if err != nil {
			t.Fatal(err)
		}
		writeRecordedInitCandidate(t, repo, plan)
		inspection, err := inspectRecordedInitPlan(repo)
		if err == nil || !strings.Contains(err.Error(), "belongs to") || inspection.State != recordedInitInvalid {
			t.Fatalf("foreign inspection = %+v err=%v", inspection, err)
		}
	})

	t.Run("missing receipt", func(t *testing.T) {
		bootstrapTestHome(t)
		repo := t.TempDir()
		plan, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileMinimal}, "test-version")
		if err != nil {
			t.Fatal(err)
		}
		writeRecordedInitCandidate(t, repo, plan)
		inspection, err := inspectRecordedInitPlan(repo)
		if err == nil || !strings.Contains(err.Error(), "no valid matching receipt") || inspection.State != recordedInitInvalid {
			t.Fatalf("missing receipt inspection = %+v err=%v", inspection, err)
		}
	})

	t.Run("multiple candidates", func(t *testing.T) {
		repo := recordedInitCandidateFixture(t, []byte("{}\n"))
		if err := os.WriteFile(filepath.Join(repo, ".reconc", "bootstrap-plan-second.json"), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		inspection, err := inspectRecordedInitPlan(repo)
		if err == nil || !strings.Contains(err.Error(), "2 recorded init plan candidates") || inspection.State != recordedInitInvalid {
			t.Fatalf("multiple inspection = %+v err=%v", inspection, err)
		}
	})

	t.Run("valid", func(t *testing.T) {
		bootstrapTestHome(t)
		repo := t.TempDir()
		report, err := Initialize(InitRequest{RepoRoot: repo, NoHooks: true}, "test-version")
		if err != nil {
			t.Fatal(err)
		}
		inspection, err := inspectRecordedInitPlan(repo)
		if err != nil || inspection.State != recordedInitValid || inspection.Plan == nil ||
			inspection.Plan.PlanDigest != *report.PlanDigest {
			t.Fatalf("valid inspection = %+v err=%v", inspection, err)
		}
	})
}

func TestInitializeRefusesInvalidRecordedStateWithoutMutation(t *testing.T) {
	bootstrapTestHome(t)
	repo := recordedInitCandidateFixture(t, []byte("{\"format_version\":\"broken\"}\n"))
	before := bootstrapTreeSnapshot(t, repo)

	report, err := Initialize(InitRequest{RepoRoot: repo, Profile: ProfileMinimal, NoHooks: true}, "test-version")
	if err == nil || report.Status != InitRefused || report.Changed ||
		!strings.Contains(report.NextAction, "repair or remove the invalid recorded init state") {
		t.Fatalf("invalid recorded-state init = %+v err=%v", report, err)
	}
	after := bootstrapTreeSnapshot(t, repo)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("refused init mutated repository: before=%v after=%v", before, after)
	}
}

func TestInitializeReportsPostApplyVerificationMutationAsDrift(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	previousHook := beforeInitVerification
	beforeInitVerification = func(plan *Plan) error {
		return os.WriteFile(filepath.Join(plan.RepoRoot, ".reconc.yml"), []byte("rules: []\n"), 0o644)
	}
	t.Cleanup(func() { beforeInitVerification = previousHook })

	report, err := Initialize(InitRequest{RepoRoot: repo, NoHooks: true}, "test-version")
	if err == nil || report.Status != InitDrift || !report.Changed ||
		!strings.Contains(report.NextAction, "resolve the installed artifact drift") {
		t.Fatalf("post-apply verification drift = %+v err=%v", report, err)
	}
	if report.ReceiptPath == nil {
		t.Fatal("post-apply drift lost the installed receipt path")
	}
	if _, statErr := os.Stat(filepath.Join(repo, filepath.FromSlash(*report.ReceiptPath))); statErr != nil {
		t.Fatalf("post-apply drift falsely removed installed state: %v", statErr)
	}
	if !hasFailedCheck(report.Checks, "artifact:.reconc.yml") {
		t.Fatalf("post-apply drift lacks the failing artifact check: %+v", report.Checks)
	}
}

func TestInitApplyFailureStatusRequiresVerifiedRollbackEvidence(t *testing.T) {
	tests := []struct {
		name        string
		report      *Report
		err         error
		wantStatus  InitStatus
		wantChanged bool
	}{
		{name: "preflight refusal", report: &Report{}, err: errors.New("refused"), wantStatus: InitRefused},
		{name: "verified rollback", report: &Report{RolledBack: []string{"owned"}}, err: errors.New("apply failed"), wantStatus: InitRolledBack},
		{name: "incomplete rollback", report: &Report{RolledBack: []string{"one"}}, err: &applyRollbackFailure{err: errors.New("two remains")}, wantStatus: InitDrift, wantChanged: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, changed := classifyInitApplyFailure(test.report, test.err)
			if status != test.wantStatus || changed != test.wantChanged {
				t.Fatalf("classification = (%s, %t), want (%s, %t)", status, changed, test.wantStatus, test.wantChanged)
			}
		})
	}
}

func recordedInitCandidateFixture(t *testing.T, body []byte) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".reconc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".reconc", "bootstrap-plan-candidate.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	return repo
}

func writeRecordedInitCandidate(t *testing.T, repo string, plan *Plan) {
	t.Helper()
	body, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".reconc"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, ".reconc", "bootstrap-plan-candidate.json")
	if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
