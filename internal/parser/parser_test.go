package parser

import (
	stderrors "errors"
	"testing"

	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
)

// makeBundle constructs a minimal SourceBundle directly without going
// through the file system, so parser tests focus on YAML semantics.
func makeBundle(sources ...policy.PolicySource) *ingest.SourceBundle {
	return &ingest.SourceBundle{
		RepoRoot: "/test/repo",
		Sources:  sources,
	}
}

func TestParseNilBundleFails(t *testing.T) {
	_, err := ParseRuleDocuments(nil)
	if err == nil {
		t.Fatal("expected error for nil bundle")
	}
	var rve *rerrors.RuleValidationError
	if !stderrors.As(err, &rve) {
		t.Errorf("expected *RuleValidationError, got %T", err)
	}
}

func TestParseEmptyBundleReturnsDefaults(t *testing.T) {
	parsed, err := ParseRuleDocuments(makeBundle())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.DefaultMode != DefaultMode {
		t.Errorf("expected default mode %s, got %s", DefaultMode, parsed.DefaultMode)
	}
	if len(parsed.Rules) != 0 {
		t.Errorf("expected zero rules, got %d", len(parsed.Rules))
	}
}

func TestParseSimpleDenyWriteRule(t *testing.T) {
	parsed, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind:    policy.SourcePolicyFile,
		Path:    "policies/x.yml",
		Content: "rules:\n  - id: deny-gen\n    kind: deny_write\n    paths: ['generated/**']\n    mode: block\n    message: protect generated\n",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parsed.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(parsed.Rules))
	}
	r := parsed.Rules[0]
	if r.ID != "deny-gen" {
		t.Errorf("expected id deny-gen, got %s", r.ID)
	}
	if r.Kind != policy.KindDenyWrite {
		t.Errorf("expected kind deny_write, got %s", r.Kind)
	}
	if r.Mode != policy.ModeBlock {
		t.Errorf("expected mode block, got %s", r.Mode)
	}
	if r.SourcePath != "policies/x.yml" {
		t.Errorf("expected source provenance, got %s", r.SourcePath)
	}
}

func TestParseRequiresFieldsByKind(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantSub string
	}{
		{
			name:    "deny_write missing paths",
			yaml:    "rules:\n  - id: x\n    kind: deny_write\n    mode: block\n    message: x\n",
			wantSub: "field 'paths'",
		},
		{
			name:    "require_read missing before_paths",
			yaml:    "rules:\n  - id: x\n    kind: require_read\n    paths: ['src/**']\n    mode: warn\n    message: x\n",
			wantSub: "field 'before_paths'",
		},
		{
			name:    "require_command missing commands",
			yaml:    "rules:\n  - id: x\n    kind: require_command\n    when_paths: ['src/**']\n    mode: warn\n    message: x\n",
			wantSub: "field 'commands'",
		},
		{
			name:    "couple_change missing when_paths",
			yaml:    "rules:\n  - id: x\n    kind: couple_change\n    paths: ['src/**']\n    mode: warn\n    message: x\n",
			wantSub: "field 'when_paths'",
		},
		{
			name:    "require_claim missing claims",
			yaml:    "rules:\n  - id: x\n    kind: require_claim\n    when_paths: ['src/**']\n    mode: warn\n    message: x\n",
			wantSub: "field 'claims'",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
				Kind:    policy.SourcePolicyFile,
				Path:    "policies/x.yml",
				Content: c.yaml,
			}))
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !contains(err.Error(), c.wantSub) {
				t.Errorf("expected error to mention %q, got: %v", c.wantSub, err)
			}
		})
	}
}

func TestParseRejectsUnknownRuleKind(t *testing.T) {
	_, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind:    policy.SourcePolicyFile,
		Path:    "policies/x.yml",
		Content: "rules:\n  - id: x\n    kind: explode_universe\n    mode: block\n    message: x\n",
	}))
	if err == nil {
		t.Fatal("expected error for unknown kind")
	}
	if !contains(err.Error(), "unknown rule kind") {
		t.Errorf("expected 'unknown rule kind' in error, got: %v", err)
	}
}

