package schema_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/schema"
)

func TestCurrentPolicyConfigSchemaAcceptsOnlyTheImplementedActionSurface(t *testing.T) {
	compiled := compileRegisteredSchemas(t)
	contract, ok := schema.CurrentContract(schema.PolicyConfig)
	if !ok {
		t.Fatal("current policy-config contract is absent")
	}
	definition := compiled[contract.DefaultURL]
	valid := map[string]interface{}{
		"actions": map[string]interface{}{
			"tools": []interface{}{map[string]interface{}{
				"id": "warehouse-query", "transport": "mcp_stdio", "server_label": "warehouse",
				"tool": "query", "effect": map[string]interface{}{"kind": "external"},
				"cost_units": 3, "max_result_bytes": 4096,
			}},
			"rules": []interface{}{map[string]interface{}{
				"id": "block-production", "decision": "block",
				"selector": map[string]interface{}{"tool_ids": []interface{}{"warehouse-query"}, "phases": []interface{}{"pre_call"}},
				"when": map[string]interface{}{"predicate": map[string]interface{}{
					"source": "arguments", "pointer": "/database", "op": "eq", "value": "production",
				}},
			}},
			"budgets": []interface{}{map[string]interface{}{
				"id":       "query-run-cap",
				"selector": map[string]interface{}{"tool_ids": []interface{}{"warehouse-query"}},
				"limits":   map[string]interface{}{"call_count": 10, "result_bytes": 40960, "cost_units": 30},
				"reset":    "operator_run", "on_exhaustion": "block",
			}},
			"defaults": map[string]interface{}{"gateway_unmatched": "block", "host_unmatched": "allow"},
		},
	}
	if err := definition.Validate(valid); err != nil {
		t.Fatalf("valid action authoring rejected: %v", err)
	}
	for _, mutate := range []func(map[string]interface{}){
		func(candidate map[string]interface{}) {
			candidate["actions"].(map[string]interface{})["approvals"] = []interface{}{}
		},
		func(candidate map[string]interface{}) {
			candidate["actions"].(map[string]interface{})["budgets"].([]interface{})[0].(map[string]interface{})["limits"] = map[string]interface{}{}
		},
		func(candidate map[string]interface{}) {
			candidate["actions"].(map[string]interface{})["rules"].([]interface{})[0].(map[string]interface{})["selector"] = map[string]interface{}{"tools": []interface{}{}}
		},
		func(candidate map[string]interface{}) {
			candidate["actions"].(map[string]interface{})["tools"].([]interface{})[0].(map[string]interface{})["effect"] = map[string]interface{}{"kind": "repository_write"}
		},
		func(candidate map[string]interface{}) {
			candidate["actions"].(map[string]interface{})["rules"].([]interface{})[0].(map[string]interface{})["when"].(map[string]interface{})["predicate"].(map[string]interface{})["op"] = "in"
			candidate["actions"].(map[string]interface{})["rules"].([]interface{})[0].(map[string]interface{})["when"].(map[string]interface{})["predicate"].(map[string]interface{})["value"] = []interface{}{"production", true}
		},
		func(candidate map[string]interface{}) {
			candidate["actions"].(map[string]interface{})["budgets"].([]interface{})[0].(map[string]interface{})["limits"] = map[string]interface{}{"call_count": 0}
		},
		func(candidate map[string]interface{}) {
			candidate["actions"].(map[string]interface{})["budgets"].([]interface{})[0].(map[string]interface{})["limits"] = map[string]interface{}{"concurrent": 5}
		},
		func(candidate map[string]interface{}) {
			candidate["actions"].(map[string]interface{})["budgets"].([]interface{})[0].(map[string]interface{})["limits"] = map[string]interface{}{"rate_window": 1}
		},
		func(candidate map[string]interface{}) {
			candidate["actions"].(map[string]interface{})["budgets"].([]interface{})[0].(map[string]interface{})["window_seconds"] = 60
		},
		func(candidate map[string]interface{}) {
			candidate["actions"].(map[string]interface{})["budgets"].([]interface{})[0].(map[string]interface{})["limits"] = map[string]interface{}{"calls": 1}
		},
	} {
		candidate := cloneJSONValue(t, valid).(map[string]interface{})
		mutate(candidate)
		if err := definition.Validate(candidate); err == nil {
			t.Fatalf("policy-config schema accepted unsafe action mutation: %s", mustJSON(t, candidate))
		}
	}
}

func TestCompilerEmitsOneSchemaValidFormat5ActionPlan(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := `mcp:
  unclassified: deny
  tools:
    - platform: cursor
      tool: write
      effect: repository_write
      path_fields: [/path]
actions:
  rules:
    - id: warn-write
      selector:
        tools: [write]
      decision: warn
`
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatalf("compile policy: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(repo, compiler.LockfileRelativePath))
	if err != nil {
		t.Fatal(err)
	}
	var lock map[string]interface{}
	if err := json.Unmarshal(body, &lock); err != nil {
		t.Fatal(err)
	}
	if _, parallel := lock["mcp"]; parallel {
		t.Fatal("format-5 lock emitted a parallel MCP plan")
	}
	compiled := compileRegisteredSchemas(t)
	contract, _ := schema.CurrentContract(schema.PolicyLock)
	if err := compiled[contract.DefaultURL].Validate(lock); err != nil {
		t.Fatalf("real compiled format-5 lock is schema-invalid: %v", err)
	}
}
