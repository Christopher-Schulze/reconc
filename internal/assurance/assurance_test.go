package assurance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/policy"
)

func TestEvaluatePositiveAllGateKinds(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	writeAssuranceFile(t, root, "go.mod", "module example\n")
	writeAssuranceFile(t, root, "src/main.go", "package main\n\nfunc run() {\n\tNewGuardedClient()\n\thttp.Get(\"https://example.test\")\n\tApplyHardening()\n\texec.Command(\"tool\")\n}\n")
	writeAssuranceFile(t, root, "package.json", `{"packageManager":"npm@11.4.2","scripts":{"test":"node --test"},"dependencies":{"react":"19.1.0","local":"workspace:*"}}`)
	evidence := []byte("benchmark samples: 10, 11, 12\n")
	writeAssuranceFile(t, root, "proof/evidence.txt", string(evidence))
	hash := sha256.Sum256(evidence)
	proof := proofDocument{FormatVersion: "1", Proofs: []proofRecord{{
		ID: "proof-1", Subject: "latency", Command: "go test ./...", Outcome: "pass",
		Aggregation: "p95", Comparator: "lte", Threshold: float64Pointer(20), Actual: float64Pointer(12), Samples: []float64{10, 11, 12},
		EvidencePath: "proof/evidence.txt", EvidenceSHA256: hex.EncodeToString(hash[:]),
		VerifiedAt: now.Format(time.RFC3339),
	}}}
	body, err := json.Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	writeAssuranceFile(t, root, "proof/proofs.json", string(body))

	gates := []policy.AssuranceGate{
		{ID: "layout", Type: policy.AssuranceRepositoryLayout, AllowedRootEntries: []string{"go.mod", "src", "package.json", "proof"}, RequiredRootEntries: []string{"go.mod"}, ReservedDirs: []string{"src"}},
		{ID: "generated", Type: policy.AssuranceGeneratedReference, Commands: []string{"go generate ./..."}, CommandPolicy: "all"},
		{ID: "language", Type: policy.AssuranceLanguageBoundary, ScanPaths: []string{"src/**"}, AllowedExtensions: []string{".go"}},
		{ID: "pins", Type: policy.AssuranceDependencyPins, ManifestPaths: []string{"package.json"}, DependencySections: []string{"dependencies"}, AllowedVersionPrefixes: []string{"workspace:"}},
		{ID: "scripts", Type: policy.AssurancePackageScripts, ManifestPaths: []string{"package.json"}, PackageManager: "npm", Commands: []string{"npm run test"}},
		{ID: "network", Type: policy.AssuranceNetworkBoundary, ScanPaths: []string{"src/**"}, SitePatterns: []string{"http.Get("}, GuardMarkers: []string{"NewGuardedClient"}, MarkerWindowLines: 2},
		{ID: "process", Type: policy.AssuranceProcessBoundary, ScanPaths: []string{"src/**"}, SitePatterns: []string{"exec.Command("}, GuardMarkers: []string{"ApplyHardening"}, MarkerWindowLines: 2},
		{ID: "proof", Type: policy.AssuranceSubstantiveProof, ProofFile: "proof/proofs.json", MinSamples: 3, MaxAgeHours: 24},
		{ID: "live", Type: policy.AssuranceLiveVerification, Commands: []string{"go test ./...", "go vet ./..."}, CommandPolicy: "all"},
		{ID: "concurrency", Type: policy.AssuranceGoConcurrency, ScanPaths: []string{"src/**"}},
		{ID: "format", Type: policy.AssuranceGoFormat, ScanPaths: []string{"src/**"}},
		{ID: "hygiene", Type: policy.AssuranceSourceHygiene, ScanPaths: []string{"src/**"}},
	}
	if len(gates) != len(policy.AllAssuranceKinds()) {
		t.Fatalf("positive matrix covers %d gates, want %d", len(gates), len(policy.AllAssuranceKinds()))
	}
	covered := make(map[policy.AssuranceKind]bool, len(gates))
	for _, gate := range gates {
		if covered[gate.Type] {
			t.Fatalf("positive matrix covers assurance kind %s more than once", gate.Type)
		}
		covered[gate.Type] = true
	}
	for _, kind := range policy.AllAssuranceKinds() {
		if !covered[kind] {
			t.Fatalf("positive matrix does not cover assurance kind %s", kind)
		}
	}
	findings, err := Evaluate(root, gates, Inputs{
		ChangedPaths:       []string{"src/main.go", "package.json"},
		SuccessfulCommands: []string{"go generate ./...", "go test ./...", "go vet ./...", "npm run test"},
		Now:                now,
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected all gates to pass, got %+v", findings)
	}
}

func TestGuardBoundaryRejectsDistantMarkerBypass(t *testing.T) {
	root := t.TempDir()
	lines := []string{"package x", "// GuardedClient theater"}
	for i := 0; i < 30; i++ {
		lines = append(lines, "")
	}
	lines = append(lines, `var _ = http.Get("https://example.test")`)
	writeAssuranceFile(t, root, "src/x.go", strings.Join(lines, "\n"))
	gate := policy.AssuranceGate{ID: "network", Type: policy.AssuranceNetworkBoundary, ScanPaths: []string{"src/**"}, SitePatterns: []string{"http.Get("}, GuardMarkers: []string{"GuardedClient"}, MarkerWindowLines: 3}

	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: []string{"src/x.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || !strings.Contains(findings[0].Message, "no configured guard marker") {
		t.Fatalf("distant marker must not bypass the gate: %+v", findings)
	}

	gate.Exemptions = []policy.AssuranceExemption{{Path: "src/x.go", Reason: "loopback-only test fixture"}}
	findings, err = Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: []string{"src/x.go"}})
	if err != nil || len(findings) != 0 {
		t.Fatalf("documented exact exemption should skip the file: findings=%+v err=%v", findings, err)
	}
}