func TestParseRejectsInvalidMode(t *testing.T) {
	_, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind:    policy.SourcePolicyFile,
		Path:    "policies/x.yml",
		Content: "rules:\n  - id: x\n    kind: deny_write\n    paths: ['x']\n    mode: nope\n    message: x\n",
	}))
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
	if !contains(err.Error(), "invalid rule mode") {
		t.Errorf("expected 'invalid rule mode' in error, got: %v", err)
	}
}

func TestParseRequiresMessage(t *testing.T) {
	_, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind:    policy.SourcePolicyFile,
		Path:    "policies/x.yml",
		Content: "rules:\n  - id: x\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n",
	}))
	if err == nil {
		t.Fatal("expected error for missing message")
	}
	if !contains(err.Error(), "message is required") {
		t.Errorf("expected 'message is required' in error, got: %v", err)
	}
}

func TestParseRequiresID(t *testing.T) {
	_, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind:    policy.SourcePolicyFile,
		Path:    "policies/x.yml",
		Content: "rules:\n  - kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: x\n",
	}))
	if err == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestParseDuplicateIDsAcrossSourcesFails(t *testing.T) {
	_, err := ParseRuleDocuments(makeBundle(
		policy.PolicySource{
			Kind:    policy.SourcePolicyFile,
			Path:    "policies/a.yml",
			Content: "rules:\n  - id: dup\n    kind: deny_write\n    paths: ['a']\n    mode: warn\n    message: a\n",
		},
		policy.PolicySource{
			Kind:    policy.SourcePolicyFile,
			Path:    "policies/b.yml",
			Content: "rules:\n  - id: dup\n    kind: deny_write\n    paths: ['b']\n    mode: warn\n    message: b\n",
		},
	))
	if err == nil {
		t.Fatal("expected error for duplicate id")
	}
	if !contains(err.Error(), "duplicate rule id") {
		t.Errorf("expected 'duplicate rule id' in error, got: %v", err)
	}
}

func TestParseDefaultModeFromCompilerConfig(t *testing.T) {
	parsed, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind:    policy.SourceCompilerConfig,
		Path:    ".reconc.yml",
		Content: "default_mode: block\nrules:\n  - id: x\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: x\n",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.DefaultMode != policy.ModeBlock {
		t.Errorf("expected default_mode block, got %s", parsed.DefaultMode)
	}
}

func TestParseRejectsInvalidDefaultMode(t *testing.T) {
	_, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind:    policy.SourceCompilerConfig,
		Path:    ".reconc.yml",
		Content: "default_mode: nope\nrules: []\n",
	}))
	if err == nil {
		t.Fatal("expected error for invalid default_mode")
	}
}

func TestParseSkipsContextOnlySources(t *testing.T) {
	// AGENTS.md / CLAUDE.md / start.md sources should not be
	// parsed as rule documents (they carry prose).
	parsed, err := ParseRuleDocuments(makeBundle(
		policy.PolicySource{
			Kind:    policy.SourceAgentsMD,
			Path:    "AGENTS.md",
			Content: "# this is prose, not yaml\nignored\n",
		},
		policy.PolicySource{
			Kind:    policy.SourceClaudeMD,
			Path:    "CLAUDE.md",
			Content: "# more prose\n",
		},
		policy.PolicySource{
			Kind:    policy.SourceStartMD,
			Path:    "start.md",
			Content: "# even more prose\n",
		},
	))
	if err != nil {
		t.Fatalf("unexpected error skipping context sources: %v", err)
	}
	if len(parsed.Rules) != 0 {
		t.Errorf("expected no rules from context-only bundle, got %d", len(parsed.Rules))
	}
}

func TestParseRejectsRulesNotList(t *testing.T) {
	_, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind:    policy.SourcePolicyFile,
		Path:    "policies/x.yml",
		Content: "rules: not-a-list\n",
	}))
	if err == nil {
		t.Fatal("expected error when rules is not a list")
	}
}

