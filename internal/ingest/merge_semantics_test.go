package ingest

import (
	"strings"
	"testing"
)

func TestDecodeYAMLMappingRejectsMergeSemantics(t *testing.T) {
	body := "base: &base {mode: block}\nrule:\n  <<: *base\n"
	if _, err := decodeYAMLMapping(body, "policy.yml"); err == nil || !strings.Contains(err.Error(), "YAML merge keys are not supported") {
		t.Fatalf("decodeYAMLMapping merge error = %v", err)
	}
}
