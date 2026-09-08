package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/presets"
)

func TestTemplateDependencyResolutionMatchesExpansion(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"direct", "rules:\n  - id: generated\n    template: no-generated-writes\n"},
		{"quoted key", "rules:\n  - id: generated\n    \"tem\\u0070late\": no-generated-writes\n"},
		{"scoped", "scopes:\n  - id: web\n    paths: ['apps/web/**']\n    rules:\n      - id: generated\n        template: no-generated-writes\n"},
		{"alias", "rules:\n  - id: first\n    template: &selected no-generated-writes\n  - id: second\n    template: *selected\n"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(presets.HomeEnvVar, t.TempDir())
			bundle := &ingest.SourceBundle{Sources: []policy.PolicySource{{Kind: policy.SourcePolicyFile, Path: "rules.yml", Content: test.content}}}
			parsed, err := ParseRuleDocuments(bundle)
			if err != nil {
				t.Fatal(err)
			}
			dependencies, err := ResolveTemplateDependencies(bundle)
			if err != nil {
				t.Fatal(err)
			}
			if len(dependencies) != 1 || !reflect.DeepEqual(dependencies, parsed.TemplateDependencies) {
				t.Fatalf("dependencies = %+v, expansion = %+v", dependencies, parsed.TemplateDependencies)
			}
			candidate, err := ingest.ReplacePolicyFileSource(bundle, "rules.yml", "rules: []\n")
			if err != nil {
				t.Fatal(err)
			}
			candidateDependencies, err := ResolveTemplateDependencies(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if len(candidateDependencies) != 0 {
				t.Fatalf("candidate retained removed dependencies: %+v", candidateDependencies)
			}
			original, err := ResolveTemplateDependencies(bundle)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(original, dependencies) {
				t.Fatal("candidate mutated original dependencies")
			}
		})
	}
}

func TestTemplateSnapshotBoundsAggregateInput(t *testing.T) {
	home := t.TempDir()
	t.Setenv(presets.HomeEnvVar, home)
	directory := filepath.Join(home, "templates")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	const prefix = "kind: deny_write\nmode: block\npaths: ['generated/**']\nmessage: protected\n#"
	content := []byte(prefix + strings.Repeat("x", (8<<20)-len(prefix)))
	var rules strings.Builder
	rules.WriteString("rules:\n")
	for index := range 9 {
		name := fmt.Sprintf("bounded-%d", index)
		if err := os.WriteFile(filepath.Join(directory, name+".yml"), content, 0o600); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&rules, "  - id: rule-%d\n    template: %s\n", index, name)
	}
	bundle := &ingest.SourceBundle{Sources: []policy.PolicySource{{Kind: policy.SourcePolicyFile, Path: "rules.yml", Content: rules.String()}}}
	if _, err := ResolveTemplateDependencies(bundle); err == nil || !strings.Contains(err.Error(), "input bytes") {
		t.Fatalf("dependency aggregate bound: %v", err)
	}
	if _, err := ParseRuleDocuments(bundle); err == nil || !strings.Contains(err.Error(), "input bytes") {
		t.Fatalf("expansion aggregate bound: %v", err)
	}
}
