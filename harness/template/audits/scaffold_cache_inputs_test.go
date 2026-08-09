package main

import (
	"os"
	"path/filepath"
	"slices"
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
		"task-state":              {"docs/tasks.md", "docs/tasks", "docs/spec.md", "tools/reconc/harness/project/config/workflow/task-schema.yaml"},
		"tasks-md-rows-immutable": {"docs/tasks.md"},
		"start-entrypoint":        {"start.md", "AGENTS.md"},
		"spec-format":             {"docs/spec.md"},
		"schema-present":          {"tools/reconc/harness/project/config/workflow/task-schema.yaml", "tools/reconc/harness/project/audits/lib/donecheck/schema.go"},
		"agents-md-mirror":        {"AGENTS.md", "tools/reconc/harness/project/config/workflow/task-schema.yaml"},
	}
	undeclared := []string{
		"repo-layout", "repo-cleanliness", "agent-quality", "agent-hooks",
		"arch-boundaries", "module-contracts", "generated-references",
		"build-baseline", "durable-store", "test-coverage", "git-hooks", "all",
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
		inputs := cacheInputsOf(rule)
		if want, ok := declared[mode]; ok {
			if len(inputs) == 0 {
				t.Fatalf("audit %q declares narrow inputs but its gate declares none", mode)
			}
			slices.Sort(inputs)
			want = slices.Clone(want)
			slices.Sort(want)
			if !slices.Equal(inputs, want) {
				t.Fatalf("gate %q cache inputs drifted\ngot:  %#v\nwant: %#v", mode, inputs, want)
			}
			continue
		}
		if len(inputs) != 0 {
			t.Fatalf("audit %q walks a broad surface; a partial declaration would re-enable unsound reuse: %#v", mode, inputs)
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

func cacheInputsOf(rule string) []string {
	_, after, found := strings.Cut(rule, "cache_inputs:\n")
	if !found {
		return nil
	}
	inputs := []string{}
	for _, line := range strings.Split(after, "\n") {
		if !strings.HasPrefix(line, "      - ") {
			break
		}
		input := strings.TrimSpace(strings.TrimPrefix(line, "      - "))
		inputs = append(inputs, strings.Trim(input, `"`))
	}
	return inputs
}