func TestGuardBoundaryRejectsCommentOnlyMarkerBypass(t *testing.T) {
	root := t.TempDir()
	writeAssuranceFile(t, root, "src/x.go", "package x\n// GuardedClient\nvar _ = http.Get(\"https://example.test\")\n")
	gate := policy.AssuranceGate{ID: "network", Type: policy.AssuranceNetworkBoundary, ScanPaths: []string{"src/**"}, SitePatterns: []string{"http.Get("}, GuardMarkers: []string{"GuardedClient"}, MarkerWindowLines: 2}

	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: []string{"src/x.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("comment-only marker must not bypass the gate: %+v", findings)
	}

	writeAssuranceFile(t, root, "src/x.go", "package x\nfunc run() { GuardedClient(); http.Get(\"https://example.test\") }\n")
	findings, err = Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: []string{"src/x.go"}})
	if err != nil || len(findings) != 0 {
		t.Fatalf("near executable guard marker should pass: findings=%+v err=%v", findings, err)
	}
}

func TestGuardBoundaryIgnoresCommentOnlySiteExamples(t *testing.T) {
	root := t.TempDir()
	writeAssuranceFile(t, root, "src/x.go", "package x\n// Never call http.Get( directly.\n")
	gate := policy.AssuranceGate{ID: "network", Type: policy.AssuranceNetworkBoundary, ScanPaths: []string{"src/**"}, SitePatterns: []string{"http.Get("}, GuardMarkers: []string{"GuardedClient"}, MarkerWindowLines: 2}
	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: []string{"src/x.go"}})
	if err != nil || len(findings) != 0 {
		t.Fatalf("comment-only site examples must not create friction: findings=%+v err=%v", findings, err)
	}
}

func TestRepositoryLayoutIsFullAuthorityNotDiffScoped(t *testing.T) {
	root := t.TempDir()
	writeAssuranceFile(t, root, "allowed/file.txt", "ok")
	writeAssuranceFile(t, root, "rogue/file.txt", "bad")
	gate := policy.AssuranceGate{ID: "layout", Type: policy.AssuranceRepositoryLayout, AllowedRootEntries: []string{"allowed"}}
	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: []string{"allowed/file.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Paths[0] != "rogue" {
		t.Fatalf("full authority scan must catch unchanged rogue root: %+v", findings)
	}
}

func TestRepositoryLayoutRejectsSymlinkOnlyReservedDirectory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeAssuranceFile(t, outside, "payload.txt", "outside")
	reserved := filepath.Join(root, "reserved")
	if err := os.MkdirAll(reserved, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "payload.txt"), filepath.Join(reserved, "payload.txt")); err != nil {
		t.Fatal(err)
	}
	gate := policy.AssuranceGate{
		ID: "layout", Type: policy.AssuranceRepositoryLayout,
		AllowedRootEntries: []string{"reserved"}, ReservedDirs: []string{"reserved"},
	}
	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || !strings.Contains(findings[0].Message, "no real content") {
		t.Fatalf("outside symlink must not satisfy reserved-directory ownership: %+v", findings)
	}
}

func TestLiteralApplicabilitySkipsAbsentSurface(t *testing.T) {
	root := t.TempDir()
	gate := policy.AssuranceGate{
		ID: "go-live", Type: policy.AssuranceLiveVerification,
		ApplicableIf: []string{"go.mod"}, Commands: []string{"go test ./..."}, CommandPolicy: "all",
	}
	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{})
	if err != nil || len(findings) != 0 {
		t.Fatalf("absent literal applicability surface should skip the gate: findings=%+v err=%v", findings, err)
	}
	writeAssuranceFile(t, root, "go.mod", "module example\n")
	findings, err = Evaluate(root, []policy.AssuranceGate{gate}, Inputs{})
	if err != nil || len(findings) != 1 {
		t.Fatalf("present literal applicability surface should activate the gate: findings=%+v err=%v", findings, err)
	}
}

