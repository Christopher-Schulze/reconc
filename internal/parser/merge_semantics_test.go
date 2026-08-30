package parser

import (
	"strings"
	"testing"

	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
)

func TestParseRuleDocumentsRejectsYAMLMergeSemantics(t *testing.T) {
	bundle := &ingest.SourceBundle{Sources: []policy.PolicySource{{
		Kind: policy.SourcePolicyFile,
		Path: "policies/merge.yml",
		Content: "rules:\n" +
			"  - &base\n" +
			"    id: first\n" +
			"    kind: deny_write\n" +
			"    paths: [generated/**]\n" +
			"    mode: block\n" +
			"    message: base\n" +
			"  - <<: *base\n" +
			"    id: second\n",
	}}}
	if _, err := ParseRuleDocuments(bundle); err == nil || !strings.Contains(err.Error(), "YAML merge keys are not supported") {
		t.Fatalf("ParseRuleDocuments merge error = %v", err)
	}
}
