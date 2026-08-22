package compiler

import (
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestDetectConflictsRequireReadSemanticKey(t *testing.T) {
	tests := []struct {
		name string
		a    policy.Rule
		b    policy.Rule
		want bool
	}{
		{
			name: "same obligation ignores order and surrounding whitespace",
			a:    policy.Rule{Paths: []string{"src/**", "docs/**"}, BeforePaths: []string{"README.md", "docs/**"}},
			b:    policy.Rule{Paths: []string{" docs/** ", "src/**"}, BeforePaths: []string{"docs/**", " README.md "}},
			want: true,
		},
		{
			name: "same trigger but different governed paths",
			a:    policy.Rule{Paths: []string{"src/**"}, BeforePaths: []string{"README.md"}, WhenPaths: []string{"shared/**"}},
			b:    policy.Rule{Paths: []string{"docs/**"}, BeforePaths: []string{"README.md"}, WhenPaths: []string{"shared/**"}},
		},
		{
			name: "same governed paths but different prerequisite reads",
			a:    policy.Rule{Paths: []string{"src/**"}, BeforePaths: []string{"README.md"}},
			b:    policy.Rule{Paths: []string{"src/**"}, BeforePaths: []string{"CONTRIBUTING.md"}},
		},
		{
			name: "absent semantic fields",
			a:    policy.Rule{},
			b:    policy.Rule{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.a.ID, test.a.Kind = "a", policy.KindRequireRead
			test.b.ID, test.b.Kind = "b", policy.KindRequireRead
			conflicts := DetectConflicts([]policy.Rule{test.a, test.b})
			got := len(conflicts) == 1 && conflicts[0].Kind == ConflictDuplicateRequireRead
			if got != test.want {
				t.Fatalf("duplicate require_read = %v, want %v; conflicts=%+v", got, test.want, conflicts)
			}
		})
	}
}
