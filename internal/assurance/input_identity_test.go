package assurance

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"reconc.dev/reconc/internal/policy"
)

func TestEvaluateWithInputIdentityBindsAuthorityContentAndDerivedLayout(t *testing.T) {
	repo := t.TempDir()
	proofPath := filepath.Join(repo, "proof.json")
	evidencePath := filepath.Join(repo, "evidence.txt")
	if err := os.WriteFile(evidencePath, []byte("measured\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(proofPath, []byte(`{"format_version":"1","proofs":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	gates := []policy.AssuranceGate{{
		ID: "proof", Type: policy.AssuranceSubstantiveProof,
		ProofFile: "proof.json", MinSamples: 1, MaxAgeHours: 24,
	}}
	inputs := Inputs{Now: time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)}
	_, first, err := EvaluateWithInputIdentity(repo, gates, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(proofPath, []byte(`{"format_version":"1","proofs":[{}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, second, err := EvaluateWithInputIdentity(repo, gates, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first == second {
		t.Fatal("assurance input identity ignored proof content drift")
	}

	layout := []policy.AssuranceGate{{ID: "layout", Type: policy.AssuranceRepositoryLayout}}
	_, beforeLayout, err := EvaluateWithInputIdentity(repo, layout, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "new-root-entry"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, afterLayout, err := EvaluateWithInputIdentity(repo, layout, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if beforeLayout == afterLayout {
		t.Fatal("assurance input identity ignored repository-layout drift")
	}
}