func TestParseAllSevenRuleKinds(t *testing.T) {
	yaml := `rules:
  - id: r1
    kind: deny_write
    paths: ['a/**']
    mode: block
    message: m1
  - id: r2
    kind: require_read
    paths: ['b/**']
    before_paths: ['docs/**']
    mode: warn
    message: m2
  - id: r3
    kind: require_command
    when_paths: ['c/**']
    commands: ['echo']
    mode: warn
    message: m3
  - id: r4
    kind: require_command_success
    when_paths: ['d/**']
    commands: ['true']
    mode: warn
    message: m4
  - id: r5
    kind: forbid_command
    commands: ['rm -rf /']
    mode: block
    message: m5
  - id: r6
    kind: couple_change
    paths: ['src/**']
    when_paths: ['tests/**']
    mode: warn
    message: m6
  - id: r7
    kind: require_claim
    when_paths: ['src/**']
    claims: ['ci-green']
    mode: block
    message: m7
`
	parsed, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind:    policy.SourcePolicyFile,
		Path:    "policies/all.yml",
		Content: yaml,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parsed.Rules) != 7 {
		t.Fatalf("expected 7 rules, got %d", len(parsed.Rules))
	}
}

// --- Phase 4A (W22) tests for require_fresh_file + require_evidence ---

func TestParseRequireFreshFileMinimal(t *testing.T) {
	parsed, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind:    policy.SourcePolicyFile,
		Path:    "policies/x.yml",
		Content: "rules:\n  - id: r\n    kind: require_fresh_file\n    when_paths: ['docs/todo/*.md']\n    required_files:\n      - path: 'docs/fidelity/x.json'\n        max_age_hours: 24\n    mode: block\n    message: m\n",
	}))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(parsed.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(parsed.Rules))
	}
	r := parsed.Rules[0]
	if r.Kind != policy.KindRequireFreshFile {
		t.Errorf("kind wrong")
	}
	if len(r.RequiredFiles) != 1 || r.RequiredFiles[0].Path != "docs/fidelity/x.json" {
		t.Errorf("required_files wrong: %v", r.RequiredFiles)
	}
	if r.RequiredFiles[0].MaxAgeHours != 24 {
		t.Errorf("max_age_hours wrong: %d", r.RequiredFiles[0].MaxAgeHours)
	}
}

func TestParseRequireFreshFileRejectsMissingRequiredFiles(t *testing.T) {
	_, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind:    policy.SourcePolicyFile,
		Path:    "x.yml",
		Content: "rules:\n  - id: r\n    kind: require_fresh_file\n    when_paths: ['x']\n    mode: warn\n    message: m\n",
	}))
	if err == nil {
		t.Fatal("expected error for missing required_files")
	}
	if !contains(err.Error(), "required_files") {
		t.Errorf("expected required_files mention, got: %v", err)
	}
}

func TestParseRequireFreshFileRejectsMissingWhenPaths(t *testing.T) {
	_, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind:    policy.SourcePolicyFile,
		Path:    "x.yml",
		Content: "rules:\n  - id: r\n    kind: require_fresh_file\n    required_files:\n      - path: 'x'\n        max_age_hours: 1\n    mode: warn\n    message: m\n",
	}))
	if err == nil {
		t.Fatal("expected error for missing when_paths")
	}
}

func TestParseRequireEvidenceMinimal(t *testing.T) {
	parsed, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind:    policy.SourcePolicyFile,
		Path:    "x.yml",
		Content: "rules:\n  - id: r\n    kind: require_evidence\n    when_paths: ['src/**']\n    evidence:\n      - file: 'docs/coverage.md'\n        must_not_contain: 'FAIL'\n    mode: warn\n    message: m\n",
	}))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	r := parsed.Rules[0]
	if r.Kind != policy.KindRequireEvidence {
		t.Errorf("kind wrong")
	}
	if len(r.Evidence) != 1 || r.Evidence[0].File != "docs/coverage.md" {
		t.Errorf("evidence wrong")
	}
	if r.Evidence[0].MustNotContain != "FAIL" {
		t.Errorf("must_not_contain wrong: %s", r.Evidence[0].MustNotContain)
	}
}

func TestParseRequireEvidenceRequiresAtLeastOneAssertion(t *testing.T) {
	_, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind:    policy.SourcePolicyFile,
		Path:    "x.yml",
		Content: "rules:\n  - id: r\n    kind: require_evidence\n    when_paths: ['x']\n    evidence:\n      - file: 'foo.md'\n    mode: warn\n    message: m\n",
	}))
	if err == nil {
		t.Fatal("expected error for evidence with no assertions")
	}
	if !contains(err.Error(), "at least one") {
		t.Errorf("expected 'at least one' in error, got: %v", err)
	}
}

