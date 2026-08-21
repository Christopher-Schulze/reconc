package parser

import (
	"fmt"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
)

func TestParserRuleResourceLimits(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "rule count",
			content: "rules:\n" + strings.Repeat("  - id: r\n    kind: deny_write\n    paths: ['x']\n    message: m\n", maxParserRules+1),
			want:    "rules actual=4097 rules exceeds maximum=4096 rules",
		},
		{
			name:    "message bytes",
			content: "rules:\n  - id: message\n    kind: deny_write\n    paths: ['x']\n    message: '" + strings.Repeat("m", maxParserMessageBytes+1) + "'\n",
			want:    "field message string bytes actual=65537 bytes exceeds maximum=65536 bytes",
		},
		{
			name:    "pattern bytes",
			content: "rules:\n  - id: pattern\n    kind: deny_write\n    paths: ['" + strings.Repeat("x", maxParserPatternBytes+1) + "']\n    message: m\n",
			want:    "field paths[0] string bytes actual=1025 bytes exceeds maximum=1024 bytes",
		},
		{
			name:    "list items",
			content: "rules:\n  - id: list\n    kind: deny_write\n    paths: [" + strings.TrimSuffix(strings.Repeat("'x', ", maxParserListItems+1), ", ") + "]\n    message: m\n",
			want:    "field paths items actual=257 items exceeds maximum=256 items",
		},
		{
			name:    "command bytes",
			content: "rules:\n  - id: command\n    kind: require_command\n    when_paths: ['x']\n    commands: ['" + strings.Repeat("c", maxParserCommandBytes+1) + "']\n    message: m\n",
			want:    "field commands[0] string bytes actual=16385 bytes exceeds maximum=16384 bytes",
		},
		{
			name:    "check count",
			content: "rules:\n  - id: checks\n    kind: all_of\n    when_paths: ['x']\n    checks:\n" + strings.Repeat("      - kind: deny_write\n        paths: ['x']\n", maxParserChecksPerRule+1) + "    message: m\n",
			want:    "field checks items actual=257 items exceeds maximum=256 items",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseRuleDocuments(&ingest.SourceBundle{Sources: []policy.PolicySource{{
				Kind: policy.SourcePolicyFile, Path: "policies/limits.yml", BlockID: "block-1", Content: test.content,
			}}})
			if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), "policies/limits.yml") {
				t.Fatalf("limit error = %v, want %q with source path", err, test.want)
			}
		})
	}
}

func TestParserResourceLimitBoundaries(t *testing.T) {
	for _, length := range []int{maxParserMessageBytes - 1, maxParserMessageBytes} {
		content := fmt.Sprintf("rules:\n  - id: boundary\n    kind: deny_write\n    paths: ['x']\n    message: '%s'\n", strings.Repeat("m", length))
		if _, err := ParseRuleDocuments(&ingest.SourceBundle{Sources: []policy.PolicySource{{Kind: policy.SourcePolicyFile, Path: "boundary.yml", Content: content}}}); err != nil {
			t.Fatalf("message length %d should be accepted: %v", length, err)
		}
	}
}

func TestParserRejectsYAMLAmplificationAndDuplicateKeys(t *testing.T) {
	aliases := "base: &base\n  value: x\nrules:\n" + strings.Repeat("  - *base\n", maxParserYAMLAliases+1)
	if _, err := decodeYAMLMappingBounded(aliases, "aliases.yml"); err == nil || !strings.Contains(err.Error(), "yaml aliases") {
		t.Fatalf("alias amplification was accepted: %v", err)
	}
	duplicate := "rules:\n  - id: one\n    id: two\n    kind: deny_write\n    paths: ['x']\n    message: m\n"
	if _, err := decodeYAMLMappingBounded(duplicate, "duplicate.yml"); err == nil || !strings.Contains(err.Error(), "mapping key") {
		t.Fatalf("duplicate YAML key was accepted: %v", err)
	}
}

func FuzzDecodeYAMLMappingBounded(f *testing.F) {
	for _, seed := range []string{
		"rules:\n  - id: one\n    kind: deny_write\n    paths: ['x']\n    message: ok\n",
		"base: &base\n  value: x\nrules: [*base]\n",
		"rules:\n  - id: one\n    id: two\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 2*maxParserScalarBytes {
			t.Skip()
		}
		_, _ = decodeYAMLMappingBounded(raw, "fuzz.yml")
	})
}
