package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScaffoldGatesDeclareTheirCacheInputs binds the shipped policy to the
// audits it invokes. A gate whose audit declares a narrow input set must
// declare the same surface in the policy, so a bootstrapped repository can
// reuse a clean Stop report; a gate whose audit walks a broad surface must
// declare nothing, because a partial declaration would re-enable reuse across
// state the gate actually inspects.
func TestScaffoldGatesDeclareTheirCacheInputs(t *testing.T) {
	declared := map[string][]string{
		"task-state":              {"docs/tasks.md", "docs/tasks", "docs/spec.md"},
		"tasks-md-rows-immutable": {"docs/tasks.md"},
		"start-entrypoint":        {"start.md", "AGENTS.md"},
		"spec-format":             {"docs/spec.md"},
		"schema-present":          {"task-schema.yaml", "donecheck/schema.go"},
		"agents-md-mirror":        {"AGENTS.md", "task-schema.yaml"},
		"build-baseline":          {"stack-config.yaml", "go.mod", "Cargo.toml"},
		"durable-store":           {"stack-config.yaml", "db/migrations"},
	}
	undeclared := []string{
		"repo-layout", "repo-cleanliness", "agent-quality", "agent-hooks",
		"arch-boundaries", "module-contracts", "generated-references",
		"test-coverage", "git-hooks", "all",
	}

	body, err := os.ReadFile(filepath.Join("..", "repo-root-scaffold", ".reconc.yml"))
	if err != nil {
		t.Fatalf("read scaffold policy: %v", err)
	}
	rules := strings.Split(string(body), "\n  - id: ")[1:]
	seen := map[string]bool{}
	for _, rule := range rules {
		if !strings.Contains(rule, "kind: require_script") {
			continue
		}
		mode := auditModeOf(rule)
		if mode == "" {
			t.Fatalf("require_script rule %q declares no audit mode", strings.SplitN(rule, "\n", 2)[0])
		}
		seen[mode] = true
		block := cacheInputsBlockOf(rule)
		if want, ok := declared[mode]; ok {
			if block == "" {
				t.Fatalf("audit %q declares narrow inputs but its gate declares none", mode)
			}
			for _, fragment := range want {
				if !strings.Contains(block, fragment) {
					t.Fatalf("gate %q must declare %q, got:%s", mode, fragment, block)
				}
			}
			continue
		}
		if block != "" {
			t.Fatalf("audit %q walks a broad surface; a partial declaration would re-enable unsound reuse:%s", mode, block)
		}
	}
	for mode := range declared {
		if !seen[mode] {
			t.Fatalf("scaffold policy no longer carries the %q gate", mode)
		}
	}
	for _, mode := range undeclared {
		if !seen[mode] {
			t.Fatalf("scaffold policy no longer carries the %q gate", mode)
		}
	}
}

func auditModeOf(rule string) string {
	_, after, found := strings.Cut(rule, "args:\n")
	if !found {
		return ""
	}
	line := strings.SplitN(after, "\n", 2)[0]
	return strings.Trim(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-")), `"`)
}

func cacheInputsBlockOf(rule string) string {
	_, after, found := strings.Cut(rule, "cache_inputs:\n")
	if !found {
		return ""
	}
	block := ""
	for _, line := range strings.Split(after, "\n") {
		if !strings.HasPrefix(line, "      - ") {
			break
		}
		block += "\n" + line
	}
	return block
}
