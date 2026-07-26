package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc-harness/template/audits/lib/donecheck"
)

func TestAuditSchemaPresentFailsClosedAndAcceptsCanonicalSchema(t *testing.T) {
	root := t.TempDir()
	failures := auditSchemaPresent(root)
	if len(failures) != 1 || !strings.Contains(failures[0], "missing or unreadable") {
		t.Fatalf("missing schema failures = %v", failures)
	}

	writeFile(t, root, schemaRel, "required_sections: [")
	failures = auditSchemaPresent(root)
	if len(failures) != 1 || !strings.Contains(failures[0], "invalid") {
		t.Fatalf("invalid schema failures = %v", failures)
	}

	canonical := readTemplateSchema(t)
	writeFile(t, root, schemaRel, strings.Replace(canonical, "  - Slice\n", "  - Segment\n", 1))
	failures = auditSchemaPresent(root)
	if len(failures) != 1 || !strings.Contains(failures[0], "diverges") {
		t.Fatalf("scope-type drift failures = %v", failures)
	}

	writeFile(t, root, schemaRel, canonical)
	if failures := auditSchemaPresent(root); len(failures) != 0 {
		t.Fatalf("canonical schema failures = %v", failures)
	}
}

func TestAuditAgentsMdMirrorReportsReadAndValueDrift(t *testing.T) {
	previous := loadedSchema
	loadedSchema = donecheck.Schema{
		RequiredSections: []string{"## Why"},
		StatusStates:     []string{"Active", ""},
	}
	t.Cleanup(func() {
		loadedSchema = previous
	})

	root := t.TempDir()
	failures := auditAgentsMdMirror(root)
	if len(failures) != 1 || !strings.Contains(failures[0], "read AGENTS.md") {
		t.Fatalf("missing AGENTS.md failures = %v", failures)
	}

	writeFile(t, root, "AGENTS.md", "Why\n")
	failures = auditAgentsMdMirror(root)
	if len(failures) != 1 || !strings.Contains(failures[0], `"Active"`) {
		t.Fatalf("missing mirrored value failures = %v", failures)
	}

	writeFile(t, root, "AGENTS.md", "Why\nActive\n")
	if failures := auditAgentsMdMirror(root); len(failures) != 0 {
		t.Fatalf("complete AGENTS.md failures = %v", failures)
	}
}

func TestSchemasEqualChecksEveryField(t *testing.T) {
	base := donecheck.DefaultSchema()
	mutations := []struct {
		name   string
		mutate func(*donecheck.Schema)
	}{
		{"required sections", func(schema *donecheck.Schema) { schema.RequiredSections[0] = "changed" }},
		{"status states", func(schema *donecheck.Schema) { schema.StatusStates[0] = "changed" }},
		{"priorities", func(schema *donecheck.Schema) { schema.Priorities[0] = "changed" }},
		{"scope types", func(schema *donecheck.Schema) { schema.ScopeTypes[0] = "changed" }},
		{"sub-task icons", func(schema *donecheck.Schema) { schema.SubTaskIcons[0] = "changed" }},
		{"placeholder values", func(schema *donecheck.Schema) { schema.PlaceholderValues[0] = "changed" }},
		{"test intent keywords", func(schema *donecheck.Schema) { schema.TestIntentKeywords[0] = "changed" }},
		{"reality fields", func(schema *donecheck.Schema) { schema.FinalRealityCheck.RequiredFields[0] = "changed" }},
		{"spec parity", func(schema *donecheck.Schema) { schema.FinalRealityCheck.SpecParityValues[0] = "changed" }},
		{"accepted keywords", func(schema *donecheck.Schema) { schema.FinalRealityCheck.ExceedsUserAcceptedKeywords[0] = "changed" }},
		{"reality prefix", func(schema *donecheck.Schema) { schema.FinalRealityCheck.RealityCheckPrefix = "changed" }},
		{"no-code marker", func(schema *donecheck.Schema) { schema.FinalRealityCheck.TestsNoCodeMarker = "changed" }},
	}
	if !schemasEqual(base, donecheck.DefaultSchema()) {
		t.Fatal("identical default schemas must compare equal")
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			candidate := cloneSchema(base)
			mutation.mutate(&candidate)
			if schemasEqual(base, candidate) {
				t.Fatalf("schemasEqual ignored %s drift", mutation.name)
			}
		})
	}

	candidate := cloneSchema(base)
	candidate.RequiredSections = append(candidate.RequiredSections, "extra")
	if stringSlicesEqual(base.RequiredSections, candidate.RequiredSections) {
		t.Fatal("different slice lengths must not compare equal")
	}
}

func readTemplateSchema(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	moduleRoot := filepath.Dir(wd)
	bytes, err := os.ReadFile(filepath.Join(moduleRoot, "config", "workflow", "task-schema.yaml"))
	if err != nil {
		t.Fatalf("read canonical task schema: %v", err)
	}
	return string(bytes)
}

func cloneSchema(schema donecheck.Schema) donecheck.Schema {
	clone := schema
	clone.RequiredSections = append([]string(nil), schema.RequiredSections...)
	clone.StatusStates = append([]string(nil), schema.StatusStates...)
	clone.Priorities = append([]string(nil), schema.Priorities...)
	clone.ScopeTypes = append([]string(nil), schema.ScopeTypes...)
	clone.SubTaskIcons = append([]string(nil), schema.SubTaskIcons...)
	clone.PlaceholderValues = append([]string(nil), schema.PlaceholderValues...)
	clone.TestIntentKeywords = append([]string(nil), schema.TestIntentKeywords...)
	clone.FinalRealityCheck.RequiredFields = append([]string(nil), schema.FinalRealityCheck.RequiredFields...)
	clone.FinalRealityCheck.SpecParityValues = append([]string(nil), schema.FinalRealityCheck.SpecParityValues...)
	clone.FinalRealityCheck.ExceedsUserAcceptedKeywords = append([]string(nil), schema.FinalRealityCheck.ExceedsUserAcceptedKeywords...)
	return clone
}
