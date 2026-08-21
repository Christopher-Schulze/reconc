package ingest

import (
	"reflect"
	"sort"
	"testing"
)

func TestSourceBundleOwnsValidatedPolicyIncludePatterns(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "AGENTS.md", "# project\n")
	writeFile(t, repo, ".reconc.yml", "include:\n  - extras/*.yml\n  - policies/*.yml\n")
	writeFile(t, repo, "policies/rules.yml", "rules: []\n")
	writeFile(t, repo, "extras/extra.yml", "rules: []\n")

	bundle, err := LoadPolicySources(repo)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]string(nil), DefaultPolicyGlobs...)
	want = append(want, "extras/*.yml")
	sort.Strings(want)
	if got := bundle.PolicyIncludePatterns(); !reflect.DeepEqual(got, want) {
		t.Fatalf("include recipe = %v, want %v", got, want)
	}

	first := bundle.PolicyIncludePatterns()
	first[0] = "mutated/*.yml"
	if got := bundle.PolicyIncludePatterns(); !reflect.DeepEqual(got, want) {
		t.Fatalf("caller mutated bundle recipe: %v", got)
	}
}
