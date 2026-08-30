package templates

import (
	"strings"
	"testing"
)

func TestParseTemplateBytesRejectsMergeSemantics(t *testing.T) {
	body := []byte("base: &base {kind: deny_write, mode: block, message: blocked}\n<<: *base\n")
	if _, _, err := parseTemplateBytes(body, "merge.yml"); err == nil || !strings.Contains(err.Error(), "YAML merge keys are not supported") {
		t.Fatalf("parseTemplateBytes merge error = %v", err)
	}
}
