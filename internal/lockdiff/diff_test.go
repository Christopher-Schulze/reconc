package lockdiff

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/schema"
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

func TestDiffClassifiesEveryTopLevelField(t *testing.T) {
	base := minimalPayload()
	classes := map[string]FieldClass{
		"$schema":           FieldClassSemantic,
		"compiler_version":  FieldClassProvenance,
		"format_version":    FieldClassSemantic,
		"repo_root":         FieldClassGenerated,
		"default_mode":      FieldClassSemantic,
		"rule_count":        FieldClassGenerated,
		"source_count":      FieldClassGenerated,
		"source_digest":     FieldClassProvenance,
		"lock_digest":       FieldClassGenerated,
		"source_precedence": FieldClassSemantic,
		"discovery":         FieldClassProvenance,
		"sources":           FieldClassProvenance,
		"rules":             FieldClassSemantic,
		"actions":           FieldClassSemantic,
		"custom_runtimes":   FieldClassSemantic,
	}
	changed := clonePayload(base)
	changed["unsupported_field"] = "visible"
	report, err := diffMaps("a", "b", base, changed)
	if err != nil {
		t.Fatalf("diffMaps: %v", err)
	}
	got := map[string]FieldClass{}
	for _, field := range report.FieldClasses {
		got[field.Field] = field.Class
	}
	classes["unsupported_field"] = FieldClassUnsupported
	if len(got) != len(classes) {
		t.Fatalf("field classes = %#v, want %#v", got, classes)
	}
	for field, want := range classes {
		if got[field] != want {
			t.Errorf("field %q class = %q, want %q", field, got[field], want)
		}
	}

	for field := range classes {
		if field == "rules" || field == "sources" {
			continue
		}
		candidate := clonePayload(base)
		candidate[field] = changedEnvelopeValue(field)
		if field == "rule_count" {
			candidate["rules"] = []interface{}{map[string]interface{}{"id": "changed", "kind": "deny_write"}}
		}
		if field == "source_count" {
			candidate["sources"] = []interface{}{sourceValue("policy", "policies/changed.yml", strings.Repeat("e", 64), "", 0)}
		}
		fieldReport, err := diffMaps("a", "b", base, candidate)
		if err != nil {
			t.Fatalf("field %q diffMaps: %v", field, err)
		}
		if len(fieldReport.EnvelopeChanges) != 1 || fieldReport.EnvelopeChanges[0].Field != field {
			t.Errorf("field %q envelope changes = %#v", field, fieldReport.EnvelopeChanges)
		}
	}
}

func TestDiffReportsSourceInventoryMovesAddsRemovalsAndOrder(t *testing.T) {
	digestA := strings.Repeat("a", 64)
	digestB := strings.Repeat("b", 64)
	digestC := strings.Repeat("c", 64)
	a := minimalPayload()
	a["sources"] = []interface{}{
		sourceValue("policy", "policies/a.yml", digestA, "block-a", 3),
		sourceValue("policy", "policies/b.yml", digestB, "", 0),
	}
	a["source_count"] = 2
	b := minimalPayload()
	b["sources"] = []interface{}{
		sourceValue("policy", "policies/moved.yml", digestA, "block-a", 3),
		sourceValue("policy", "policies/new.yml", digestC, "", 0),
	}
	b["source_count"] = 2
	report, err := diffMaps("a", "b", a, b)
	if err != nil {
		t.Fatalf("diffMaps: %v", err)
	}
	if len(report.SourceChanges) != 3 {
		t.Fatalf("source changes = %#v, want move/add/remove", report.SourceChanges)
	}
	wants := []string{"added", "moved", "removed"}
	for index, want := range wants {
		if report.SourceChanges[index].Change != want {
			t.Errorf("source change %d = %q, want %q", index, report.SourceChanges[index].Change, want)
		}
	}
	if !report.SourceOrderDiff || len(report.SourceOrderA) != 2 || len(report.SourceOrderB) != 2 {
		t.Fatalf("source order change = %t, A=%v, B=%v", report.SourceOrderDiff, report.SourceOrderA, report.SourceOrderB)
	}
	if report.IsEmpty() {
		t.Fatal("source inventory changes were reported as empty")
	}
}

func TestDiffReportsSourceContentChangeAtStableLocation(t *testing.T) {
	a := minimalPayload()
	a["sources"] = []interface{}{sourceValue("policy", "policies/rules.yml", strings.Repeat("a", 64), "", 0)}
	a["source_count"] = 1
	b := minimalPayload()
	b["sources"] = []interface{}{sourceValue("policy", "policies/rules.yml", strings.Repeat("b", 64), "", 0)}
	b["source_count"] = 1
	report, err := diffMaps("a", "b", a, b)
	if err != nil {
		t.Fatalf("diffMaps: %v", err)
	}
	if len(report.SourceChanges) != 1 || report.SourceChanges[0].Change != "changed" {
		t.Fatalf("source changes = %#v, want one changed source", report.SourceChanges)
	}
}