func TestDependencyPinsMutation(t *testing.T) {
	root := t.TempDir()
	writeAssuranceFile(t, root, "package.json", `{"dependencies":{"react":"^19.1.0"}}`)
	gate := policy.AssuranceGate{ID: "pins", Type: policy.AssuranceDependencyPins, ManifestPaths: []string{"package.json"}, DependencySections: []string{"dependencies"}}
	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: []string{"package.json"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || !strings.Contains(findings[0].Message, "not exactly pinned") {
		t.Fatalf("floating range mutation must fail: %+v", findings)
	}
	writeAssuranceFile(t, root, "package.json", `{"dependencies":{"react":"19.1.0"}}`)
	findings, err = Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: []string{"package.json"}})
	if err != nil || len(findings) != 0 {
		t.Fatalf("exact pin should pass: findings=%+v err=%v", findings, err)
	}
}

func TestSubstantiveProofRejectsStaleHashAndMissingLiveCommand(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	writeAssuranceFile(t, root, "evidence.txt", "actual\n")
	document := proofDocument{FormatVersion: "1", Proofs: []proofRecord{{
		ID: "proof-1", Subject: "throughput", Command: "go test ./...", Outcome: "pass",
		Aggregation: "mean", Comparator: "gte", Threshold: float64Pointer(10), Actual: float64Pointer(11), Samples: []float64{10, 11, 12},
		EvidencePath: "evidence.txt", EvidenceSHA256: strings.Repeat("0", 64),
		VerifiedAt: now.Add(-48 * time.Hour).Format(time.RFC3339),
	}}}
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	writeAssuranceFile(t, root, "proofs.json", string(body))
	gate := policy.AssuranceGate{ID: "proof", Type: policy.AssuranceSubstantiveProof, ProofFile: "proofs.json", MinSamples: 3, MaxAgeHours: 24}
	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	joined := findingsText(findings)
	for _, expected := range []string{"no current successful runtime evidence", "verified_at is invalid", "evidence hash mismatch"} {
		if !strings.Contains(joined, expected) {
			t.Errorf("expected %q in findings: %s", expected, joined)
		}
	}
}

func TestSubstantiveProofRejectsFabricatedActualAndFailedThreshold(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	evidence := []byte("measured samples: 10, 11, 12\n")
	writeAssuranceFile(t, root, "evidence.txt", string(evidence))
	hash := sha256.Sum256(evidence)
	document := proofDocument{FormatVersion: "1", Proofs: []proofRecord{{
		ID: "proof-1", Subject: "latency", Command: "go test ./...", Outcome: "pass",
		Aggregation: "p95", Comparator: "lte", Threshold: float64Pointer(9), Actual: float64Pointer(1), Samples: []float64{10, 11, 12},
		EvidencePath: "evidence.txt", EvidenceSHA256: hex.EncodeToString(hash[:]), VerifiedAt: now.Format(time.RFC3339),
	}}}
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	writeAssuranceFile(t, root, "proofs.json", string(body))
	gate := policy.AssuranceGate{ID: "proof", Type: policy.AssuranceSubstantiveProof, ProofFile: "proofs.json", MinSamples: 3, MaxAgeHours: 24}

	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{SuccessfulCommands: []string{"go test ./..."}, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(findingsText(findings), "actual 1 does not match p95(samples) 12") {
		t.Fatalf("fabricated actual must fail: %+v", findings)
	}

	document.Proofs[0].Actual = float64Pointer(12)
	body, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	writeAssuranceFile(t, root, "proofs.json", string(body))
	findings, err = Evaluate(root, []policy.AssuranceGate{gate}, Inputs{SuccessfulCommands: []string{"go test ./..."}, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(findingsText(findings), "does not satisfy lte 9") {
		t.Fatalf("failed measured threshold must fail: %+v", findings)
	}
}

func TestFindingOutputIsBoundedAtScale(t *testing.T) {
	root := t.TempDir()
	changed := []string{}
	for i := 0; i < 100; i++ {
		name := filepath.ToSlash(filepath.Join("src", strings.Repeat("x", i%10)+string(rune('a'+i%26))+".ts"))
		if containsString(changed, name) {
			name = filepath.ToSlash(filepath.Join("src", string(rune('a'+i/26))+name[4:]))
		}
		writeAssuranceFile(t, root, name, "export {}\n")
		changed = append(changed, name)
	}
	gate := policy.AssuranceGate{ID: "language", Type: policy.AssuranceLanguageBoundary, ScanPaths: []string{"src/**"}, AllowedExtensions: []string{".go"}}
	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: changed})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != maxFindings+1 || findings[len(findings)-1].GateID != "assurance-budget" {
		t.Fatalf("expected bounded findings plus overflow marker, got %d: %+v", len(findings), findings[len(findings)-1])
	}
}

func TestReadBudgetCoversAggregateDistinctFileIO(t *testing.T) {
	root := t.TempDir()
	writeAssuranceFile(t, root, "src/large.go", strings.Repeat("x", maxFileBytes))
	changed := []string{"src/large.go"}
	for index := 1; index < 9; index++ {
		relative := filepath.ToSlash(filepath.Join("src", "large-"+strconv.Itoa(index)+".go"))
		if err := os.Link(filepath.Join(root, "src", "large.go"), filepath.Join(root, filepath.FromSlash(relative))); err != nil {
			t.Fatal(err)
		}
		changed = append(changed, relative)
	}
	gate := policy.AssuranceGate{
		ID: "network", Type: policy.AssuranceNetworkBoundary,
		ScanPaths: []string{"src/**"}, SitePatterns: []string{"http.Get("},
		GuardMarkers: []string{"GuardedClient"}, MarkerWindowLines: 2,
	}
	_, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: changed})
	if err == nil || !strings.Contains(err.Error(), "assurance read budget exceeded") {
		t.Fatalf("expected total read budget failure, got %v", err)
	}
}

