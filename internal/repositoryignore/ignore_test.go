package repositoryignore

import (
	"strings"
	"testing"
)

func TestMergePreservesUserContentAndIsIdempotent(t *testing.T) {
	initial := "vendor/\n"
	first, err := Merge(initial)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Merge(first)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("managed ignore merge is not idempotent:\nfirst=%q\nsecond=%q", first, second)
	}
	if !strings.HasPrefix(first, initial) || strings.Count(first, BlockStart) != 1 || strings.Count(first, BlockEnd) != 1 {
		t.Fatalf("managed ignore block did not preserve user content: %q", first)
	}
}

func TestMergeRejectsMalformedMarkers(t *testing.T) {
	for _, input := range []string{
		BlockStart + "\n",
		BlockEnd + "\n",
		Block() + Block(),
	} {
		if _, err := Merge(input); err == nil {
			t.Fatalf("expected malformed markers to fail closed: %q", input)
		}
	}
}

func TestBodyIgnoresBootstrapAndRemovalCandidates(t *testing.T) {
	for _, pattern := range []string{"*.reconc-candidate-*", "*.reconc-remove-candidate-*"} {
		if !strings.Contains(Body(), pattern) {
			t.Fatalf("managed ignore body is missing %q", pattern)
		}
	}
}
