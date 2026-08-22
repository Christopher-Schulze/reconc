package schema

import (
	"strings"
	"testing"
)

func TestValidatePolicyConfigYAMLUsesShippedSchemaOffline(t *testing.T) {
	valid := []byte("default_mode: warn\nrules:\n  - id: test\n    kind: deny_write\n    paths: ['dist/**']\n    mode: warn\n    message: test\n")
	if err := ValidatePolicyConfigYAML(valid); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{name: "unknown", body: "unknown: true\n", want: "schema"},
		{name: "wrong type", body: "rules: nope\n", want: "schema"},
		{name: "multiple documents", body: "rules: []\n---\nrules: []\n", want: "exactly one"},
		{name: "empty", body: "# comment\n", want: "object"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidatePolicyConfigYAML([]byte(test.body))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %v, want %q", err, test.want)
			}
		})
	}
	aliases := "base: &base\n  value: x\nrules:\n" + strings.Repeat("  - *base\n", 1025)
	if err := ValidatePolicyConfigYAML([]byte(aliases)); err == nil || !strings.Contains(err.Error(), "yaml aliases") {
		t.Fatalf("alias amplification error = %v", err)
	}
}