func TestRepeatedSourceGatesReuseOneFileRead(t *testing.T) {
	root := t.TempDir()
	writeAssuranceFile(t, root, "src/large.go", strings.Repeat("x", maxFileBytes))
	gates := make([]policy.AssuranceGate, 0, 9)
	for index := 0; index < 9; index++ {
		gates = append(gates, policy.AssuranceGate{
			ID: "network-" + strconv.Itoa(index), Type: policy.AssuranceNetworkBoundary,
			ScanPaths: []string{"src/**"}, SitePatterns: []string{"http.Get("},
			GuardMarkers: []string{"GuardedClient"}, MarkerWindowLines: 2,
		})
	}
	findings, err := Evaluate(root, gates, Inputs{ChangedPaths: []string{"src/large.go"}})
	if err != nil || len(findings) != 0 {
		t.Fatalf("repeated gates should share one bounded file snapshot: findings=%+v err=%v", findings, err)
	}
}

func BenchmarkEvaluateChangedSourceGates(b *testing.B) {
	root := b.TempDir()
	changed := make([]string, 0, 1_000)
	for index := 0; index < 1_000; index++ {
		relative := filepath.ToSlash(filepath.Join("src", "pkg", "file-"+strconv.Itoa(index)+".go"))
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			b.Fatal(err)
		}
		content := "package pkg\n\nfunc run() {\n\tGuardedClient()\n\thttp.Get(\"https://example.test\")\n\tApplyHardening()\n\texec.Command(\"tool\")\n}\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			b.Fatal(err)
		}
		changed = append(changed, relative)
	}
	gates := mixedSourceBenchmarkGates()
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		findings, err := Evaluate(root, gates, Inputs{ChangedPaths: changed})
		if err != nil {
			b.Fatal(err)
		}
		if len(findings) != 0 {
			b.Fatalf("unexpected findings: %+v", findings)
		}
	}
}

func writeAssuranceFile(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func findingsText(findings []Finding) string {
	parts := make([]string, 0, len(findings))
	for _, finding := range findings {
		parts = append(parts, finding.Message)
	}
	return strings.Join(parts, "\n")
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func float64Pointer(value float64) *float64 {
	return &value
}

func TestSubstantiveProofEmptySamplesDoesNotPanic(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	evidence := []byte("measured\n")
	writeAssuranceFile(t, root, "evidence.txt", string(evidence))
	hash := sha256.Sum256(evidence)
	document := proofDocument{FormatVersion: "1", Proofs: []proofRecord{{
		ID: "proof-1", Subject: "latency", Command: "go test ./...", Outcome: "pass",
		Aggregation: "last", Comparator: "lte", Threshold: float64Pointer(9), Actual: float64Pointer(9), Samples: []float64{},
		EvidencePath: "evidence.txt", EvidenceSHA256: hex.EncodeToString(hash[:]), VerifiedAt: now.Format(time.RFC3339),
	}}}
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	writeAssuranceFile(t, root, "proofs.json", string(body))
	// MinSamples 0 models a hand-edited lockfile that dropped the
	// parser-defaulted floor; the gate must fail with a finding, not
	// panic on the empty aggregation input.
	gate := policy.AssuranceGate{ID: "proof", Type: policy.AssuranceSubstantiveProof, ProofFile: "proofs.json", MinSamples: 0, MaxAgeHours: 24}
	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{SuccessfulCommands: []string{"go test ./..."}, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(findingsText(findings), "samples must contain at least one value") {
		t.Fatalf("expected empty-samples finding, got: %+v", findings)
	}
}
