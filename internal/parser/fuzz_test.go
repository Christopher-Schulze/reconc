package parser

import (
	"strings"
	"testing"

	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
)

func FuzzParseRuleDocumentsNoPanic(f *testing.F) {
	for _, seed := range []string{
		"rules: []\n",
		"rules:\n  - id: deny\n    kind: deny_write\n    paths: ['generated/**']\n    message: no generated writes\n",
		"rules:\n  - id: claim\n    kind: require_claim\n    when_paths: ['src/**']\n    claims: ['ci-green']\n    message: ci required\n",
		"rules:\n  - id: bad\n    kind: nope\n    message: bad\n",
		"rules: [",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, content string) {
		bundle := &ingest.SourceBundle{
			RepoRoot: "/tmp/reconc-fuzz",
			Sources: []policy.PolicySource{
				{
					Kind:    policy.SourcePolicyFile,
					Path:    "policies/fuzz.yml",
					Content: content,
				},
			},
		}
		parsed, err := ParseRuleDocuments(bundle)
		if err != nil {
			return
		}
		seen := map[string]struct{}{}
		for _, rule := range parsed.Rules {
			if strings.TrimSpace(rule.ID) == "" {
				t.Fatalf("parser accepted empty rule id")
			}
			if _, ok := seen[rule.ID]; ok {
				t.Fatalf("parser accepted duplicate rule id %q", rule.ID)
			}
			seen[rule.ID] = struct{}{}
			if !rule.Kind.Valid() {
				t.Fatalf("parser accepted invalid kind %q", rule.Kind)
			}
			if rule.Mode != "" && !rule.Mode.Valid() {
				t.Fatalf("parser accepted invalid mode %q", rule.Mode)
			}
		}
	})
}
