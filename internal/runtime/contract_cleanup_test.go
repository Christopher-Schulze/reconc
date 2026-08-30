package runtime

import (
	"reflect"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestDenyWriteExplanationUsesOnlyDeniedRulePaths(t *testing.T) {
	t.Parallel()
	rule := &policy.Rule{ID: "generated", Kind: policy.KindDenyWrite, Paths: []string{"generated/**"}}
	wantExplanation := `Write activity generated/output.go matched deny_write rule "generated".`
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

func TestDiagnosticQuotingEscapesUntrustedValues(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		value string
		want  string
	}{
		{name: "plain", value: "value", want: `"value"`},
		{name: "single quote", value: "rule'; run", want: `"rule'; run"`},
		{name: "double quote", value: `rule"tail`, want: `"rule\"tail"`},
		{name: "control character", value: "rule\nnext", want: `"rule\nnext"`},
		{name: "unicode", value: "rüle", want: `"rüle"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := quote(test.value); got != test.want {
				t.Fatalf("quote(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}

func TestViolationExplanationEscapesRuleIdentity(t *testing.T) {
	t.Parallel()
	rule := &policy.Rule{ID: "generated'\"\nnext", Kind: policy.KindDenyWrite, Paths: []string{"generated/**"}}
	explanation, _ := explainViolation(
		rule.ID, rule.Kind, rule, []string{"generated/output.go"}, nil, nil, nil, nil,
	)
	want := `Write activity generated/output.go matched deny_write rule "generated'\"\nnext".`
	if explanation != want {
		t.Fatalf("explanation = %q, want %q", explanation, want)
	}
}

func TestSortedKeysUsesStableLexicalOrder(t *testing.T) {
	t.Parallel()
	if got := sortedKeys(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil set result = %#v, want owned empty slice", got)
	}
	want := []string{"alpha", "middle", "zeta"}
	if got := sortedKeys(map[string]struct{}{"zeta": {}, "alpha": {}, "middle": {}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("sorted keys = %v, want %v", got, want)
	}
}