func TestParseRequireEvidenceMustContainList(t *testing.T) {
	parsed, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind:    policy.SourcePolicyFile,
		Path:    "x.yml",
		Content: "rules:\n  - id: r\n    kind: require_evidence\n    when_paths: ['x']\n    evidence:\n      - file: 'f.md'\n        must_contain:\n          - 'a'\n          - 'b'\n    mode: warn\n    message: m\n",
	}))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(parsed.Rules[0].Evidence[0].MustContain) != 2 {
		t.Errorf("expected 2 must_contain entries, got %d", len(parsed.Rules[0].Evidence[0].MustContain))
	}
}

func TestParseSourcePathProvenancePreserved(t *testing.T) {
	parsed, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind:    policy.SourceInlineBlock,
		Path:    "AGENTS.md",
		Content: "rules:\n  - id: from-md\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: x\n",
		BlockID: "AGENTS.md:14",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := parsed.Rules[0]
	if r.SourcePath != "AGENTS.md" {
		t.Errorf("expected SourcePath AGENTS.md, got %s", r.SourcePath)
	}
	if r.SourceBlockID != "AGENTS.md:14" {
		t.Errorf("expected SourceBlockID, got %s", r.SourceBlockID)
	}
}

// --- W18: rule templates --------------------------------------------

func TestParseRuleWithBuiltinTemplate(t *testing.T) {
	yml := "rules:\n  - id: t1\n    template: tests-follow-source\n    paths: ['src/**']\n    when_paths: ['tests/**']\n"
	parsed, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: "p.yml", Content: yml,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parsed.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(parsed.Rules))
	}
	r := parsed.Rules[0]
	if r.ID != "t1" {
		t.Errorf("expected id t1, got %s", r.ID)
	}
	// kind / mode / message must be inherited from template.
	if r.Kind != policy.KindCoupleChange {
		t.Errorf("kind not inherited from template; got %s", r.Kind)
	}
	if r.Mode != policy.ModeWarn {
		t.Errorf("mode not inherited; got %s", r.Mode)
	}
	if r.Message == "" {
		t.Errorf("message not inherited from template")
	}
	// paths / when_paths provided by user must survive.
	if len(r.Paths) != 1 || r.Paths[0] != "src/**" {
		t.Errorf("user paths not preserved; got %v", r.Paths)
	}
	if len(r.WhenPaths) != 1 || r.WhenPaths[0] != "tests/**" {
		t.Errorf("user when_paths not preserved; got %v", r.WhenPaths)
	}
}

func TestParseRuleTemplateUserModeOverride(t *testing.T) {
	// Template sets mode: warn; user sets mode: block. User wins.
	yml := "rules:\n  - id: t1\n    template: tests-follow-source\n    paths: ['x']\n    when_paths: ['y']\n    mode: block\n"
	parsed, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: "p.yml", Content: yml,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.Rules[0].Mode != policy.ModeBlock {
		t.Errorf("user mode override lost; got %s", parsed.Rules[0].Mode)
	}
}

func TestParseRuleTemplateUnknownFails(t *testing.T) {
	yml := "rules:\n  - id: t1\n    template: bogus-template-name\n    paths: ['x']\n"
	_, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: "p.yml", Content: yml,
	}))
	if err == nil {
		t.Fatal("expected error for unknown template")
	}
	if !contains(err.Error(), "not found") {
		t.Errorf("error should mention template not found; got: %v", err)
	}
}

func TestParseRuleTemplateInheritsPaths(t *testing.T) {
	// no-generated-writes template pre-fills paths; user just gives id.
	yml := "rules:\n  - id: no-gen\n    template: no-generated-writes\n"
	parsed, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: "p.yml", Content: yml,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parsed.Rules[0].Paths) < 3 {
		t.Errorf("expected template paths to be inherited; got %v", parsed.Rules[0].Paths)
	}
}

// --- W17: scoped rules (monorepo) ------------------------------------

