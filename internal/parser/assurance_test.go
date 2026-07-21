package parser

import (
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestParseRequireAssuranceAppliesTypedDefaults(t *testing.T) {
	parsed, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: "policy.yml", Content: `rules:
  - id: assurance
    kind: require_assurance
    mode: block
    when_paths: ["**"]
    message: native gates
    assurance:
      - id: process
        type: process_boundary
        scan_paths: ["**/*.go"]
        site_patterns: ["exec.Command("]
        guard_markers: ["ApplyHardening"]
      - id: proof
        type: substantive_proof
        proof_file: .reconc/proofs.json
`,
	}))
	if err != nil {
		t.Fatalf("ParseRuleDocuments: %v", err)
	}
	gates := parsed.Rules[0].Assurance
	if len(gates) != 2 {
		t.Fatalf("expected 2 gates, got %d", len(gates))
	}
	if gates[0].MarkerWindowLines != 20 {
		t.Errorf("expected marker window default 20, got %d", gates[0].MarkerWindowLines)
	}
	if gates[1].MinSamples != 3 || gates[1].MaxAgeHours != 24 {
		t.Errorf("unexpected proof defaults: %+v", gates[1])
	}
}

func TestParseRequireAssuranceRejectsIrrelevantFieldByType(t *testing.T) {
	_, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: "policy.yml", Content: `rules:
  - id: assurance
    kind: require_assurance
    mode: block
    when_paths: ["**"]
    message: native gates
    assurance:
      - id: layout
        type: repository_layout
        allowed_root_entries: ["src"]
        commands: ["fake pass"]
`,
	}))
	if err == nil || !strings.Contains(err.Error(), `field "commands" is not valid for type repository_layout`) {
		t.Fatalf("expected irrelevant field rejection, got %v", err)
	}
}

func TestParseRequireAssuranceRejectsUnreasonedExemption(t *testing.T) {
	_, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: "policy.yml", Content: `rules:
  - id: assurance
    kind: require_assurance
    mode: block
    when_paths: ["**"]
    message: native gates
    assurance:
      - id: network
        type: network_boundary
        scan_paths: ["**/*.go"]
        site_patterns: ["http.Get("]
        guard_markers: ["GuardedClient"]
        exemptions:
          - path: legacy/**
            reason: ""
`,
	}))
	if err == nil || !strings.Contains(err.Error(), "non-empty reason") {
		t.Fatalf("expected exemption rationale rejection, got %v", err)
	}
}

func TestParseRequireAssuranceRejectsEscapingManifestPath(t *testing.T) {
	_, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: "policy.yml", Content: `rules:
  - id: assurance
    kind: require_assurance
    mode: block
    when_paths: ["**"]
    message: native gates
    assurance:
      - id: pins
        type: dependency_pins
        manifest_paths: ["../package.json"]
`,
	}))
	if err == nil || !strings.Contains(err.Error(), "repo-relative") {
		t.Fatalf("expected escaping manifest path rejection, got %v", err)
	}
}

func TestParseRequireAssuranceRejectsEmptyVersionPrefix(t *testing.T) {
	_, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: "policy.yml", Content: `rules:
  - id: assurance
    kind: require_assurance
    mode: block
    when_paths: ["**"]
    message: native gates
    assurance:
      - id: pins
        type: dependency_pins
        manifest_paths: ["package.json"]
        allowed_version_prefixes: [""]
`,
	}))
	if err == nil || !strings.Contains(err.Error(), "cannot contain an empty value") {
		t.Fatalf("expected empty version prefix rejection, got %v", err)
	}
}

func TestParseRequireAssuranceRejectsContradictoryRootEntry(t *testing.T) {
	_, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: "policy.yml", Content: `rules:
  - id: assurance
    kind: require_assurance
    mode: block
    when_paths: ["**"]
    message: native gates
    assurance:
      - id: layout
        type: repository_layout
        allowed_root_entries: ["src"]
        forbidden_root_entries: ["src"]
`,
	}))
	if err == nil || !strings.Contains(err.Error(), "cannot be both forbidden") {
		t.Fatalf("expected contradictory root contract rejection, got %v", err)
	}
}

func TestParseRequireAssuranceRejectsIrrelevantTopLevelField(t *testing.T) {
	_, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: "policy.yml", Content: `rules:
  - id: assurance
    kind: require_assurance
    mode: block
    when_paths: ["**"]
    commands: ["fake success"]
    message: native gates
    assurance:
      - id: live
        type: live_verification
        commands: ["go test ./..."]
`,
	}))
	if err == nil || !strings.Contains(err.Error(), "field 'commands' is not valid") {
		t.Fatalf("expected irrelevant top-level field rejection, got %v", err)
	}
}

func TestParseOtherRuleKindRejectsAssuranceField(t *testing.T) {
	_, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: "policy.yml", Content: `rules:
  - id: deny
    kind: deny_write
    mode: block
    paths: ["generated/**"]
    message: protected
    assurance: []
`,
	}))
	if err == nil || !strings.Contains(err.Error(), "only valid for kind require_assurance") {
		t.Fatalf("expected cross-kind assurance rejection, got %v", err)
	}
}

func TestParseRequireAssuranceRejectsBackslashEscape(t *testing.T) {
	_, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: "policy.yml", Content: `rules:
  - id: assurance
    kind: require_assurance
    mode: block
    when_paths: ["**"]
    message: native gates
    assurance:
      - id: pins
        type: dependency_pins
        manifest_paths: ['..\package.json']
`,
	}))
	if err == nil || !strings.Contains(err.Error(), "repo-relative") {
		t.Fatalf("expected Windows-style path escape rejection, got %v", err)
	}
}

func TestParseRequireAssuranceValidatesPackageScriptCommands(t *testing.T) {
	valid, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: "policy.yml", Content: `rules:
  - id: assurance
    kind: require_assurance
    mode: block
    when_paths: ["**"]
    message: native gates
    assurance:
      - id: scripts
        type: package_scripts
        manifest_paths: ["**/package.json"]
        manifest_markers: ["tsconfig*.json"]
        exclude_paths: ["fixtures/**"]
        package_manager: pnpm
        commands: ["pnpm run test", "npm run lint"]
`,
	}))
	if err != nil {
		t.Fatalf("valid package_scripts gate: %v", err)
	}
	gate := valid.Rules[0].Assurance[0]
	if gate.PackageManager != "pnpm" || len(gate.ManifestMarkers) != 1 || len(gate.ExcludePaths) != 1 {
		t.Fatalf("package_scripts gate = %+v", gate)
	}

	_, err = ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: "policy.yml", Content: `rules:
  - id: assurance
    kind: require_assurance
    mode: block
    when_paths: ["**"]
    message: native gates
    assurance:
      - id: scripts
        type: package_scripts
        manifest_paths: ["**/package.json"]
        commands: ["npm test"]
`,
	}))
	if err == nil || !strings.Contains(err.Error(), "must use '<bun|npm|pnpm|yarn> run <script>'") {
		t.Fatalf("expected malformed package script command rejection, got %v", err)
	}
}
