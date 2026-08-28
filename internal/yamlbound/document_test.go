package yamlbound

import (
	"fmt"
	"strings"
	"testing"
)

func TestDecodeMappingContract(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "empty", body: ""},
		{name: "comment only", body: "# comment\n"},
		{name: "mapping", body: "rules: []\n"},
		{name: "explicit null", body: "null\n", wantErr: "explicit null is not an empty mapping"},
		{name: "sequence root", body: "- item\n", wantErr: "expected a YAML mapping"},
		{name: "duplicate document", body: "rules: []\n---\nrules: []\n", wantErr: "must contain exactly one document"},
		{name: "alias budget", body: "base: &base {value: x}\nrules:\n" + strings.Repeat("  - *base\n", MaxAliases+1), wantErr: "yaml aliases"},
		{name: "depth budget", body: deeplyNestedMapping(MaxDepth + 1), wantErr: "yaml nesting depth"},
		{name: "node budget", body: "items:\n" + strings.Repeat("  - x\n", MaxNodes), wantErr: "yaml nodes"},
		{name: "scalar budget", body: "value: " + strings.Repeat("x", MaxScalarBytes+1) + "\n", wantErr: "decoded scalar bytes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, mapping, err := DecodeMapping([]byte(test.body), "contract.yml")
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("DecodeMapping: %v", err)
				}
				if mapping == nil {
					t.Fatal("DecodeMapping returned a nil mapping")
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) || !strings.Contains(err.Error(), "contract.yml") {
				t.Fatalf("DecodeMapping error = %v, want %q with context", err, test.wantErr)
			}
		})
	}
}

func FuzzDecodeMapping(f *testing.F) {
	for _, seed := range []string{
		"rules: []\n",
		"null\n",
		"base: &base {value: x}\nrules: [*base]\n",
		"rules: []\n---\nrules: []\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		if len(body) > 2*MaxScalarBytes {
			t.Skip()
		}
		_, _, _ = DecodeMapping([]byte(body), "fuzz.yml")
	})
}

func deeplyNestedMapping(levels int) string {
	var body strings.Builder
	for level := 0; level < levels; level++ {
		fmt.Fprintf(&body, "%sa:\n", strings.Repeat("  ", level))
	}
	fmt.Fprintf(&body, "%svalue: x\n", strings.Repeat("  ", levels))
	return body.String()
}
