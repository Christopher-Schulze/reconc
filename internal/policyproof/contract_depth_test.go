package policyproof

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/runtime"
)

func policyProofBlockingReport(repo string) *runtime.CheckReport {
	report := runtime.NewEmptyReport(
		repo,
		filepath.Join(repo, ".reconc", "policy.lock.json"),
		policy.ModeBlock,
		runtime.Empty(),
	)
	report.Violations = append(report.Violations, runtime.Violation{
		RuleID: "blocked", Kind: policy.KindDenyWrite, Mode: policy.ModeBlock,
		Message: "blocked", RecommendedAction: "fix",
	})
	report.Finalize()
	return &report
}

func TestValidateRecordShapeRejectsEveryInvalidField(t *testing.T) {
	repo := t.TempDir()
	valid, err := newRecord(repo, "check", strings.Repeat("a", 64), policyProofBlockingReport(repo))
	if err != nil {
		t.Fatalf("newRecord: %v", err)
	}
	if err := validateRecordShape(valid); err != nil {
		t.Fatalf("valid record rejected: %v", err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*Record)
		err    string
	}{
		{name: "schema", mutate: func(r *Record) { r.Schema = "other" }, err: "schema"},
		{name: "format", mutate: func(r *Record) { r.FormatVersion = "2" }, err: "format version"},
		{name: "empty event", mutate: func(r *Record) { r.Event = "" }, err: "event is empty"},
		{name: "multiline event", mutate: func(r *Record) { r.Event = "check\nstop" }, err: "one line"},
		{name: "long event", mutate: func(r *Record) { r.Event = strings.Repeat("x", 129) }, err: "at most 128"},
		{name: "relative root", mutate: func(r *Record) { r.RepoRoot = "relative" }, err: "root is not absolute"},
		{name: "empty fingerprint", mutate: func(r *Record) { r.CandidateFingerprint = "" }, err: "fingerprint is empty"},
		{name: "invalid fingerprint", mutate: func(r *Record) { r.CandidateFingerprint = "xyz" }, err: "fingerprint is invalid"},
		{name: "missing report", mutate: func(r *Record) { r.Report = nil }, err: "report is missing"},
		{name: "report repository", mutate: func(r *Record) { r.Report.RepoRoot = t.TempDir() }, err: "report repository mismatch"},
		{name: "empty report hash", mutate: func(r *Record) { r.PolicyReportHash = "" }, err: "report hash is empty"},
		{name: "invalid report hash", mutate: func(r *Record) { r.PolicyReportHash = "xyz" }, err: "report hash is invalid"},
		{name: "report schema", mutate: func(r *Record) { r.Report.Schema = "other" }, err: "report schema"},
		{name: "derived report fields", mutate: func(r *Record) { r.Report.OK = true }, err: "derived fields"},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := valid
			report := *valid.Report
			record.Report = &report
			test.mutate(&record)
			err := validateRecordShape(record)
			if err == nil || !strings.Contains(err.Error(), test.err) {
				t.Fatalf("expected %q error, got %v", test.err, err)
			}
		})
	}
}

func TestValidateRecordRejectsNestedTampering(t *testing.T) {
	repo := t.TempDir()
	valid, err := newRecord(repo, "check", strings.Repeat("b", 64), policyProofBlockingReport(repo))
	if err != nil {
		t.Fatalf("newRecord: %v", err)
	}
	if err := validateRecord(valid, repo); err != nil {
		t.Fatalf("valid record rejected: %v", err)
	}

	t.Run("report hash", func(t *testing.T) {
		record := valid
		report := *valid.Report
		report.Violations = append([]runtime.Violation(nil), valid.Report.Violations...)
		record.Report = &report
		record.Report.Violations[0].Message = "tampered"
		if err := validateRecord(record, repo); err == nil || !strings.Contains(err.Error(), "report hash mismatch") {
			t.Fatalf("expected report-hash rejection, got %v", err)
		}
	})
	t.Run("record digest", func(t *testing.T) {
		record := valid
		record.Digest = strings.Repeat("0", 64)
		if err := validateRecord(record, repo); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
			t.Fatalf("expected record-digest rejection, got %v", err)
		}
	})
	t.Run("repository identity", func(t *testing.T) {
		if err := validateRecord(valid, t.TempDir()); err == nil || !strings.Contains(err.Error(), "does not match") {
			t.Fatalf("expected repository-identity rejection, got %v", err)
		}
	})
}

func TestLoadLatestRejectsNonRegularAndTrailingData(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	path := Path(repo)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create state directory: %v", err)
	}

	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("create non-regular proof: %v", err)
	}
	if _, _, err := LoadLatest(repo); err == nil || !strings.Contains(err.Error(), "bounded regular file") {
		t.Fatalf("expected non-regular rejection, got %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove directory fixture: %v", err)
	}

	if err := os.WriteFile(path, []byte(`{} {}`), 0o600); err != nil {
		t.Fatalf("write trailing-data fixture: %v", err)
	}
	if _, _, err := LoadLatest(repo); err == nil || !strings.Contains(err.Error(), "trailing JSON") {
		t.Fatalf("expected trailing-data rejection, got %v", err)
	}
}

func TestClearRejectsNonRegularReceipt(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	path := Path(repo)
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("create directory receipt: %v", err)
	}
	if err := clear(repo); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("expected non-regular clear rejection, got %v", err)
	}
}
