package compiler

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestRenderRepoPolicyWithCandidateAddsActionsInMemoryOnly(t *testing.T) {
	repo := t.TempDir()
	config := `actions:
  tools:
    - id: database-write
      transport: mcp_stdio
      server_label: database
      tool: execute
      effect:
        kind: external
  rules: []
rules: []
`
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(repo, LockfileRelativePath)
	before, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	compiled, body, baseDigest, err := RenderRepoPolicyWithCandidate(repo, "test", CandidateSource{
		Kind: policy.SourcePolicyFile, Name: "candidate",
		Content: `actions:
  rules:
    - id: block-production
      selector:
        tool_ids: [database-write]
      decision: block
rules: []
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if compiled.ActionToolCount != 1 || compiled.ActionRuleCount != 1 || baseDigest == compiled.SourceDigest ||
		!bytes.Contains(body, []byte(policy.ImpactCandidateBlockPrefix+"candidate")) {
		t.Fatalf("compiled candidate = %+v, base=%s", compiled, baseDigest)
	}
	after, err := os.ReadFile(lockPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("candidate render mutated current lock: %v", err)
	}
	preset, presetBody, _, err := RenderRepoPolicyWithCandidate(repo, "test", CandidateSource{
		Kind: policy.SourcePreset, Name: "candidate-preset",
		Content: `actions:
  rules:
    - id: warn-production
      selector:
        tool_ids: [database-write]
      decision: warn
rules: []
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if preset.ActionToolCount != 1 || preset.ActionRuleCount != 1 ||
		!bytes.Contains(presetBody, []byte(policy.ImpactCandidateBlockPrefix+"candidate-preset")) {
		t.Fatalf("compiled preset candidate = %+v", preset)
	}

	_, _, _, err = RenderRepoPolicyWithCandidate(repo, "test", CandidateSource{
		Kind: policy.SourcePolicyFile, Name: "duplicate",
		Content: `actions:
  tools:
    - id: database-write
      transport: mcp_stdio
      server_label: other
      tool: execute
      effect:
        kind: external
rules: []
`,
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Fatalf("duplicate candidate action tool error = %v", err)
	}
}
