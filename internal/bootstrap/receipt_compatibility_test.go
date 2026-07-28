package bootstrap

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInstallReceiptAcceptsLegacyV1WithoutHarnessPacks(t *testing.T) {
	type legacyReceipt struct {
		FormatVersion  string                `json:"format_version"`
		ProductVersion string                `json:"product_version"`
		RepoRoot       string                `json:"repo_root"`
		PlanDigest     string                `json:"plan_digest"`
		Entries        []InstallReceiptEntry `json:"entries"`
		Digest         string                `json:"digest"`
	}

	plan := &Plan{
		ProductVersion: "0.8.8",
		RepoRoot:       t.TempDir(),
		PlanDigest:     strings.Repeat("a", 64),
		Selection:      Selection{},
	}
	legacy := legacyReceipt{
		FormatVersion:  InstallReceiptFormatVersion,
		ProductVersion: plan.ProductVersion,
		RepoRoot:       plan.RepoRoot,
		PlanDigest:     plan.PlanDigest,
		Entries:        []InstallReceiptEntry{},
	}
	digestBody, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Digest = bytesSHA256(digestBody)
	body, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}

	var current InstallReceipt
	if err := json.Unmarshal(body, &current); err != nil {
		t.Fatal(err)
	}
	if err := validateInstallReceipt(plan, &current); err != nil {
		t.Fatalf("legacy v1 receipt rejected after harness-pack addition: %v", err)
	}
}
