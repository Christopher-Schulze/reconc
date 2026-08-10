package parser

import (
	"testing"

	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
)

func FuzzParseActionPolicy(f *testing.F) {
	for _, seed := range []string{
		"actions: {}\n",
		"actions:\n  tools: []\n  rules: []\n",
		"actions:\n  rules:\n    - id: block\n      decision: block\n",
		"actions:\n  detectors: []\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		bundle := &ingest.SourceBundle{Sources: []policy.PolicySource{{
			Kind: policy.SourceCompilerConfig, Path: ".reconc.yml", Content: body,
		}}}
		_, _ = ParseRuleDocuments(bundle)
	})
}
