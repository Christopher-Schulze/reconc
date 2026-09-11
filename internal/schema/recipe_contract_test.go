package schema_test

import (
	"encoding/json"
	"testing"

	"reconc.dev/reconc/internal/schema"
)

func TestRecipeContractSchemaAcceptsOnlyKnownScriptContracts(t *testing.T) {
	compiled := compileRegisteredSchemas(t)
	contract, ok := schema.CurrentContract(schema.PolicyLock)
	if !ok {
		t.Fatal("missing policy-lock contract")
	}
	definition := compiled[contract.DefaultURL]
	for _, test := range []struct {
		name, kind, recipe string
		valid              bool
	}{
		{"public API", "require_script", "public-api-compatibility", true},
		{"migration", "require_script", "schema-migration-safety", true},
		{"generator", "require_script", "generated-artifact-consistency", true},
		{"performance", "require_script", "performance-budget", true},
		{"unknown contract", "require_script", "unknown", false},
		{"non-script contract", "deny_write", "performance-budget", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			artifact := representativeArtifact(t, definition)
			artifact["rule_count"] = json.Number("1")
			rule := map[string]any{"id": "recipe", "kind": test.kind, "message": "verify recipe", "recipe_contract": test.recipe}
			if test.kind == "require_script" {
				rule["script"] = "scripts/check.sh"
				rule["when_paths"] = []any{"src/**"}
			} else {
				rule["paths"] = []any{"src/**"}
			}
			artifact["rules"] = []any{rule}
			if err := definition.Validate(artifact); (err == nil) != test.valid {
				t.Fatalf("schema validation = %v, want valid %v", err, test.valid)
			}
		})
	}
}
