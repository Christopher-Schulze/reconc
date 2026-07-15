package schema_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/schema"
)

func TestPublicSchemaAliasesHaveOneOwner(t *testing.T) {
	if compiler.DefaultLockfileSchema != schema.PolicyLockURL {
		t.Fatalf("compiler lock schema drifted: %s", compiler.DefaultLockfileSchema)
	}
	if runtime.DefaultCheckReportSchema != schema.PolicyReportURL {
		t.Fatalf("runtime report schema drifted: %s", runtime.DefaultCheckReportSchema)
	}
	if runtime.DefaultFixPlanSchema != schema.PolicyFixPlanURL {
		t.Fatalf("runtime fix-plan schema drifted: %s", runtime.DefaultFixPlanSchema)
	}
}

func TestResolveEnterpriseSchemaBase(t *testing.T) {
	t.Setenv("RECONC_SCHEMA_BASE_URL", "https://schemas.example.test/")
	if got, want := schema.Resolve(schema.PolicyLock), "https://schemas.example.test/schemas/policy-lock/v1"; got != want {
		t.Fatalf("Resolve() = %q, want %q", got, want)
	}
}

func TestPublishedSchemasAreVersionedJSONContracts(t *testing.T) {
	contracts := map[string]string{
		"policy-config.schema.json":   schema.PolicyConfigURL,
		"policy-lock.schema.json":     schema.PolicyLockURL,
		"policy-report.schema.json":   schema.PolicyReportURL,
		"policy-fix-plan.schema.json": schema.PolicyFixPlanURL,
	}
	root := filepath.Join("..", "..", "schemas", "v1")
	paths, err := filepath.Glob(filepath.Join(root, "*.schema.json"))
	if err != nil {
		t.Fatalf("glob schemas: %v", err)
	}
	if len(paths) != len(contracts) {
		t.Fatalf("published schema count = %d, want %d", len(paths), len(contracts))
	}
	for name, wantID := range contracts {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var document map[string]interface{}
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if got := document["$id"]; got != wantID {
			t.Fatalf("%s $id = %v, want %s", name, got, wantID)
		}
		if got := document["$schema"]; got != "https://json-schema.org/draft/2020-12/schema" {
			t.Fatalf("%s draft = %v", name, got)
		}
		if got := document["type"]; got != "object" {
			t.Fatalf("%s root type = %v", name, got)
		}
	}
}

func TestPublishedSchemaPropertiesMatchEmittedGoTypes(t *testing.T) {
	lock := readSchemaDocument(t, "policy-lock.schema.json")
	report := readSchemaDocument(t, "policy-report.schema.json")
	fixPlan := readSchemaDocument(t, "policy-fix-plan.schema.json")

	assertPropertiesMatch(t, schemaDefinition(t, lock, "discovery"), ingest.DiscoveryResult{})
	assertPropertiesMatch(t, schemaDefinition(t, lock, "source"), policy.PolicySource{})
	assertPropertiesMatch(t, schemaDefinition(t, lock, "requiredFile"), policy.RequiredFile{})
	assertPropertiesMatch(t, schemaDefinition(t, lock, "evidence"), policy.EvidenceCheck{})
	assertPropertiesMatch(t, schemaDefinition(t, lock, "check"), policy.Check{})
	assertPropertiesMatch(t, schemaDefinition(t, lock, "assuranceExemption"), policy.AssuranceExemption{})
	assertPropertiesMatch(t, schemaDefinition(t, lock, "assurance"), policy.AssuranceGate{})
	assertPropertiesMatch(t, schemaDefinition(t, lock, "rule"), policy.Rule{})

	assertPropertiesMatch(t, schemaRootProperties(t, report), runtime.CheckReport{})
	assertPropertiesMatch(t, schemaDefinition(t, report, "commandResult"), runtime.CommandResult{})
	assertPropertiesMatch(t, schemaDefinition(t, report, "inputs"), runtime.ExecutionInputs{})
	assertPropertiesMatch(t, schemaDefinition(t, report, "violation"), runtime.Violation{})

	assertPropertiesMatch(t, schemaRootProperties(t, fixPlan), runtime.FixPlan{})
	assertPropertiesMatch(t, schemaDefinition(t, fixPlan, "remediation"), runtime.Remediation{})
}

func TestPublishedAssuranceEnumMatchesPolicyKinds(t *testing.T) {
	lock := readSchemaDocument(t, "policy-lock.schema.json")
	definitions := lock["$defs"].(map[string]interface{})
	assurance := definitions["assurance"].(map[string]interface{})
	properties := assurance["properties"].(map[string]interface{})
	typeSchema := properties["type"].(map[string]interface{})
	raw := typeSchema["enum"].([]interface{})
	got := make([]string, len(raw))
	for i, value := range raw {
		got[i] = value.(string)
	}
	wantKinds := policy.AllAssuranceKinds()
	want := make([]string, len(wantKinds))
	for i, kind := range wantKinds {
		want[i] = string(kind)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("published assurance enum = %v, want %v", got, want)
	}
}

func readSchemaDocument(t *testing.T, name string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "v1", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var document map[string]interface{}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return document
}

func schemaRootProperties(t *testing.T, document map[string]interface{}) map[string]interface{} {
	t.Helper()
	properties, ok := document["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("schema root has no properties object")
	}
	return properties
}

func schemaDefinition(t *testing.T, document map[string]interface{}, name string) map[string]interface{} {
	t.Helper()
	definitions, ok := document["$defs"].(map[string]interface{})
	if !ok {
		t.Fatal("schema has no $defs object")
	}
	definition, ok := definitions[name].(map[string]interface{})
	if !ok {
		t.Fatalf("schema has no %q definition", name)
	}
	properties, ok := definition["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("schema definition %q has no properties object", name)
	}
	return properties
}

func assertPropertiesMatch(t *testing.T, properties map[string]interface{}, value interface{}) {
	t.Helper()
	typeOf := reflect.TypeOf(value)
	want := make([]string, 0, typeOf.NumField())
	for i := 0; i < typeOf.NumField(); i++ {
		tag := strings.Split(typeOf.Field(i).Tag.Get("json"), ",")[0]
		if tag != "" && tag != "-" {
			want = append(want, tag)
		}
	}
	got := make([]string, 0, len(properties))
	for name := range properties {
		got = append(got, name)
	}
	sort.Strings(want)
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("schema properties for %T = %v, want %v", value, got, want)
	}
}