func TestParseScopedRules(t *testing.T) {
	yml := `rules:
  - id: global
    kind: deny_write
    paths: ['**/.env']
    mode: block
    message: g
scopes:
  - id: web
    paths: ['apps/web/**']
    rules:
      - id: web-gen
        kind: deny_write
        paths: ['apps/web/generated/**']
        mode: block
        message: m
`
	parsed, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: "p.yml", Content: yml,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parsed.Rules) != 2 {
		t.Fatalf("expected 2 rules (1 global + 1 scoped), got %d", len(parsed.Rules))
	}

	var globalRule, scopedRule *policy.Rule
	for i := range parsed.Rules {
		if parsed.Rules[i].ID == "global" {
			globalRule = &parsed.Rules[i]
		}
		if parsed.Rules[i].ID == "web-gen" {
			scopedRule = &parsed.Rules[i]
		}
	}
	if globalRule == nil || scopedRule == nil {
		t.Fatal("expected both rules to be present")
	}
	if len(globalRule.ScopePaths) != 0 {
		t.Errorf("global rule should have empty ScopePaths; got %v", globalRule.ScopePaths)
	}
	if len(scopedRule.ScopePaths) != 1 || scopedRule.ScopePaths[0] != "apps/web/**" {
		t.Errorf("scoped rule should inherit scope paths; got %v", scopedRule.ScopePaths)
	}
	if scopedRule.ScopeID != "web" {
		t.Errorf("scoped rule should inherit scope id; got %q", scopedRule.ScopeID)
	}
}

func TestParseScopedRulesMultipleScopes(t *testing.T) {
	yml := `scopes:
  - id: web
    paths: ['apps/web/**']
    rules:
      - id: w1
        kind: deny_write
        paths: ['apps/web/x']
        mode: warn
        message: m
  - id: mobile
    paths: ['apps/mobile/**']
    rules:
      - id: m1
        kind: deny_write
        paths: ['apps/mobile/x']
        mode: warn
        message: m
`
	parsed, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: "p.yml", Content: yml,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parsed.Rules) != 2 {
		t.Fatalf("expected 2 rules (one per scope), got %d", len(parsed.Rules))
	}
}

func TestParseScopeMissingPathsFails(t *testing.T) {
	yml := `scopes:
  - id: bad
    rules:
      - id: x
        kind: deny_write
        paths: ['x']
        mode: warn
        message: m
`
	_, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: "p.yml", Content: yml,
	}))
	if err == nil {
		t.Fatal("expected error for scope missing paths")
	}
}

func TestParseScopeEmptyRulesIsLegal(t *testing.T) {
	yml := `scopes:
  - id: empty
    paths: ['apps/empty/**']
`
	parsed, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: "p.yml", Content: yml,
	}))
	if err != nil {
		t.Fatalf("empty scope rules list should not fail: %v", err)
	}
	if len(parsed.Rules) != 0 {
		t.Errorf("expected 0 rules, got %d", len(parsed.Rules))
	}
}

func TestParseScopedRuleDuplicateIDFails(t *testing.T) {
	yml := `rules:
  - id: dup
    kind: deny_write
    paths: ['x']
    mode: warn
    message: m
scopes:
  - id: s
    paths: ['x/**']
    rules:
      - id: dup
        kind: deny_write
        paths: ['y']
        mode: warn
        message: m
`
	_, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: "p.yml", Content: yml,
	}))
	if err == nil {
		t.Fatal("expected duplicate-id error across global and scoped rules")
	}
}

// --- W31: rule deprecation ------------------------------------------

func TestParseRuleDeprecated(t *testing.T) {
	yml := `rules:
  - id: legacy
    kind: deny_write
    paths: ['x']
    mode: warn
    message: m
    deprecated: true
    deprecated_since: "2026-01-15"
    deprecated_reason: "scope too broad"
    deprecated_replaced_by: "new-rule"
`
	parsed, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: "p.yml", Content: yml,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := parsed.Rules[0]
	if !r.Deprecated {
		t.Errorf("expected Deprecated=true")
	}
	if r.DeprecatedSince != "2026-01-15" {
		t.Errorf("DeprecatedSince wrong: %s", r.DeprecatedSince)
	}
	if r.DeprecatedReplacedBy != "new-rule" {
		t.Errorf("DeprecatedReplacedBy wrong: %s", r.DeprecatedReplacedBy)
	}
	if r.DeprecatedReason != "scope too broad" {
		t.Errorf("DeprecatedReason wrong: %s", r.DeprecatedReason)
	}
}

