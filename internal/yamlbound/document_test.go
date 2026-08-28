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

func TestDecodeMappingSeparatesRawAndExpandedNodeBudgets(t *testing.T) {
	t.Run("raw boundary", func(t *testing.T) {
		atLimit := "items:\n" + strings.Repeat("  - x\n", MaxNodes-4)
		if _, _, err := DecodeMapping([]byte(atLimit), "raw-limit.yml"); err != nil {
			t.Fatalf("raw node boundary was rejected: %v", err)
		}
		overLimit := atLimit + "  - x\n"
		if _, _, err := DecodeMapping([]byte(overLimit), "raw-overflow.yml"); err == nil ||
			!strings.Contains(err.Error(), fmt.Sprintf("yaml nodes actual=%d nodes exceeds maximum=%d nodes", MaxNodes+1, MaxNodes)) {
			t.Fatalf("raw node overflow error = %v", err)
		}
	})

	t.Run("expanded boundary", func(t *testing.T) {
		atLimit := expandedAliasDocument(t, MaxExpandedNodes)
		if _, _, err := DecodeMapping([]byte(atLimit), "expanded-limit.yml"); err != nil {
			t.Fatalf("expanded node boundary was rejected: %v", err)
		}
		overLimit := expandedAliasDocument(t, MaxExpandedNodes+1)
		if _, _, err := DecodeMapping([]byte(overLimit), "expanded-overflow.yml"); err == nil ||
			!strings.Contains(err.Error(), fmt.Sprintf("expanded yaml nodes actual=%d nodes exceeds maximum=%d nodes", MaxExpandedNodes+1, MaxExpandedNodes)) {
			t.Fatalf("expanded node overflow error = %v", err)
		}
	})
}

func TestDecodeMappingAliasGraphs(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name: "shared alias",
			body: "base: &base [x, y]\nleft: *base\nright: *base\n",
		},
		{
			name: "nested alias",
			body: "leaf: &leaf [x]\nbranch: &branch [*leaf]\nroot: [*branch, *branch]\n",
		},
		{
			name:    "recursive alias",
			body:    "root: &root\n  child: *root\n",
			wantErr: "recursive yaml alias",
		},
		{
			name: "alias boundary",
			body: "base: &base x\nitems:\n" + strings.Repeat("  - *base\n", MaxAliases),
		},
		{
			name:    "alias overflow",
			body:    "base: &base x\nitems:\n" + strings.Repeat("  - *base\n", MaxAliases+1),
			wantErr: fmt.Sprintf("yaml aliases actual=%d aliases exceeds maximum=%d aliases", MaxAliases+1, MaxAliases),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := DecodeMapping([]byte(test.body), "aliases.yml")
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("DecodeMapping: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("DecodeMapping error = %v, want %q", err, test.wantErr)
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

func expandedAliasDocument(t *testing.T, expandedNodes int) string {
	t.Helper()
	// Keep gopkg.in/yaml.v3's independent 99% alias-ratio guard below its
	// threshold while driving Reconc's exact expanded-node boundary.
	const targetScalars = 253
	baseNodes := 6 + targetScalars + MaxAliases*(targetScalars+1)
	paddingScalars := expandedNodes - baseNodes - 2
	if paddingScalars < 0 {
		t.Fatalf("expanded node target %d is too small for boundary fixture", expandedNodes)
	}
	return "base: &base\n" +
		strings.Repeat("  - x\n", targetScalars) +
		"padding:\n" + strings.Repeat("  - p\n", paddingScalars) +
		"items:\n" + strings.Repeat("  - *base\n", MaxAliases)
}
