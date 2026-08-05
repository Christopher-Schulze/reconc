package parser

import (
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestParseRejectsEscapingPolicyControlledFiles(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "top-level required file traversal",
			content: "rules:\n  - id: gate\n    kind: require_fresh_file\n    when_paths: ['src/**']\n    required_files:\n      - path: '../outside'\n    mode: block\n    message: gate\n",
		},
		{
			name:    "top-level evidence absolute",
			content: "rules:\n  - id: gate\n    kind: require_evidence\n    when_paths: ['src/**']\n    evidence:\n      - file: '/etc/passwd'\n        must_exist: true\n    mode: block\n    message: gate\n",
		},
		{
			name:    "composite required file traversal",
			content: "rules:\n  - id: gate\n    kind: all_of\n    when_paths: ['src/**']\n    checks:\n      - kind: require_fresh_file\n        path: 'proof/../../outside'\n    mode: block\n    message: gate\n",
		},
		{
			name:    "composite evidence absolute",
			content: "rules:\n  - id: gate\n    kind: all_of\n    when_paths: ['src/**']\n    checks:\n      - kind: require_evidence\n        file: 'C:/outside'\n        must_exist: true\n    mode: block\n    message: gate\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseRuleDocuments(makeBundle(policy.PolicySource{Kind: policy.SourcePolicyFile, Path: "policies/test.yml", Content: test.content}))
			if err == nil || !strings.Contains(err.Error(), "repo-relative path") {
				t.Fatalf("escaping policy path error = %v", err)
			}
		})
	}
}

func TestParseAcceptsSafeTemplatedPolicyControlledFile(t *testing.T) {
	content := "rules:\n  - id: gate\n    kind: require_evidence\n    when_paths: ['src/{module}/**']\n    evidence:\n      - file: 'proof/{module}/report.json'\n        must_exist: true\n    mode: block\n    message: gate\n"
	if _, err := ParseRuleDocuments(makeBundle(policy.PolicySource{Kind: policy.SourcePolicyFile, Path: "policies/test.yml", Content: content})); err != nil {
		t.Fatalf("safe templated path was rejected: %v", err)
	}
}
