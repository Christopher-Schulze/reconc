package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/actionevidence"
	"reconc.dev/reconc/internal/actionledger"
)

func TestActionEvidenceExportAndVerifyUseCurrentVerifiedRuntimeEvidence(t *testing.T) {
	repository, home, _ := createActionLogFixture(t)
	t.Setenv("RECONC_HOME", home)
	arguments := []string{
		repository, "--as-of", "2026-08-12T00:00:00Z",
		"--since", "2026-08-11T11:00:00Z", "--until", "2026-08-11T13:00:00Z",
	}
	var stdout bytes.Buffer
	if err := runAction([]string{"evidence", "export", arguments[0], arguments[1], arguments[2], arguments[3], arguments[4], arguments[5], arguments[6]}, &stdout); err != nil {
		t.Fatal(err)
	}
	var report actionevidence.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Schema != actionevidence.ReportSchema || report.Window.SelectedCalls != 1 ||
		report.Policy.ToolCount != 1 || report.Policy.RuleCount != 1 ||
		report.Ledger.Integrity != "verified" || report.RepositoryIdentity == "unavailable" ||
		len(report.MappingPacks) != 4 || len(report.Controls) == 0 || report.Identity == "" {
		t.Fatalf("evidence export = %#v", report)
	}
	if _, err := actionevidence.DecodeReport(stdout.Bytes()); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	verifyArguments := append([]string{"evidence", "verify"}, arguments...)
	verifyArguments = append(verifyArguments, "--json")
	if err := runAction(verifyArguments, &stdout); err == nil || !strings.Contains(err.Error(), "technical evidence status") {
		t.Fatalf("verify error = %v, output = %s", err, stdout.String())
	}
	if _, err := actionevidence.DecodeReport(stdout.Bytes()); err != nil {
		t.Fatalf("verify did not emit its exact downgraded report: %v", err)
	}
}

func TestActionEvidenceRefusesAmbiguousTimePackAndOutputOptions(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing as of", args: []string{"evidence", "export", "."}},
		{name: "non utc as of", args: []string{"evidence", "export", ".", "--as-of", "2026-08-12T12:00:00+02:00"}},
		{name: "inverted window", args: []string{"evidence", "export", ".", "--as-of", "2026-08-12T12:00:00Z", "--since", "2026-08-12T11:00:00Z", "--until", "2026-08-12T10:00:00Z"}},
		{name: "unsupported format", args: []string{"evidence", "export", ".", "--as-of", "2026-08-12T12:00:00Z", "--format", "pdf"}},
		{name: "pack without auth", args: []string{"evidence", "export", ".", "--as-of", "2026-08-12T12:00:00Z", "--map-pack", "pack.json"}},
		{name: "digest count mismatch", args: []string{"evidence", "export", ".", "--as-of", "2026-08-12T12:00:00Z", "--map-pack", "one.json", "--map-pack", "two.json", "--map-pack-digest", "sha256:" + strings.Repeat("1", 64)}},
		{name: "verify output", args: []string{"evidence", "verify", ".", "--as-of", "2026-08-12T12:00:00Z", "--output", "report.json"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := runAction(test.args, &bytes.Buffer{}); err == nil {
				t.Fatalf("ambiguous evidence arguments were accepted: %v", test.args)
			}
		})
	}
}

func TestActionEvidenceTimeFlagsRequireCanonicalZuluForm(t *testing.T) {
	for _, value := range []string{"2020-01-01T00:00:00+00:00", "2020-01-01T02:00:00+02:00"} {
		var options actionEvidenceOptions
		if err := bindActionEvidenceValue(&options, "--as-of", value); err == nil {
			t.Fatalf("non-canonical UTC timestamp %q was accepted", value)
		}
	}
}

func TestActionEvidenceExportRefusesExistingOutput(t *testing.T) {
	repository, home, _ := createActionLogFixture(t)
	t.Setenv("RECONC_HOME", home)
	output := filepath.Join(t.TempDir(), "evidence.json")
	original := []byte("operator-owned\n")
	if err := os.WriteFile(output, original, 0o600); err != nil {
		t.Fatal(err)
	}
	err := runAction([]string{
		"evidence", "export", repository, "--as-of", "2026-08-12T00:00:00Z", "--output", output,
	}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("existing output was overwritten")
	}
	body, readErr := os.ReadFile(output)
	if readErr != nil || !bytes.Equal(body, original) {
		t.Fatalf("existing output changed: %q, %v", body, readErr)
	}
}

func TestActionEvidenceLedgerResampleRequiresExactVerificationState(t *testing.T) {
	initial := actionledger.VerificationReport{
		FormatVersion: actionledger.FormatVersion, Integrity: actionledger.StatusEmpty,
		ArchiveContinuity: actionledger.StatusEmpty, DetachedHead: actionledger.HeadAbsent,
		EventsEvaluated: true, EventsComplete: true, CallsEvaluated: true, CallsComplete: true,
	}
	if !sameEvidenceLedgerSnapshot(nil, initial, nil, initial) {
		t.Fatal("identical empty ledger verification state was rejected")
	}
	changed := initial
	changed.Integrity = actionledger.StatusInvalid
	if sameEvidenceLedgerSnapshot(nil, initial, nil, changed) {
		t.Fatal("ledger integrity mutation with unchanged records was accepted")
	}
}
