package lockdiff

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

func writeLock(t *testing.T, path, body string) {
	t.Helper()
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	payload["$schema"] = compiler.LockfileSchema()
	payload["format_version"] = compiler.LockfileFormatVersion
	payload["repo_root"] = compiler.PortableRepoRoot
	payload["discovery"] = map[string]interface{}{
		"repo_root":  compiler.PortableRepoRoot,
		"start_path": compiler.PortableRepoRoot,
	}
	if _, ok := payload["sources"]; !ok {
		payload["sources"] = []interface{}{}
	}
	if _, ok := payload["rules"]; !ok {
		payload["rules"] = []interface{}{}
	}
	payload["source_count"] = len(payload["sources"].([]interface{}))
	payload["rule_count"] = len(payload["rules"].([]interface{}))
	if _, ok := payload["default_mode"]; !ok {
		payload["default_mode"] = "warn"
	}
	if _, ok := payload["source_digest"]; !ok {
		payload["source_digest"] = strings.Repeat("0", 64)
	}
	delete(payload, "lock_digest")
	digest, err := compiler.ComputeLockDigest(payload)
	if err != nil {
		t.Fatalf("compute fixture digest: %v", err)
	}
	payload["lock_digest"] = digest
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiffIdenticalLocksReturnEmpty(t *testing.T) {
	dir := t.TempDir()
	body := `{"default_mode":"warn","source_digest":"d1","rules":[{"id":"r1","kind":"deny_write","mode":"warn","paths":["x"]}]}`
	writeLock(t, filepath.Join(dir, "a.json"), body)
	writeLock(t, filepath.Join(dir, "b.json"), body)

	r, err := Diff(filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json"))
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !r.IsEmpty() {
		t.Errorf("expected empty diff, got %+v", r)
	}
	if r.Unchanged != 1 {
		t.Errorf("expected 1 unchanged rule, got %d", r.Unchanged)
	}
}

func TestDiffAddedRule(t *testing.T) {
	dir := t.TempDir()
	a := `{"rules":[{"id":"r1","kind":"deny_write","mode":"warn"}]}`
	b := `{"rules":[{"id":"r1","kind":"deny_write","mode":"warn"},{"id":"r2","kind":"deny_write","mode":"block"}]}`
	writeLock(t, filepath.Join(dir, "a.json"), a)
	writeLock(t, filepath.Join(dir, "b.json"), b)

	r, err := Diff(filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Added) != 1 || r.Added[0].ID != "r2" {
		t.Errorf("expected 1 added rule 'r2', got %+v", r.Added)
	}
	if len(r.Removed) != 0 || len(r.Changed) != 0 {
		t.Errorf("no removed/changed expected, got %+v", r)
	}
}

func TestDiffRemovedRule(t *testing.T) {
	dir := t.TempDir()
	a := `{"rules":[{"id":"r1","kind":"deny_write"},{"id":"r2","kind":"deny_write"}]}`
	b := `{"rules":[{"id":"r1","kind":"deny_write"}]}`
	writeLock(t, filepath.Join(dir, "a.json"), a)
	writeLock(t, filepath.Join(dir, "b.json"), b)

	r, _ := Diff(filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json"))
	if len(r.Removed) != 1 || r.Removed[0].ID != "r2" {
		t.Errorf("expected r2 removed, got %+v", r.Removed)
	}
}

func TestDiffChangedRule(t *testing.T) {
	dir := t.TempDir()
	a := `{"rules":[{"id":"r1","kind":"deny_write","mode":"warn","paths":["x"]}]}`
	b := `{"rules":[{"id":"r1","kind":"deny_write","mode":"block","paths":["x","y"]}]}`
	writeLock(t, filepath.Join(dir, "a.json"), a)
	writeLock(t, filepath.Join(dir, "b.json"), b)

	r, _ := Diff(filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json"))
	if len(r.Changed) != 1 {
		t.Fatalf("expected 1 changed, got %+v", r.Changed)
	}
	fields := r.Changed[0].FieldsChanged
	if len(fields) != 2 || fields[0] != "mode" || fields[1] != "paths" {
		t.Errorf("expected fields [mode paths], got %v", fields)
	}
}

func TestDiffIgnoresProvenanceFields(t *testing.T) {
	dir := t.TempDir()
	// Rules identical except source_path -- must NOT show as changed.
	a := `{"rules":[{"id":"r1","kind":"deny_write","mode":"warn","source_path":"a.md"}]}`
	b := `{"rules":[{"id":"r1","kind":"deny_write","mode":"warn","source_path":"b.md"}]}`
	writeLock(t, filepath.Join(dir, "a.json"), a)
	writeLock(t, filepath.Join(dir, "b.json"), b)

	r, _ := Diff(filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json"))
	if len(r.Changed) != 0 {
		t.Errorf("source_path drift should not register as changed; got %+v", r.Changed)
	}
}

func TestDiffDefaultModeChange(t *testing.T) {
	dir := t.TempDir()
	a := `{"default_mode":"warn","rules":[]}`
	b := `{"default_mode":"block","rules":[]}`
	writeLock(t, filepath.Join(dir, "a.json"), a)
	writeLock(t, filepath.Join(dir, "b.json"), b)

	r, _ := Diff(filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json"))
	if !r.DefaultModeDiff {
		t.Error("expected default_mode_changed=true")
	}
}

func TestDiffDeterministicOrdering(t *testing.T) {
	dir := t.TempDir()
	// Added rules in random order on the B side; result must be sorted.
	b := `{"rules":[{"id":"r-zeta","kind":"deny_write"},{"id":"r-alpha","kind":"deny_write"},{"id":"r-mu","kind":"deny_write"}]}`
	writeLock(t, filepath.Join(dir, "a.json"), `{"rules":[]}`)
	writeLock(t, filepath.Join(dir, "b.json"), b)

	r, _ := Diff(filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json"))
	if len(r.Added) != 3 {
		t.Fatalf("expected 3 added, got %d", len(r.Added))
	}
	if r.Added[0].ID != "r-alpha" || r.Added[1].ID != "r-mu" || r.Added[2].ID != "r-zeta" {
		t.Errorf("expected sorted [alpha mu zeta], got %+v", r.Added)
	}
}

func TestDiffMissingFile(t *testing.T) {
	dir := t.TempDir()
	writeLock(t, filepath.Join(dir, "a.json"), `{"rules":[]}`)
	_, err := Diff(filepath.Join(dir, "a.json"), filepath.Join(dir, "missing.json"))
	if err == nil {
		t.Fatal("expected error for missing lockfile")
	}
}

func TestDiffMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.json"), []byte(`{not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeLock(t, filepath.Join(dir, "b.json"), `{"rules":[]}`)
	_, err := Diff(filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json"))
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestDiffRejectsIncompleteLockfile(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.json")
	if err := os.WriteFile(a, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeLock(t, filepath.Join(dir, "b.json"), `{"rules":[]}`)
	if _, err := Diff(a, filepath.Join(dir, "b.json")); err == nil {
		t.Fatal("incomplete lockfile must fail validation")
	}
}

func TestDiffRejectsDuplicateRuleIDs(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.json")
	writeLock(t, a, `{"rules":[{"id":"dup","kind":"require_script"},{"id":"dup","kind":"require_script"}]}`)
	writeLock(t, filepath.Join(dir, "b.json"), `{"rules":[]}`)
	if _, err := Diff(a, filepath.Join(dir, "b.json")); err == nil || !strings.Contains(err.Error(), "duplicate rule id") {
		t.Fatalf("duplicate rule IDs must fail closed, got %v", err)
	}
}

func TestDiffPreservesArgumentOrder(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.json")
	b := filepath.Join(dir, "b.json")
	writeLock(t, a, `{"rules":[{"id":"r1","kind":"require_script","args":["first","second"]}]}`)
	writeLock(t, b, `{"rules":[{"id":"r1","kind":"require_script","args":["second","first"]}]}`)
	report, err := Diff(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Changed) != 1 || len(report.Changed[0].FieldsChanged) != 1 || report.Changed[0].FieldsChanged[0] != "args" {
		t.Fatalf("argument reordering must be semantic: %+v", report)
	}
}

func TestDiffIgnoresStringListReordering(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.json")
	b := filepath.Join(dir, "b.json")
	writeJSON := func(path, whenPaths string) {
		t.Helper()
		body := `{"default_mode":"warn","rules":[{"id":"r1","kind":"deny_write","when_paths":` + whenPaths + `}]}`
		writeLock(t, path, body)
	}
	writeJSON(a, `["src/**","docs/**"]`)
	writeJSON(b, `["docs/**","src/**"]`)
	report, err := Diff(a, b)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !report.IsEmpty() {
		t.Fatalf("reordered string lists must not report a change: %+v", report.Changed)
	}
}

func TestDiffIgnoresNestedSetListReordering(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.json")
	b := filepath.Join(dir, "b.json")
	writeJSON := func(path, mustContain string) {
		t.Helper()
		body := `{"default_mode":"warn","rules":[{"id":"r1","kind":"require_evidence","when_paths":["src/**"],"evidence":[{"file":"proof.md","must_contain":` + mustContain + `}]}]}`
		writeLock(t, path, body)
	}
	writeJSON(a, `["alpha","beta"]`)
	writeJSON(b, `["beta","alpha"]`)
	report, err := Diff(a, b)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !report.IsEmpty() {
		t.Fatalf("reordered nested must_contain must not report a change: %+v", report.Changed)
	}
}

func TestDiffReportsDigestOnlyChangeAsNonEmpty(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.json")
	b := filepath.Join(dir, "b.json")
	rules := `{"rules":[{"id":"r1","kind":"deny_write","mode":"warn","paths":["x"]}]}`
	writeLock(t, a, `{"source_digest":"`+strings.Repeat("a", 64)+`",`+rules[1:])
	writeLock(t, b, `{"source_digest":"`+strings.Repeat("b", 64)+`",`+rules[1:])
	report, err := Diff(a, b)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if report.IsEmpty() {
		t.Fatalf("a source digest change must not read as an empty diff: %+v", report)
	}
}
