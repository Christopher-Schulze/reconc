package parser

import (
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestDefaultModeOwnedByCompilerConfiguration(t *testing.T) {
	for _, mode := range []policy.Mode{policy.ModeObserve, policy.ModeWarn, policy.ModeBlock, policy.ModeFix} {
		parsed, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
			Kind: policy.SourceCompilerConfig, Path: ".reconc.yml",
			Content: "default_mode: " + string(mode) + "\nrules: []\n",
		}))
		if err != nil {
			t.Fatalf("compiler default mode %q rejected: %v", mode, err)
		}
		if parsed.DefaultMode != mode {
			t.Fatalf("default mode = %q, want %q", parsed.DefaultMode, mode)
		}
	}

	for _, source := range []policy.PolicySource{
		{Kind: policy.SourceGlobal},
		{Kind: policy.SourceInlineBlock},
		{Kind: policy.SourcePreset},
		{Kind: policy.SourcePolicyFile},
		{Kind: policy.SourcePolicyFile, BlockID: policy.ImpactCandidateBlockPrefix + "candidate"},
	} {
		source.Path = string(source.Kind) + ".yml"
		source.Content = "default_mode: block\nrules: []\n"
		_, err := ParseRuleDocuments(makeBundle(source))
		if err == nil || !strings.Contains(err.Error(), "only valid in compiler configuration") {
			t.Fatalf("source %s block %q must reject misplaced default_mode explicitly, got %v", source.Kind, source.BlockID, err)
		}
	}
}

func TestPolicyStringListsShareTrimmedNormalization(t *testing.T) {
	parsed, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind: policy.SourcePolicyFile,
		Path: "policies/lists.yml",
		Content: `rules:
  - id: command
    kind: require_command
    commands: ["  go test ./...  "]
    when_paths: ["  **/*.go  "]
    message: command
  - id: claim
    kind: require_claim
    claims: ["  ci-green  "]
    when_paths: ["  src/**  "]
    message: claim
  - id: read
    kind: require_read
    paths: ["  docs/**  "]
    before_paths: ["  README.md  "]
    message: read
  - id: script
    kind: require_script
    script: scripts/check.sh
    args: ["  --strict  "]
    cache_inputs: ["  go.mod  "]
    when_paths: ["  **/*.go  "]
    message: script
  - id: evidence
    kind: require_evidence
    evidence:
      - file: proof.md
        must_contain: ["  PASS  "]
    when_paths: ["  src/**  "]
    message: evidence
`,
	}))
	if err != nil {
		t.Fatalf("parse normalized lists: %v", err)
	}
	if len(parsed.Rules) != 5 {
		t.Fatalf("parsed rules = %d, want 5", len(parsed.Rules))
	}
	want := map[string][]string{
		"command":  {"go test ./...", "**/*.go"},
		"claim":    {"ci-green", "src/**"},
		"read":     {"docs/**", "README.md"},
		"script":   {"--strict", "go.mod", "**/*.go"},
		"evidence": {"PASS", "src/**"},
	}
	for _, rule := range parsed.Rules {
		expected, known := want[rule.ID]
		if !known {
			t.Fatalf("unexpected parsed rule %q", rule.ID)
		}
		var got []string
		switch rule.ID {
		case "command":
			got = []string{rule.Commands[0], rule.WhenPaths[0]}
		case "claim":
			got = []string{rule.Claims[0], rule.WhenPaths[0]}
		case "read":
			got = []string{rule.Paths[0], rule.BeforePaths[0]}
		case "script":
			got = []string{rule.Args[0], rule.CacheInputs[0], rule.WhenPaths[0]}
		case "evidence":
			got = []string{rule.Evidence[0].MustContain[0], rule.WhenPaths[0]}
		}
		if len(got) != len(expected) {
			t.Fatalf("rule %s normalized list length = %d, want %d", rule.ID, len(got), len(expected))
		}
		for index := range got {
			if got[index] != expected[index] {
				t.Fatalf("rule %s list[%d] = %q, want %q", rule.ID, index, got[index], expected[index])
			}
		}
	}
}

func TestPolicyStringListsRejectWhitespaceOnlyEntries(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"paths", "kind: deny_write\n    paths: ['   ']"},
		{"commands", "kind: require_command\n    commands: ['   ']\n    when_paths: ['src/**']"},
		{"claims", "kind: require_claim\n    claims: ['   ']\n    when_paths: ['src/**']"},
		{"args", "kind: require_script\n    script: scripts/check.sh\n    args: ['   ']\n    when_paths: ['src/**']"},
		{"cache inputs", "kind: require_script\n    script: scripts/check.sh\n    cache_inputs: ['   ']\n    when_paths: ['src/**']"},
		{"evidence content", "kind: require_evidence\n    evidence:\n      - file: proof.md\n        must_contain: ['   ']\n    when_paths: ['src/**']"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content := "rules:\n  - id: bad\n    " + test.body + "\n    message: bad\n"
			_, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
				Kind: policy.SourcePolicyFile, Path: "policies/bad.yml", Content: content,
			}))
			if err == nil || !strings.Contains(err.Error(), "non-empty") {
				t.Fatalf("whitespace-only list entry error = %v", err)
			}
		})
	}
}
