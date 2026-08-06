package parser

import (
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func parseCacheInputPolicy(t *testing.T, body string) (*ParsedPolicy, error) {
	t.Helper()
	return ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: "policy.yml", Content: body,
	}))
}

// TestCacheInputsAcceptsLiteralRepoRelativePaths covers the shape Stop report
// reuse can bind, on the rule and on the composite check.
func TestCacheInputsAcceptsLiteralRepoRelativePaths(t *testing.T) {
	parsed, err := parseCacheInputPolicy(t, `rules:
  - id: gate
    kind: require_script
    when_paths: ['src/**']
    script: 'scripts/check.sh'
    cache_inputs: ['build/report.json', 'STATUS.md']
    message: gate
  - id: composite
    kind: all_of
    when_paths: ['src/**']
    checks:
      - kind: require_script
        script: 'scripts/inner.sh'
        cache_inputs: ['build/inner.json']
    message: composite
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := parsed.Rules[0].CacheInputs; len(got) != 2 || got[0] != "build/report.json" || got[1] != "STATUS.md" {
		t.Fatalf("rule cache_inputs = %v", got)
	}
	if got := parsed.Rules[1].Checks[0].CacheInputs; len(got) != 1 || got[0] != "build/inner.json" {
		t.Fatalf("check cache_inputs = %v", got)
	}
}

// TestCacheInputsRejectsUnbindableShapes bounds the field: anything the Stop
// fingerprint cannot bind exactly must fail at compile time, not silently
// degrade at Stop time.
func TestCacheInputsRejectsUnbindableShapes(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{name: "glob", value: "['build/**/*.json']", want: "literal files"},
		{name: "template variable", value: "['reports/{capture}.json']", want: "literal files"},
		{name: "absolute", value: "['/etc/passwd']", want: "repo-relative"},
		{name: "parent escape", value: "['../outside.json']", want: "repo-relative"},
		{name: "duplicate", value: "['build/a.json', 'build/a.json']", want: "more than once"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseCacheInputPolicy(t, `rules:
  - id: gate
    kind: require_script
    when_paths: ['src/**']
    script: 'scripts/check.sh'
    cache_inputs: `+tc.value+`
    message: gate
`)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

// TestCacheInputsRejectedOnOtherKinds keeps the field where it means something.
func TestCacheInputsRejectedOnOtherKinds(t *testing.T) {
	_, err := parseCacheInputPolicy(t, `rules:
  - id: claim
    kind: require_claim
    when_paths: ['src/**']
    claims: ['done']
    cache_inputs: ['build/report.json']
    message: claim
`)
	if err == nil || !strings.Contains(err.Error(), "only valid for kind require_script") {
		t.Fatalf("error = %v, want a kind rejection", err)
	}
}
