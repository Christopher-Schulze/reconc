package runtime

import (
	"reflect"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestDenyWriteExplanationUsesOnlyDeniedRulePaths(t *testing.T) {
	t.Parallel()
	rule := &policy.Rule{ID: "generated", Kind: policy.KindDenyWrite, Paths: []string{"generated/**"}}
	wantExplanation := "Write activity generated/output.go matched deny_write rule 'generated'."
	wantRecommendation := "Avoid writing paths matching generated/**."
	for _, requiredPaths := range [][]string{nil, {"unreachable/**"}} {
		explanation, recommendation := explainViolation(
			rule.ID, rule.Kind, rule, []string{"generated/output.go"}, nil, requiredPaths, nil, nil,
		)
		if explanation != wantExplanation || recommendation != wantRecommendation {
			t.Fatalf("required_paths=%v produced (%q, %q)", requiredPaths, explanation, recommendation)
		}
	}
}

func TestMapKeysSortedUsesStableLexicalOrder(t *testing.T) {
	t.Parallel()
	if got := mapKeysSorted(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil set result = %#v, want owned empty slice", got)
	}
	want := []string{"alpha", "middle", "zeta"}
	if got := mapKeysSorted(map[string]struct{}{"zeta": {}, "alpha": {}, "middle": {}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("sorted keys = %v, want %v", got, want)
	}
}
