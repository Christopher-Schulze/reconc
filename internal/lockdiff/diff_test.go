package lockdiff

import (
	"os"
	"path/filepath"
	"testing"
)

func writeLock(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
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
	writeLock(t, filepath.Join(dir, "a.json"), `{not json`)
	writeLock(t, filepath.Join(dir, "b.json"), `{"rules":[]}`)
	_, err := Diff(filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json"))
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}
