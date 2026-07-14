package parser

import (
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestParseNativeHygieneAssurance(t *testing.T) {
	parsed, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: "policy.yml", Content: `rules:
  - id: hygiene
    kind: require_assurance
    mode: warn
    when_paths: ["**"]
    message: native hygiene
    assurance:
      - id: format
        type: go_format
        scan_paths: ["**/*.go"]
        exclude_paths: ["vendor/**"]
      - id: shipped-source
        type: source_hygiene
        scan_paths: ["src/**"]
        exemptions:
          - path: src/legacy/**
            reason: frozen legacy surface
`,
	}))
	if err != nil {
		t.Fatalf("ParseRuleDocuments: %v", err)
	}
	gates := parsed.Rules[0].Assurance
	if len(gates) != 2 || gates[0].Type != policy.AssuranceGoFormat || gates[1].Type != policy.AssuranceSourceHygiene {
		t.Fatalf("unexpected native hygiene gates: %+v", gates)
	}
}

func TestParseNativeHygieneRequiresScanPaths(t *testing.T) {
	_, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: "policy.yml", Content: `rules:
  - id: hygiene
    kind: require_assurance
    mode: block
    when_paths: ["**"]
    message: native hygiene
    assurance:
      - id: shipped-source
        type: source_hygiene
`,
	}))
	if err == nil || !strings.Contains(err.Error(), "source_hygiene requires scan_paths") {
		t.Fatalf("expected missing scan_paths rejection, got %v", err)
	}
}
