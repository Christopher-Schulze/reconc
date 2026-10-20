package assurance

import (
	"fmt"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/stackdetect"
)

func TestEvaluationSharesDiscoveryAndModuleFactsAcrossRules(t *testing.T) {
	root := t.TempDir()
	writeAssuranceFile(t, root, "services/api/go.mod", "module example/api\n")
	writeAssuranceFile(t, root, "services/api/main.go", "package api\nfunc answer() { }\n")
	previous := detectModuleRoots
	t.Cleanup(func() { detectModuleRoots = previous })
	detections := 0
	detectModuleRoots = func(root string) (stackdetect.Result, error) {
		detections++
		return previous(root)
	}
	evaluation, err := NewEvaluation(root, Inputs{ChangedPaths: []string{"services/api/main.go"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first-rule", "second-rule"} {
		gate := policy.AssuranceGate{ID: id, Type: policy.AssuranceGoFormat,
			ApplicableIf: []string{"go.mod"}, ScanPaths: []string{"**/*.go"}}
		findings, err := evaluation.Evaluate([]policy.AssuranceGate{gate})
		if err != nil || len(findings) != 1 || findings[0].GateID != id {
			t.Fatalf("per-rule findings changed: %+v, err=%v", findings, err)
		}
	}
	stats := evaluation.state.analysisStats()
	if detections != 1 || len(evaluation.state.scoped) != 1 || stats.BodyReads != 1 || stats.GoFormats != 1 {
		t.Fatalf("repeated rule work: detection=%d scopes=%d stats=%+v", detections, len(evaluation.state.scoped), stats)
	}
}

// writeGoFiles creates count formatted Go files of at least size bytes under
// directory and returns their repository-relative paths.
func writeGoFiles(t *testing.T, root, directory string, count, size int) []string {
	t.Helper()
	content := "package " + directory + "\n"
	if padding := size - len(content) - len("//\n"); padding > 0 {
		content += "//" + strings.Repeat("x", padding) + "\n"
	}
	paths := make([]string, 0, count)
	for index := range count {
		relative := fmt.Sprintf("%s/f%04d.go", directory, index)
		writeAssuranceFile(t, root, relative, content)
		paths = append(paths, relative)
	}
	return paths
}

func goFormatGate(id string, scan ...string) []policy.AssuranceGate {
	return []policy.AssuranceGate{{ID: id, Type: policy.AssuranceGoFormat, ScanPaths: scan}}
}

func TestEvaluationBudgetsEachRuleSeparately(t *testing.T) {
	root := t.TempDir()
	changed := append(writeGoFiles(t, root, "a", 3000, 0), writeGoFiles(t, root, "b", 3000, 0)...)
	evaluation, err := NewEvaluation(root, Inputs{ChangedPaths: changed})
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range [][]policy.AssuranceGate{goFormatGate("first-rule", "a/**"), goFormatGate("second-rule", "b/**")} {
		if findings, err := evaluation.Evaluate(rule); err != nil || len(findings) != 0 {
			t.Fatalf("a rule within its own budget failed after another rule: %+v, err=%v", findings, err)
		}
	}
}

func TestEvaluationChargesSharedFactsToEveryRule(t *testing.T) {
	// 4,000 files of 8.5 KiB stay under the file budget but exceed the 32 MiB
	// read budget; the first rule reads 3,000 of them within its own budget.
	root := t.TempDir()
	changed := append(writeGoFiles(t, root, "a", 3000, 8704), writeGoFiles(t, root, "b", 1000, 8704)...)
	alone, err := NewEvaluation(root, Inputs{ChangedPaths: changed})
	if err != nil {
		t.Fatal(err)
	}
	_, aloneErr := alone.Evaluate(goFormatGate("wide-rule", "**/*.go"))
	if aloneErr == nil || !strings.Contains(aloneErr.Error(), "budget exceeded") {
		t.Fatalf("a rule over its own budget passed: %v", aloneErr)
	}
	evaluation, err := NewEvaluation(root, Inputs{ChangedPaths: changed})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := evaluation.Evaluate(goFormatGate("narrow-rule", "a/**")); err != nil {
		t.Fatal(err)
	}
	if _, err := evaluation.Evaluate(goFormatGate("wide-rule", "**/*.go")); err == nil || err.Error() != aloneErr.Error() {
		t.Fatalf("a rule reusing another rule's facts was not charged for them: %v, want %v", err, aloneErr)
	}
	if reads := evaluation.state.analysisStats().BodyReads; reads > int64(len(changed)) {
		t.Fatalf("rules no longer share file facts: %d body reads for %d files", reads, len(changed))
	}
}

func TestEvaluationChargesCachedListingsToEveryRule(t *testing.T) {
	root := t.TempDir()
	changed := append(writeGoFiles(t, root, "a", 3000, 0), writeGoFiles(t, root, "b", 1500, 0)...)
	boundary := policy.AssuranceGate{ID: "boundary", Type: policy.AssuranceLanguageBoundary,
		ScanPaths: []string{"a/**"}, AllowedExtensions: []string{".go"}}
	evaluation, err := NewEvaluation(root, Inputs{ChangedPaths: changed})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := evaluation.Evaluate([]policy.AssuranceGate{boundary}); err != nil {
		t.Fatal(err)
	}
	// The second rule lists the same 3,000 files from the cached listing and
	// reads 1,500 more: 4,500 files exceed its own budget.
	_, err = evaluation.Evaluate(append([]policy.AssuranceGate{boundary}, goFormatGate("format", "b/**")...))
	if err == nil || !strings.Contains(err.Error(), "assurance file budget exceeded") {
		t.Fatalf("a rule reusing a cached listing was not charged for it: %v", err)
	}
}
