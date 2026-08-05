package ingest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestRepositoryPolicySourceReadIsBounded(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "policy.yml")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxPolicySourceBytes + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readRepositorySource(root, "policy.yml"); err == nil || !strings.Contains(err.Error(), "exceeds 8388608 bytes") {
		t.Fatalf("oversized source error = %v", err)
	}
}

func TestPolicyBundleSourceCountIsBounded(t *testing.T) {
	sources := make([]policy.PolicySource, maxPolicySources+1)
	if err := validatePolicySourceBounds(sources); err == nil || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("oversized source bundle error = %v", err)
	}
}
