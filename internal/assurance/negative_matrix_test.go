package assurance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"reconc.dev/reconc/internal/policy"
)

// TestEvaluateNegativeAllGateKinds is the complement of the positive matrix.
// That one proves a satisfied repository produces no finding, which a gate
// that never fires would also satisfy. This one drives every registered kind
// into its violation and requires a finding, so a gate cannot silently stop
// blocking.
func TestEvaluateNegativeAllGateKinds(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	writeAssuranceFile(t, root, "go.mod", "module example\n")
	writeAssuranceFile(t, root, "stray.txt", "unexpected root entry\n")
	// Unguarded network and process sites, a bare goroutine, a debt marker,
	// and deliberately unformatted source, all in one changed file.
	writeAssuranceFile(t, root, "src/main.go", "package main\n\nfunc run()  {\n\thttp.Get(\"https://example.test\")\n\texec.Command(\"tool\")\n\tgo func() { work() }()\n\t// TODO: finish this\n}\n")
	writeAssuranceFile(t, root, "src/note.py", "print('not go')\n")
	writeAssuranceFile(t, root, "package.json", `{"packageManager":"npm@11.4.2","scripts":{"test":"node --test"},"dependencies":{"react":"^19.1.0"}}`)
	evidence := []byte("benchmark samples: 10\n")
	writeAssuranceFile(t, root, "proof/evidence.txt", string(evidence))
	hash := sha256.Sum256(evidence)
	proof := proofDocument{FormatVersion: "1", Proofs: []proofRecord{{
		ID: "proof-1", Subject: "latency", Command: "go test ./...", Outcome: "pass",
		Aggregation: "p95", Comparator: "lte", Threshold: float64Pointer(20), Actual: float64Pointer(12), Samples: []float64{10},
		EvidencePath: "proof/evidence.txt", EvidenceSHA256: hex.EncodeToString(hash[:]),
		VerifiedAt: now.Format(time.RFC3339),
	}}}
	body, err := json.Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	writeAssuranceFile(t, root, "proof/proofs.json", string(body))

	violations := []policy.AssuranceGate{
		// stray.txt is present but not allowed at the root.
		{ID: "layout", Type: policy.AssuranceRepositoryLayout, AllowedRootEntries: []string{"go.mod", "src", "package.json", "proof"}, RequiredRootEntries: []string{"go.mod"}},
		// The generator command was never run.
		{ID: "generated", Type: policy.AssuranceGeneratedReference, Commands: []string{"go generate ./..."}, CommandPolicy: "all"},
		// A .py file changed under a Go-only scan surface.
		{ID: "language", Type: policy.AssuranceLanguageBoundary, ScanPaths: []string{"src/**"}, AllowedExtensions: []string{".go"}},
		// The caret range is not a pinned version.
		{ID: "pins", Type: policy.AssuranceDependencyPins, ManifestPaths: []string{"package.json"}, DependencySections: []string{"dependencies"}},
		// The declared script was never run.
		{ID: "scripts", Type: policy.AssurancePackageScripts, ManifestPaths: []string{"package.json"}, PackageManager: "npm", Commands: []string{"npm run test"}},
		// The site exists with no guard marker in range.
		{ID: "network", Type: policy.AssuranceNetworkBoundary, ScanPaths: []string{"src/**"}, SitePatterns: []string{"http.Get("}, GuardMarkers: []string{"NewGuardedClient"}, MarkerWindowLines: 1},
		{ID: "process", Type: policy.AssuranceProcessBoundary, ScanPaths: []string{"src/**"}, SitePatterns: []string{"exec.Command("}, GuardMarkers: []string{"ApplyHardening"}, MarkerWindowLines: 1},
		// One sample is below the declared minimum.
		{ID: "proof", Type: policy.AssuranceSubstantiveProof, ProofFile: "proof/proofs.json", MinSamples: 3, MaxAgeHours: 24},
		// The verification command was never run.
		{ID: "live", Type: policy.AssuranceLiveVerification, Commands: []string{"go test ./..."}, CommandPolicy: "all"},
		{ID: "concurrency", Type: policy.AssuranceGoConcurrency, ScanPaths: []string{"src/**"}},
		{ID: "format", Type: policy.AssuranceGoFormat, ScanPaths: []string{"src/**"}},
		{ID: "hygiene", Type: policy.AssuranceSourceHygiene, ScanPaths: []string{"src/**"}},
	}
	if len(violations) != len(policy.AllAssuranceKinds()) {
		t.Fatalf("negative matrix covers %d gates, want %d", len(violations), len(policy.AllAssuranceKinds()))
	}
	covered := map[policy.AssuranceKind]bool{}
	for _, gate := range violations {
		if covered[gate.Type] {
			t.Fatalf("negative matrix covers assurance kind %s more than once", gate.Type)
		}
		covered[gate.Type] = true
	}
	for _, kind := range policy.AllAssuranceKinds() {
		if !covered[kind] {
			t.Fatalf("negative matrix does not cover assurance kind %s", kind)
		}
	}

	// Each gate is evaluated on its own so one gate's finding cannot stand in
	// for another's silence.
	for _, gate := range violations {
		findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{
			ChangedPaths: []string{"src/main.go", "src/note.py", "package.json", "stray.txt"},
			Now:          now,
		})
		if err != nil {
			t.Fatalf("gate %s: Evaluate: %v", gate.ID, err)
		}
		if len(findings) == 0 {
			t.Fatalf("gate %s produced no finding for a violating repository", gate.ID)
		}
		for _, finding := range findings {
			if finding.GateID != gate.ID {
				t.Fatalf("gate %s produced a finding attributed to %s", gate.ID, finding.GateID)
			}
			if finding.Message == "" || finding.Remediation == "" {
				t.Fatalf("gate %s produced a finding without a message or remediation: %+v", gate.ID, finding)
			}
		}
	}
}
