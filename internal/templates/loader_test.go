package templates

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveBuiltin(t *testing.T) {
	tmpl, err := Resolve("tests-follow-source")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if tmpl.Source != SourceBuiltin {
		t.Errorf("expected builtin source, got %s", tmpl.Source)
	}
	if tmpl.Body["kind"] != "couple_change" {
		t.Errorf("expected kind=couple_change, got %v", tmpl.Body["kind"])
	}
	if tmpl.Description == "" {
		t.Error("description should be populated from YAML")
	}
}

func TestResolveNotFound(t *testing.T) {
	_, err := Resolve("bogus-template-name")
	if err == nil {
		t.Fatal("expected ErrNotFound")
	}
	if _, ok := err.(*ErrNotFound); !ok {
		t.Errorf("expected *ErrNotFound, got %T", err)
	}
}

func TestResolveEmptyName(t *testing.T) {
	_, err := Resolve("")
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestTemplateNamesRejectTraversalAndNonCanonicalSpelling(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	for _, name := range []string{"../secret", "nested/name", "name.yml", "Uppercase", "-leading"} {
		t.Run(name, func(t *testing.T) {
			if _, err := Resolve(name); err == nil {
				t.Fatalf("unsafe template name %q was accepted", name)
			}
		})
	}
}

func TestResolveUserOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RECONC_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	override := filepath.Join(home, "templates", "tests-follow-source.yml")
	if err := os.WriteFile(override, []byte("description: custom\nkind: deny_write\nmode: block\nmessage: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tmpl, err := Resolve("tests-follow-source")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if tmpl.Source != SourceUser {
		t.Errorf("expected user source, got %s", tmpl.Source)
	}
	if tmpl.Body["kind"] != "deny_write" {
		t.Errorf("user override should win; got kind=%v", tmpl.Body["kind"])
	}
}

func TestResolveUsesDefaultReconcHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("RECONC_HOME", "")
	templateDir := filepath.Join(home, ".reconc", "templates")
	if err := os.MkdirAll(templateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templateDir, "default-home.yml"), []byte("description: custom\nkind: deny_write\nmode: block\nmessage: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tmpl, err := Resolve("default-home")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if tmpl.Source != SourceUser {
		t.Fatalf("default-home source = %s, want user", tmpl.Source)
	}
}

func TestListReturnsAllBuiltins(t *testing.T) {
	// Ensure no RECONC_HOME leaks user templates into this test.
	t.Setenv("RECONC_HOME", t.TempDir())
	list, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) < 8 {
		t.Errorf("expected at least 8 builtin templates, got %d: %v", len(list), list)
	}
	names := map[string]bool{}
	for _, t := range list {
		names[t.Name] = true
	}
	for _, want := range []string{
		"authority-change-approval",
		"ci-green-before-merge",
		"custom-gate-on-change",
		"docs-follow-code",
		"local-secret-state-read-only",
		"no-generated-writes",
		"tests-follow-source",
		"verified-change",
	} {
		if !names[want] {
			t.Errorf("expected template %q in list", want)
		}
	}
}

func TestListMergesUserAndBuiltin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RECONC_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(home, "templates", "my-custom.yml")
	if err := os.WriteFile(custom, []byte("description: mine\nkind: deny_write\nmode: block\nmessage: x\npaths: ['x']\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, tmpl := range list {
		if tmpl.Name == "my-custom" {
			found = true
			if tmpl.Source != SourceUser {
				t.Errorf("my-custom should be user-source, got %s", tmpl.Source)
			}
		}
	}
	if !found {
		t.Error("user template my-custom not in List() output")
	}
}

func TestListFailsClosedOnMalformedUserTemplate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RECONC_HOME", home)
	templateDir := filepath.Join(home, "templates")
	if err := os.MkdirAll(templateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templateDir, "broken.yml"), []byte("kind: ["), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := List(); err == nil {
		t.Fatal("malformed user template was silently omitted")
	}
}

func TestApplyUserFieldsWin(t *testing.T) {
	tmpl := &Template{
		Body: map[string]interface{}{
			"kind":    "couple_change",
			"mode":    "warn",
			"message": "default",
		},
	}
	user := map[string]interface{}{
		"id":    "my-rule",
		"mode":  "block", // override
		"paths": []interface{}{"src/**"},
	}
	merged := Apply(tmpl, user)
	if merged["kind"] != "couple_change" {
		t.Errorf("template kind should be inherited; got %v", merged["kind"])
	}
	if merged["mode"] != "block" {
		t.Errorf("user mode should win; got %v", merged["mode"])
	}
	if merged["message"] != "default" {
		t.Errorf("template message should be inherited; got %v", merged["message"])
	}
	if merged["id"] != "my-rule" {
		t.Errorf("user id should be present; got %v", merged["id"])
	}
	if _, hasTmpl := merged["template"]; hasTmpl {
		t.Errorf("template: field should be stripped from merged output")
	}
}

func TestApplyStripsTemplateMetadata(t *testing.T) {
	tmpl := &Template{
		Body: map[string]interface{}{
			"description": "should not leak",
			"kind":        "deny_write",
		},
	}
	user := map[string]interface{}{"id": "r1", "paths": []interface{}{"x"}, "mode": "warn", "message": "x"}
	merged := Apply(tmpl, user)
	if _, ok := merged["description"]; ok {
		t.Errorf("description should be stripped from merged rule output")
	}
}

func TestAllBuiltinsAreWellFormed(t *testing.T) {
	// Every builtin must have at least kind, mode, message after merge
	// (some may require user to supply additional fields like paths).
	t.Setenv("RECONC_HOME", t.TempDir())
	list, err := List()
	if err != nil {
		t.Fatal(err)
	}
	for _, tmpl := range list {
		if tmpl.Body["kind"] == nil {
			t.Errorf("template %s missing kind", tmpl.Name)
		}
		if tmpl.Body["mode"] == nil {
			t.Errorf("template %s missing mode", tmpl.Name)
		}
		if tmpl.Body["message"] == nil {
			t.Errorf("template %s missing message", tmpl.Name)
		}
	}
}
