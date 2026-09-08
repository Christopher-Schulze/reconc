package parser

import (
	"strings"
	"testing"

	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
)

func TestCompositeWritePhaseAuthoring(t *testing.T) {
	const deny = "      - kind: deny_write\n        paths: ['protected/**']\n"
	checks := []struct{ name, body string }{
		{"claim", "      - kind: require_claim\n        claims: ['approved']\n"},
		{"command", "      - kind: require_command\n        commands: ['go test ./...']\n"},
		{"successful command", "      - kind: require_command_success\n        commands: ['go test ./...']\n"},
		{"forbidden command", "      - kind: forbid_command\n        commands: ['rm -rf']\n"},
		{"file", "      - kind: require_fresh_file\n        path: proof.txt\n"},
		{"evidence", "      - kind: require_evidence\n        file: proof.txt\n        must_exist: true\n"},
		{"script", "      - kind: require_script\n        script: scripts/check.sh\n"},
	}
	for _, check := range checks {
		for _, kind := range []string{"all_of", "any_of"} {
			t.Run(kind+"/"+check.name, func(t *testing.T) {
				content := "rules:\n  - id: boundary\n    kind: " + kind + "\n    when_paths: ['**']\n    mode: block\n    message: boundary\n    checks:\n" + check.body + deny
				bundle := &ingest.SourceBundle{Sources: []policy.PolicySource{{Kind: policy.SourcePolicyFile, Path: "rules.yml", Content: content}}}
				_, err := ParseRuleDocuments(bundle)
				if kind == "any_of" {
					if err == nil || !strings.Contains(err.Error(), "across enforcement phases") {
						t.Fatalf("mixed disjunction admitted: %v", err)
					}
				} else if err != nil {
					t.Fatalf("mixed conjunction rejected: %v", err)
				}
			})
		}
	}
}
