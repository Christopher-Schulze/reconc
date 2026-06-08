package donecheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultSchemaValidates(t *testing.T) {
	if err := DefaultSchema().validate(); err != nil {
		t.Fatalf("DefaultSchema must validate, got %v", err)
	}
}

func TestLoadSchemaHappy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "schema.yaml")
	if err := os.WriteFile(path, []byte(`required_sections: ["## Why"]
status_states: [Active, Done]
priorities: [P0]
scope_types: [Slice]
sub_task_icons: [" ", x]
final_reality_check:
  required_fields: [Spec Parity]
  spec_parity_values: [MATCHES]
  reality_check_prefix: "PASS - "
  tests_no_code_marker: NO_CODE_CHANGED
  exceeds_user_accepted_keywords: [user]
placeholder_values: [todo]
test_intent_keywords: [test]
`), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	schema, err := LoadSchema(path)
	if err != nil {
		t.Fatalf("LoadSchema: %v", err)
	}
	if !schema.IsValidStatusState("Active") || !schema.IsValidStatusState("Done") {
		t.Fatal("expected Active and Done to be valid")
	}
	if schema.IsValidStatusState("Bogus") {
		t.Fatal("Bogus must not be valid")
	}
	if !schema.IsValidPriority("P0") || schema.IsValidPriority("P5") {
		t.Fatal("priority validation failed")
	}
	if !schema.IsValidSpecParity("MATCHES") || schema.IsValidSpecParity("WHATEVER") {
		t.Fatal("parity validation failed")
	}
}

func TestLoadSchemaMissingFile(t *testing.T) {
	if _, err := LoadSchema(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadSchemaInvalidYaml(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("not: [valid"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadSchema(path); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestLoadSchemaRejectsEmptySections(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(path, []byte(`required_sections: []
status_states: [Done]
priorities: [P0]
scope_types: [Slice]
sub_task_icons: [x]
final_reality_check:
  required_fields: [Tests]
  spec_parity_values: [MATCHES]
  reality_check_prefix: "PASS - "
  tests_no_code_marker: X
  exceeds_user_accepted_keywords: [user]
`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadSchema(path); err == nil || !strings.Contains(err.Error(), "required_sections") {
		t.Fatalf("expected validation error for empty required_sections, got %v", err)
	}
}

func TestMirroredValues(t *testing.T) {
	values := DefaultSchema().MirroredValues()
	expected := []string{"Why", "Active", "P0", "Slice", "Spec Parity", "MATCHES", "NO_CODE_CHANGED"}
	for _, want := range expected {
		found := false
		for _, v := range values {
			if v == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("MirroredValues missing %q (got %v)", want, values)
		}
	}
	for _, v := range values {
		if strings.HasPrefix(v, "## ") {
			t.Fatalf("MirroredValues must strip H2 prefix, found %q", v)
		}
	}
}

func TestRealRepoSchemaLoads(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot := wd
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(repoRoot, "go.mod")); err == nil {
			break
		}
		repoRoot = filepath.Dir(repoRoot)
	}
	path := filepath.Join(repoRoot, "codebase", "config", "workflow", "task-schema.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("schema not yet present: %v", err)
	}
	schema, err := LoadSchema(path)
	if err != nil {
		t.Fatalf("LoadSchema(real): %v", err)
	}
	def := DefaultSchema()
	if len(schema.RequiredSections) != len(def.RequiredSections) {
		t.Fatalf("real schema and DefaultSchema diverged on RequiredSections: %v vs %v", schema.RequiredSections, def.RequiredSections)
	}
	for i, want := range def.RequiredSections {
		if schema.RequiredSections[i] != want {
			t.Fatalf("RequiredSections[%d]: real=%q default=%q", i, schema.RequiredSections[i], want)
		}
	}
	for i, want := range def.StatusStates {
		if i >= len(schema.StatusStates) || schema.StatusStates[i] != want {
			t.Fatalf("StatusStates[%d]: real=%v default=%q", i, schema.StatusStates, want)
		}
	}
}
