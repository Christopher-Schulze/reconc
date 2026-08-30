package presets

import (
	"strings"
	"testing"
)

func TestParseManifestRejectsMergeSemantics(t *testing.T) {
	body := "base: &base\n" +
		"  format_version: \"1\"\n" +
		"  name: merge\n" +
		"  summary: merged\n" +
		"  stacks: [go]\n" +
		"  capabilities:\n" +
		"    - id: test\n" +
		"      inputs: [source]\n" +
		"      evidence: [test]\n" +
		"      rules: [rule]\n" +
		"pack:\n" +
		"  <<: *base\n" +
		"rules:\n" +
		"  - id: rule\n"
	if _, err := parseManifest("merge", body); err == nil || !strings.Contains(err.Error(), "YAML merge keys are not supported") {
		t.Fatalf("parseManifest merge error = %v", err)
	}
}
