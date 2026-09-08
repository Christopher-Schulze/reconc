package schema_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/schema"
)

func TestTemplateProvenanceSchemaMatchesCompiler(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte("rules:\n  - id: generated\n    template: no-generated-writes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, data, err := compiler.RenderRepoPolicy(repo, "test")
	if err != nil {
		t.Fatal(err)
	}
	var lock map[string]interface{}
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatal(err)
	}
	compiled := compileRegisteredSchemas(t)
	contract, ok := schema.CurrentContract(schema.PolicyLock)
	if !ok {
		t.Fatal("current policy lock schema missing")
	}
	definition := compiled[contract.DefaultURL]
	if err := definition.Validate(lock); err != nil {
		t.Fatalf("compiled template provenance is invalid: %v", err)
	}
	for _, value := range []interface{}{nil, []interface{}{}, []interface{}{map[string]interface{}{"name": "bad_name", "source": "user", "content_sha256": "bad"}}} {
		lock["template_dependencies"] = value
		if err := definition.Validate(lock); err == nil {
			t.Fatalf("invalid template dependencies accepted: %#v", value)
		}
	}
}
