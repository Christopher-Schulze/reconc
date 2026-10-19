package runtime

import (
	"fmt"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/pathidentity"
)

func TestEvaluationInputNormalizesEachWritePathOnce(t *testing.T) {
	repo := t.TempDir()
	calls := map[string]int{}
	previous := normalizeEvidencePath
	t.Cleanup(func() { normalizeEvidencePath = previous })
	normalizeEvidencePath = func(raw, resolvedRoot string, prospective *pathidentity.ProspectiveResolver) (string, bool, error) {
		calls[raw]++
		return previous(raw, resolvedRoot, prospective)
	}
	inputs := ExecutionInputs{WriteEpochs: map[string]uint64{}}
	for index := 0; index < 50; index++ {
		write := fmt.Sprintf("src/file%02d.go", index)
		inputs.WritePaths = append(inputs.WritePaths, write)
		inputs.WriteEpochs[write] = uint64(index + 1)
	}
	inputs.ReadPaths = []string{"docs/read.md"}
	normalized, err := normalizeEvaluationInput(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized.inputs.WritePaths) != 50 || normalized.inputs.WriteEpochs["src/file49.go"] != 50 {
		t.Fatalf("normalized writes=%d epoch=%d", len(normalized.inputs.WritePaths), normalized.inputs.WriteEpochs["src/file49.go"])
	}
	for raw, count := range calls {
		if count != 1 {
			t.Fatalf("%s normalized %d times, want once", raw, count)
		}
	}
	if len(calls) != 51 {
		t.Fatalf("normalized %d distinct paths, want 51", len(calls))
	}
}

func TestEvaluationInputCollapsesReadPathSpellings(t *testing.T) {
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	inputs := ExecutionInputs{ReadPaths: []string{"docs/a.md", filepath.Join(repo, "docs", "a.md"), "docs/./a.md", "docs/b.md"}}
	normalized, err := normalizeEvaluationInput(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(normalized.inputs.ReadPaths) != "[docs/a.md docs/b.md]" {
		t.Fatalf("normalized reads = %v, want one entry per path", normalized.inputs.ReadPaths)
	}
}