func TestParseRuleDeprecatedNonBoolFails(t *testing.T) {
	yml := "rules:\n  - id: r1\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: m\n    deprecated: \"yes\"\n"
	_, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: "p.yml", Content: yml,
	}))
	if err == nil {
		t.Fatal("expected error for non-bool deprecated field")
	}
}

func TestParseRuleWithoutDeprecationFields(t *testing.T) {
	yml := "rules:\n  - id: r1\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: m\n"
	parsed, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: "p.yml", Content: yml,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := parsed.Rules[0]
	if r.Deprecated {
		t.Errorf("Deprecated should default to false")
	}
	if r.DeprecatedSince != "" || r.DeprecatedReason != "" || r.DeprecatedReplacedBy != "" {
		t.Errorf("deprecation fields should be empty by default")
	}
}

func TestIsRepoRelativePath(t *testing.T) {
	cases := []struct {
		name string
		path string
		want bool
	}{
		{name: "relative", path: "scripts/check.sh", want: true},
		{name: "trimmed", path: "  docs/report.md  ", want: true},
		{name: "absolute", path: "/tmp/nope", want: false},
		{name: "escape", path: "../secret.sh", want: false},
		{name: "windows-drive", path: "C:/tmp/nope", want: false},
		{name: "empty", path: "   ", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRepoRelativePath(tc.path); got != tc.want {
				t.Fatalf("isRepoRelativePath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestParseCheckSupportedKinds(t *testing.T) {
	cases := []struct {
		name string
		item map[string]interface{}
		kind policy.Kind
	}{
		{
			name: "require-fresh-file",
			item: map[string]interface{}{"kind": "require_fresh_file", "path": "docs/report.md", "max_age_hours": 24},
			kind: policy.KindRequireFreshFile,
		},
		{
			name: "require-evidence",
			item: map[string]interface{}{"kind": "require_evidence", "file": "docs/coverage.md", "must_exist": true},
			kind: policy.KindRequireEvidence,
		},
		{
			name: "require-claim",
			item: map[string]interface{}{"kind": "require_claim", "claims": []interface{}{"ci-green"}},
			kind: policy.KindRequireClaim,
		},
		{
			name: "require-command",
			item: map[string]interface{}{"kind": "require_command", "commands": []interface{}{"go test ./..."}},
			kind: policy.KindRequireCommand,
		},
		{
			name: "forbid-command",
			item: map[string]interface{}{"kind": "forbid_command", "commands": []interface{}{"rm -rf /"}},
			kind: policy.KindForbidCommand,
		},
		{
			name: "deny-write",
			item: map[string]interface{}{"kind": "deny_write", "paths": []interface{}{"gen/**"}},
			kind: policy.KindDenyWrite,
		},
		{
			name: "require-script",
			item: map[string]interface{}{"kind": "require_script", "script": "scripts/check.sh", "args": []interface{}{"--fast"}, "timeout_sec": 30},
			kind: policy.KindRequireScript,
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseCheck(tc.item, "rule-1", "checks", i)
			if err != nil {
				t.Fatalf("parseCheck returned error: %v", err)
			}
			if got.Kind != tc.kind {
				t.Fatalf("parseCheck kind = %s, want %s", got.Kind, tc.kind)
			}
		})
	}
}

func TestParseCheckRejectsInvalidShapes(t *testing.T) {
	cases := []struct {
		name    string
		item    map[string]interface{}
		wantSub string
	}{
		{
			name:    "nested-composite",
			item:    map[string]interface{}{"kind": "all_of"},
			wantSub: "nested composite kinds are not supported",
		},
		{
			name:    "script-not-relative",
			item:    map[string]interface{}{"kind": "require_script", "script": "../hack.sh"},
			wantSub: "repo-relative path",
		},
		{
			name:    "evidence-needs-assertion",
			item:    map[string]interface{}{"kind": "require_evidence", "file": "docs/evidence.md"},
			wantSub: "must specify at least one",
		},
		{
			name:    "unknown-subcheck-kind",
			item:    map[string]interface{}{"kind": "explode"},
			wantSub: "not a recognized kind",
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseCheck(tc.item, "rule-1", "checks", i)
			if err == nil {
				t.Fatal("expected parseCheck error")
			}
			if !contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestOptionalCheckListDirectBranches(t *testing.T) {
	if checks, err := optionalCheckList(map[string]interface{}{}, "checks", "rule-1"); err != nil {
		t.Fatalf("unexpected error for missing checks: %v", err)
	} else if checks != nil {
		t.Fatalf("missing checks should return nil, got %#v", checks)
	}

	_, err := optionalCheckList(map[string]interface{}{"checks": "nope"}, "checks", "rule-1")
	if err == nil || !contains(err.Error(), "must be a list of check mappings") {
		t.Fatalf("expected list-of-check-mappings error, got %v", err)
	}

	_, err = optionalCheckList(map[string]interface{}{"checks": []interface{}{"nope"}}, "checks", "rule-1")
	if err == nil || !contains(err.Error(), "must be a YAML mapping") {
		t.Fatalf("expected YAML mapping error, got %v", err)
	}

	checks, err := optionalCheckList(
		map[string]interface{}{
			"checks": []interface{}{
				map[string]interface{}{
					"kind":           "require_evidence",
					"file":           "docs/evidence.md",
					"must_exist":     true,
					"must_contain":   []interface{}{"PASS"},
					"optional":       true,
					"max_line_count": 5,
				},
			},
		},
		"checks",
		"rule-1",
	)
	if err != nil {
		t.Fatalf("unexpected error for valid checks: %v", err)
	}
	if len(checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(checks))
	}
	if checks[0].Kind != policy.KindRequireEvidence || checks[0].File != "docs/evidence.md" || !checks[0].MustExist || !checks[0].Optional || checks[0].MaxLineCount != 5 {
		t.Fatalf("parsed check mismatch: %#v", checks[0])
	}
}

func TestParseCheckAdditionalBranches(t *testing.T) {
	successCases := []struct {
		name string
		item map[string]interface{}
		want policy.Check
	}{
		{
			name: "require-command-success",
			item: map[string]interface{}{"kind": "require_command_success", "commands": []interface{}{"go test ./..."}},
			want: policy.Check{Kind: policy.KindRequireCommandSuccess, Commands: []string{"go test ./..."}},
		},
		{
			name: "deny-write-optional",
			item: map[string]interface{}{"kind": "deny_write", "paths": []interface{}{"gen/**"}, "optional": true},
			want: policy.Check{Kind: policy.KindDenyWrite, Paths: []string{"gen/**"}, Optional: true},
		},
		{
			name: "require-script-all-fields",
			item: map[string]interface{}{"kind": "require_script", "script": "scripts/check.sh", "args": []interface{}{"--fast"}, "timeout_sec": 30, "optional": true},
			want: policy.Check{Kind: policy.KindRequireScript, Script: "scripts/check.sh", Args: []string{"--fast"}, TimeoutSec: 30, Optional: true},
		},
		{
			name: "require-evidence-all-assertions",
			item: map[string]interface{}{
				"kind":             "require_evidence",
				"file":             "docs/evidence.md",
				"must_exist":       true,
				"must_contain":     []interface{}{"PASS"},
				"must_not_contain": "FAIL",
				"max_line_count":   7,
				"optional":         true,
			},
			want: policy.Check{
				Kind:           policy.KindRequireEvidence,
				File:           "docs/evidence.md",
				MustExist:      true,
				MustContain:    []string{"PASS"},
				MustNotContain: "FAIL",
				MaxLineCount:   7,
				Optional:       true,
			},
		},
	}
	for i, tc := range successCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseCheck(tc.item, "rule-1", "checks", i)
			if err != nil {
				t.Fatalf("parseCheck returned error: %v", err)
			}
			if got.Kind != tc.want.Kind || got.File != tc.want.File || got.Script != tc.want.Script || got.TimeoutSec != tc.want.TimeoutSec || got.Optional != tc.want.Optional {
				t.Fatalf("parseCheck mismatch: got %#v want %#v", got, tc.want)
			}
			if len(got.Commands) != len(tc.want.Commands) || len(got.Paths) != len(tc.want.Paths) || len(got.Args) != len(tc.want.Args) || len(got.MustContain) != len(tc.want.MustContain) {
				t.Fatalf("slice length mismatch: got %#v want %#v", got, tc.want)
			}
		})
	}

	errorCases := []struct {
		name    string
		item    map[string]interface{}
		wantSub string
	}{
		{
			name:    "negative-fresh-file-age",
			item:    map[string]interface{}{"kind": "require_fresh_file", "path": "docs/report.md", "max_age_hours": -1},
			wantSub: "must be >= 0",
		},
		{
			name:    "negative-evidence-max-lines",
			item:    map[string]interface{}{"kind": "require_evidence", "file": "docs/evidence.md", "max_line_count": -1},
			wantSub: "must be >= 0",
		},
		{
			name:    "missing-claims",
			item:    map[string]interface{}{"kind": "require_claim"},
			wantSub: ".claims' is required",
		},
		{
			name:    "missing-commands",
			item:    map[string]interface{}{"kind": "require_command_success"},
			wantSub: ".commands' is required",
		},
		{
			name:    "missing-paths",
			item:    map[string]interface{}{"kind": "deny_write"},
			wantSub: ".paths' is required",
		},
		{
			name:    "negative-script-timeout",
			item:    map[string]interface{}{"kind": "require_script", "script": "scripts/check.sh", "timeout_sec": -1},
			wantSub: "must be >= 0",
		},
		{
			name:    "unsupported-require-read-subcheck",
			item:    map[string]interface{}{"kind": "require_read"},
			wantSub: "not yet supported as a sub-check",
		},
		{
			name:    "optional-must-be-bool",
			item:    map[string]interface{}{"kind": "deny_write", "paths": []interface{}{"gen/**"}, "optional": "yes"},
			wantSub: ".optional' must be a boolean",
		},
	}
	for i, tc := range errorCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseCheck(tc.item, "rule-1", "checks", i)
			if err == nil {
				t.Fatal("expected parseCheck error")
			}
			if !contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestOptionalRequiredFileListDirectBranches(t *testing.T) {
	files, err := optionalRequiredFileList(
		map[string]interface{}{
			"required_files": []interface{}{
				map[string]interface{}{"path": "docs/report.md", "max_age_hours": 24, "optional": true},
			},
		},
		"required_files",
		"rule-1",
	)
	if err != nil {
		t.Fatalf("unexpected error for valid required_files: %v", err)
	}
	if len(files) != 1 || files[0].Path != "docs/report.md" || files[0].MaxAgeHours != 24 || !files[0].Optional {
		t.Fatalf("parsed required_files mismatch: %#v", files)
	}

	_, err = optionalRequiredFileList(
		map[string]interface{}{
			"required_files": []interface{}{
				map[string]interface{}{"path": "docs/report.md", "max_age_hours": -1},
			},
		},
		"required_files",
		"rule-1",
	)
	if err == nil || !contains(err.Error(), "must be >= 0") {
		t.Fatalf("expected max_age_hours validation error, got %v", err)
	}
}

func TestOptionalEvidenceCheckListDirectBranches(t *testing.T) {
	evidence, err := optionalEvidenceCheckList(
		map[string]interface{}{
			"evidence": []interface{}{
				map[string]interface{}{
					"file":             "docs/evidence.md",
					"must_exist":       true,
					"must_contain":     []interface{}{"PASS"},
					"must_not_contain": "FAIL",
					"max_line_count":   7,
					"optional":         true,
				},
			},
		},
		"evidence",
		"rule-1",
	)
	if err != nil {
		t.Fatalf("unexpected error for valid evidence: %v", err)
	}
	if len(evidence) != 1 || evidence[0].File != "docs/evidence.md" || !evidence[0].MustExist || evidence[0].MustNotContain != "FAIL" || evidence[0].MaxLineCount != 7 || !evidence[0].Optional {
		t.Fatalf("parsed evidence mismatch: %#v", evidence)
	}

	_, err = optionalEvidenceCheckList(
		map[string]interface{}{
			"evidence": []interface{}{
				map[string]interface{}{"file": "docs/evidence.md", "max_line_count": -1},
			},
		},
		"evidence",
		"rule-1",
	)
	if err == nil || !contains(err.Error(), "must be >= 0") {
		t.Fatalf("expected max_line_count validation error, got %v", err)
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
