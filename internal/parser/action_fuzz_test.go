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
		"actions:\n  rules:\n    - id: number\n      decision: block\n      when:\n        predicate: {source: context, pointer: /value, op: eq, value: 01}\n",
		"actions:\n  defaults:\n    cache: never\n    cache: always\n",
		"\v&0",
		"\xe80 ",
		"\f0",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		bundle := &ingest.SourceBundle{Sources: []policy.PolicySource{{
			Kind: policy.SourceCompilerConfig, Path: ".reconc.yml", Content: body,
		}}}
		gotPolicy, gotErr := parseRuleDocumentsWithDecoder(bundle, decodeRuleSourceDocumentBounded)
		wantPolicy, wantErr := parseRuleDocumentsWithDecoder(bundle, legacyTwoPassSourceDocumentDecoder)
		assertParserParity(t, gotPolicy, gotErr, wantPolicy, wantErr)
	})
}
