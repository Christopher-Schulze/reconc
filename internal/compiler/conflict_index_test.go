package compiler

import (
	"encoding/json"
	"reflect"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestConflictIndexesPreserveRuleAndResultOwnership(t *testing.T) {
	for _, rules := range [][]policy.Rule{
		{
			{ID: "b", Kind: policy.KindDenyWrite, Paths: []string{"z/**", "a/**"}, ExcludePaths: []string{"z/example", "a/example"}},
			{ID: "a", Kind: policy.KindDenyWrite, Paths: []string{"a/**", "z/**"}, ExcludePaths: []string{"a/example", "z/example"}},
		}, {
			{ID: "b", Kind: policy.KindRequireRead, Paths: []string{"z/**", "a/**"}, BeforePaths: []string{"b.md", "a.md"}},
			{ID: "a", Kind: policy.KindRequireRead, Paths: []string{"a/**", "z/**"}, BeforePaths: []string{"a.md", "b.md"}},
		}, {
			{ID: "b", Kind: policy.KindRequireCommand, Commands: []string{"go vet ./...", "go test ./..."}},
			{ID: "a", Kind: policy.KindRequireCommand, Commands: []string{"go test ./...", "go vet ./..."}},
		}, {
			{ID: "b", Kind: policy.KindRequireClaim, Claims: []string{"z", "a"}},
			{ID: "a", Kind: policy.KindRequireClaim, Claims: []string{"a", "z"}},
		},
	} {
		t.Run(string(rules[0].Kind), func(t *testing.T) {
			before, err := json.Marshal(rules)
			if err != nil {
				t.Fatal(err)
			}
			want := DetectConflicts(rules)
			got := DetectConflicts(rules)
			if len(got) != 1 || len(got[0].Paths) != 2 || got[0].RuleIDA != "a" || got[0].RuleIDB != "b" {
				t.Fatalf("conflicts = %+v", got)
			}
			got[0].Paths[0] = "mutated-result"
			if again := DetectConflicts(rules); !reflect.DeepEqual(again, want) {
				t.Fatalf("result mutation changed later analysis: got %+v, want %+v", again, want)
			}
			after, err := json.Marshal(rules)
			if err != nil || string(after) != string(before) {
				t.Fatalf("analysis or result mutation changed input rules: %s -> %s, err=%v", before, after, err)
			}
		})
	}
}
