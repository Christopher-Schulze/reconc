package policyproof_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/policyproof"
	reconcruntime "reconc.dev/reconc/internal/runtime"
)

func TestStoreLoadAndTamperDetection(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	report := blockingReport(repo)
	fingerprint := strings.Repeat("a", 64)

	if err := policyproof.Store(repo, "check", fingerprint, report); err != nil {
		t.Fatalf("Store: %v", err)
	}
	record, found, err := policyproof.LoadLatest(repo)
	if err != nil || !found {
		t.Fatalf("LoadLatest: found=%v err=%v", found, err)
	}
	if record.CandidateFingerprint != fingerprint || record.Report.Decision != reconcruntime.DecisionBlock {
		t.Fatalf("unexpected record: %#v", record)
	}

	path := policyproof.Path(repo)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(body), `"event": "check"`, `"event": "ci"`, 1)
	if err := os.WriteFile(path, []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := policyproof.LoadLatest(repo); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("tampered receipt was accepted: %v", err)
	}
}

func TestStoreRejectsInconsistentReportAndFingerprint(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	report := passingReport(repo)
	report.Decision = reconcruntime.DecisionBlock
	if err := policyproof.Store(repo, "check", strings.Repeat("b", 64), report); err == nil || !strings.Contains(err.Error(), "derived fields") {
		t.Fatalf("inconsistent report was accepted: %v", err)
	}

	report = passingReport(repo)
	if err := policyproof.Store(repo, "check", "not-a-digest", report); err == nil || !strings.Contains(err.Error(), "fingerprint is invalid") {
		t.Fatalf("invalid fingerprint was accepted: %v", err)
	}
	for _, fingerprint := range []string{strings.Repeat("A", 64), " " + strings.Repeat("a", 64), strings.Repeat("a", 64) + " "} {
		if err := policyproof.Store(repo, "check", fingerprint, report); err == nil || !strings.Contains(err.Error(), "fingerprint is invalid") {
			t.Fatalf("non-canonical fingerprint %q was accepted: %v", fingerprint, err)
		}
	}

	report = passingReport(repo)
	report.Schema = "https://example.invalid/report.schema.json"
	if err := policyproof.Store(repo, "check", strings.Repeat("b", 64), report); err == nil || !strings.Contains(err.Error(), "schema or format version") {
		t.Fatalf("invalid report schema was accepted: %v", err)
	}
}

func TestNonBlockingDecisionDurablyClearsOlderBlock(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	fingerprint := strings.Repeat("d", 64)
	if err := policyproof.Store(repo, "check", fingerprint, blockingReport(repo)); err != nil {
		t.Fatal(err)
	}
	if err := policyproof.Store(repo, "check", fingerprint, passingReport(repo)); err != nil {
		t.Fatal(err)
	}
	if _, found, err := policyproof.LoadLatest(repo); err != nil || found {
		t.Fatalf("corrected pass left an unresolved block: found=%v err=%v", found, err)
	}
	if _, err := os.Stat(policyproof.Path(repo)); !os.IsNotExist(err) {
		t.Fatalf("cleared proof still exists: %v", err)
	}
}

func TestRepositoryAliasesShareOneReceipt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory symlink creation is not guaranteed for unprivileged Windows tests")
	}
	t.Setenv("RECONC_HOME", t.TempDir())
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	alias := filepath.Join(root, "alias")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	report := blockingReport(repo)
	if err := policyproof.Store(alias, "check", strings.Repeat("c", 64), report); err != nil {
		t.Fatalf("Store(alias): %v", err)
	}
	if policyproof.Path(alias) != policyproof.Path(repo) {
		t.Fatalf("aliases diverged: %q != %q", policyproof.Path(alias), policyproof.Path(repo))
	}
	if _, found, err := policyproof.LoadLatest(repo); err != nil || !found {
		t.Fatalf("LoadLatest(canonical): found=%v err=%v", found, err)
	}
}

func passingReport(repo string) *reconcruntime.CheckReport {
	report := reconcruntime.NewEmptyReport(
		repo,
		filepath.Join(repo, ".reconc", "policy.lock.json"),
		policy.ModeBlock,
		reconcruntime.Empty(),
	)
	report.Finalize()
	return &report
}

func blockingReport(repo string) *reconcruntime.CheckReport {
	report := passingReport(repo)
	report.Violations = append(report.Violations, reconcruntime.Violation{
		RuleID: "blocked", Kind: policy.KindDenyWrite, Mode: policy.ModeBlock,
		Message: "blocked", RecommendedAction: "fix the block",
	})
	report.Finalize()
	return report
}
