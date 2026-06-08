package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reconc-harness/template/audits/lib/donecheck"
)

// auditSchemaPresent verifies the canonical task-schema.yaml exists and is
// loadable. The audit fails closed if the YAML is missing or invalid because
// every other audit then silently falls back to DefaultSchema and drift
// becomes invisible.
func auditSchemaPresent(root string) []string {
	path := filepath.Join(root, filepath.FromSlash(schemaRel))
	if _, err := os.Stat(path); err != nil {
		return []string{fmt.Sprintf("%s missing or unreadable: %v", schemaRel, err)}
	}
	schema, err := donecheck.LoadSchema(path)
	if err != nil {
		return []string{fmt.Sprintf("%s invalid: %v", schemaRel, err)}
	}
	if !schemasEqual(schema, donecheck.DefaultSchema()) {
		return []string{fmt.Sprintf("%s diverges from donecheck.DefaultSchema(); update DefaultSchema or YAML so the in-repo SoT and the failure-safe fallback match", schemaRel)}
	}
	return nil
}

// auditAgentsMdMirror verifies that AGENTS.md (the LLM-facing narrative
// rulebook) literally mentions every machine value declared in
// task-schema.yaml. This makes drift between the human prose and the
// machine-enforced rules detectable instead of silent.
func auditAgentsMdMirror(root string) []string {
	agentsPath := filepath.Join(root, "AGENTS.md")
	contentBytes, err := os.ReadFile(agentsPath)
	if err != nil {
		return []string{fmt.Sprintf("read AGENTS.md: %v", err)}
	}
	content := string(contentBytes)
	var failures []string
	for _, value := range loadedSchema.MirroredValues() {
		if value == "" {
			continue
		}
		if !strings.Contains(content, value) {
			failures = append(failures, fmt.Sprintf("AGENTS.md must mention machine schema value %q (declared in %s)", value, schemaRel))
		}
	}
	return failures
}

// schemasEqual reports whether two Schemas are structurally identical. Used
// only by auditSchemaPresent; cheap O(n) field comparison.
func schemasEqual(a donecheck.Schema, b donecheck.Schema) bool {
	if !stringSlicesEqual(a.RequiredSections, b.RequiredSections) ||
		!stringSlicesEqual(a.StatusStates, b.StatusStates) ||
		!stringSlicesEqual(a.Priorities, b.Priorities) ||
		!stringSlicesEqual(a.SubTaskIcons, b.SubTaskIcons) ||
		!stringSlicesEqual(a.PlaceholderValues, b.PlaceholderValues) ||
		!stringSlicesEqual(a.TestIntentKeywords, b.TestIntentKeywords) {
		return false
	}
	if !stringSlicesEqual(a.FinalRealityCheck.RequiredFields, b.FinalRealityCheck.RequiredFields) ||
		!stringSlicesEqual(a.FinalRealityCheck.SpecParityValues, b.FinalRealityCheck.SpecParityValues) ||
		!stringSlicesEqual(a.FinalRealityCheck.ExceedsUserAcceptedKeywords, b.FinalRealityCheck.ExceedsUserAcceptedKeywords) {
		return false
	}
	if a.FinalRealityCheck.RealityCheckPrefix != b.FinalRealityCheck.RealityCheckPrefix ||
		a.FinalRealityCheck.TestsNoCodeMarker != b.FinalRealityCheck.TestsNoCodeMarker {
		return false
	}
	return true
}

func stringSlicesEqual(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