func TestDiffDoesNotTreatSourceOrderAsASet(t *testing.T) {
	a := minimalPayload()
	first := sourceValue("policy", "policies/a.yml", strings.Repeat("a", 64), "", 0)
	second := sourceValue("policy", "policies/b.yml", strings.Repeat("b", 64), "", 0)
	a["sources"] = []interface{}{first, second}
	a["source_count"] = 2
	b := minimalPayload()
	b["sources"] = []interface{}{second, first}
	b["source_count"] = 2
	report, err := diffMaps("a", "b", a, b)
	if err != nil {
		t.Fatalf("diffMaps: %v", err)
	}
	if len(report.SourceChanges) != 0 || !report.SourceOrderDiff || report.IsEmpty() {
		t.Fatalf("source order was not preserved: %#v", report)
	}
}

func TestDiffReportsPureRuleProvenanceMove(t *testing.T) {
	a := minimalPayload()
	a["rules"] = []interface{}{map[string]interface{}{
		"id": "r1", "kind": "deny_write", "mode": "warn", "source_path": "policies/a.yml", "source_block_id": "a",
	}}
	a["rule_count"] = 1
	b := clonePayload(a)
	b["rules"] = []interface{}{map[string]interface{}{
		"id": "r1", "kind": "deny_write", "mode": "warn", "source_path": "policies/b.yml", "source_block_id": "b",
	}}
	report, err := diffMaps("a", "b", a, b)
	if err != nil {
		t.Fatalf("diffMaps: %v", err)
	}
	if len(report.Changed) != 0 || len(report.RuleProvenance) != 1 {
		t.Fatalf("rule provenance = changed=%v provenance=%#v", report.Changed, report.RuleProvenance)
	}
	if report.RuleProvenance[0].FieldsChanged[0] != "source_block_id" || report.RuleProvenance[0].FieldsChanged[1] != "source_path" {
		t.Fatalf("provenance fields = %v", report.RuleProvenance[0].FieldsChanged)
	}
	if report.IsEmpty() {
		t.Fatal("rule provenance move was reported as empty")
	}
}

func TestDiffMigratesLegacyV5BeforeComparison(t *testing.T) {
	v5 := minimalPayload()
	v5["$schema"] = schema.LegacyPolicyLockV5URL
	v5["format_version"] = "5"
	v5["actions"] = map[string]interface{}{
		"format_version": "1", "tools": []interface{}{}, "rules": []interface{}{},
		"budgets": []interface{}{}, "approvals": []interface{}{}, "detectors": []interface{}{},
		"defaults": map[string]interface{}{},
	}
	v5Digest, err := compiler.ComputeLockDigest(v5)
	if err != nil {
		t.Fatalf("compute v5 digest: %v", err)
	}
	v5["lock_digest"] = v5Digest
	current, _, err := compiler.MigrateLockfile(clonePayload(v5))
	if err != nil {
		t.Fatalf("migrate v5: %v", err)
	}
	legacyPath, currentPath := filepath.Join(t.TempDir(), "v5.json"), filepath.Join(t.TempDir(), "v6.json")
	writeRawLock(t, legacyPath, v5)
	writeRawLock(t, currentPath, current)
	report, err := Diff(legacyPath, currentPath)
	if err != nil {
		t.Fatalf("Diff migrated/current: %v", err)
	}
	if !report.IsEmpty() {
		t.Fatalf("migration-only comparison is not empty: %#v", report)
	}
}

func minimalPayload() map[string]interface{} {
	return map[string]interface{}{
		"$schema": compiler.LockfileSchema(), "compiler_version": "test", "format_version": compiler.LockfileFormatVersion,
		"repo_root": compiler.PortableRepoRoot, "default_mode": "warn", "rule_count": 0, "source_count": 0,
		"source_digest": strings.Repeat("0", 64), "lock_digest": strings.Repeat("1", 64),
		"source_precedence": []interface{}{}, "discovery": map[string]interface{}{
			"repo_root": ".", "start_path": ".", "discovered": true,
			"config_candidates": []interface{}{}, "policy_paths": []interface{}{}, "warnings": []interface{}{},
		}, "sources": []interface{}{},
		"rules": []interface{}{}, "actions": map[string]interface{}{}, "custom_runtimes": []interface{}{},
	}
}

func clonePayload(input map[string]interface{}) map[string]interface{} {
	output := make(map[string]interface{}, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func changedEnvelopeValue(field string) interface{} {
	if field == "default_mode" {
		return "block"
	}
	if field == "rule_count" || field == "source_count" {
		return 1
	}
	if field == "custom_runtimes" || field == "source_precedence" {
		return []interface{}{"changed"}
	}
	if field == "discovery" || field == "actions" {
		return map[string]interface{}{"changed": true}
	}
	return "changed"
}

func sourceValue(kind, path, digest, blockID string, lineStart int) map[string]interface{} {
	value := map[string]interface{}{"kind": kind, "path": path, "content_sha256": digest}
	if blockID != "" {
		value["block_id"] = blockID
	}
	if lineStart != 0 {
		value["line_start"] = lineStart
	}
	return value
}

func writeRawLock(t *testing.T, path string, payload map[string]interface{}) {
	t.Helper()
	digest, err := compiler.ComputeLockDigest(payload)
	if err != nil {
		t.Fatalf("compute raw lock digest: %v", err)
	}
	payload["lock_digest"] = digest
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal raw lock: %v", err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write raw lock: %v", err)
	}
}
