//go:build !windows

package usercli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGitHubCLI puts an executable named gh first on PATH; lifecycleCommand
// decides what the verification reports.
func fakeGitHubCLI(t *testing.T) {
	t.Helper()
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "gh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestOnlyTheGitHubCLIProvesGitHubProvenance(t *testing.T) {
	previousCommand := lifecycleCommand
	t.Cleanup(func() { lifecycleCommand = previousCommand })
	release := selectedRelease{manifest: ReleaseManifest{Tag: "reconc-v1.0.0"}}
	lifecycleCommand = lifecycleHelperCommandWithOutput("0", "verified")

	fakeGitHubCLI(t)
	t.Setenv("RECONC_ATTESTATION_TOOL", "")
	if state, err := verifyAttestation(context.Background(), "candidate", release); err != nil || state != ProvenanceGitHubVerified {
		t.Fatalf("gh verification = %q, %v", state, err)
	}
	t.Setenv("RECONC_ATTESTATION_TOOL", os.Args[0])
	if state, err := verifyAttestation(context.Background(), "candidate", release); err != nil || state != ProvenanceEmbeddedVerified {
		t.Fatalf("overridden verifier must not claim GitHub provenance: %q, %v", state, err)
	}
}

func TestEnvironmentGitHubProvenanceClaimIsVerified(t *testing.T) {
	previousCommand := lifecycleCommand
	t.Cleanup(func() { lifecycleCommand = previousCommand })
	t.Setenv("RECONC_ATTESTATION_TOOL", "")

	t.Setenv("PATH", t.TempDir())
	state, err := verifiedInstallProvenance(ProvenanceGitHubVerified, "binary", "reconc-v1.0.0")
	if err != nil || state != ProvenanceEmbeddedVerified {
		t.Fatalf("a claim without a verifier must keep the checksum state: %q, %v", state, err)
	}

	fakeGitHubCLI(t)
	lifecycleCommand = lifecycleHelperCommandWithOutput("1", "no attestation")
	if _, err := verifiedInstallProvenance(ProvenanceGitHubVerified, "binary", "reconc-v1.0.0"); err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Fatalf("a failing claim must refuse the receipt: %v", err)
	}
	lifecycleCommand = lifecycleHelperCommandWithOutput("0", "verified")
	if state, err := verifiedInstallProvenance(ProvenanceGitHubVerified, "binary", "reconc-v1.0.0"); err != nil || state != ProvenanceGitHubVerified {
		t.Fatalf("a verified claim = %q, %v", state, err)
	}
	if state, err := verifiedInstallProvenance(ProvenanceEmbeddedVerified, "binary", "reconc-v1.0.0"); err != nil || state != ProvenanceEmbeddedVerified {
		t.Fatalf("an embedded claim passes through: %q, %v", state, err)
	}
}
