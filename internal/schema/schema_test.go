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
	"reconc.dev/reconc/internal/completiongate"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/proofbundle"
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
	if got, want := schema.Resolve(schema.PolicyLock), "https://schemas.example.test/schemas/policy-lock/v2"; got != want {
		t.Fatalf("Resolve() = %q, want %q", got, want)
	}
	if got, want := schema.Resolve(schema.PolicyReport), "https://schemas.example.test/schemas/policy-report/v1"; got != want {
		t.Fatalf("Resolve(report) = %q, want %q", got, want)
	}
}

func TestPublishedSchemasAreVersionedJSONContracts(t *testing.T) {
	contracts := map[string]string{
		"policy-config.schema.json":     schema.PolicyConfigURL,
		"completion-report.schema.json": schema.CompletionReportURL,
		"policy-lock.schema.json":       schema.LegacyPolicyLockURL,
		"policy-report.schema.json":     schema.PolicyReportURL,
		"policy-fix-plan.schema.json":   schema.PolicyFixPlanURL,
		"proof-bundle.schema.json":      schema.ProofBundleURL,
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
	assertPublishedSchema(t, filepath.Join("..", "..", "schemas", "v2", "policy-lock.schema.json"), schema.PolicyLockURL)
}

func assertPublishedSchema(t *testing.T, path, wantID string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var document map[string]interface{}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if got := document["$id"]; got != wantID {
		t.Fatalf("%s $id = %v, want %s", path, got, wantID)
	}
	if got := document["$schema"]; got != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("%s draft = %v", path, got)
	}
	if got := document["type"]; got != "object" {
		t.Fatalf("%s root type = %v", path, got)
	}
}

func TestPublishedSchemaPropertiesMatchEmittedGoTypes(t *testing.T) {
	lock := readLegacyLockSchemaDocument(t)
	report := readSchemaDocument(t, "policy-report.schema.json")
	fixPlan := readSchemaDocument(t, "policy-fix-plan.schema.json")
	completion := readSchemaDocument(t, "completion-report.schema.json")
	proof := readSchemaDocument(t, "proof-bundle.schema.json")

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

	assertPropertiesMatch(t, schemaRootProperties(t, completion), completiongate.Report{})
	assertPropertiesMatch(t, schemaDefinition(t, completion, "check"), completiongate.Check{})
	assertPropertiesMatch(t, schemaDefinition(t, completion, "candidate"), completiongate.CandidateBinding{})

	assertPropertiesMatch(t, schemaRootProperties(t, proof), proofbundle.Bundle{})
	assertPropertiesMatch(t, schemaDefinition(t, proof, "build"), proofbundle.Build{})
	assertPropertiesMatch(t, schemaDefinition(t, proof, "task"), proofbundle.Task{})
	assertPropertiesMatch(t, schemaDefinition(t, proof, "candidate"), proofbundle.Candidate{})
	assertPropertiesMatch(t, schemaDefinition(t, proof, "check"), proofbundle.Check{})
	assertPropertiesMatch(t, schemaDefinition(t, proof, "commandProof"), proofbundle.CommandProof{})
	assertPropertiesMatch(t, schemaDefinition(t, proof, "evidence"), proofbundle.Evidence{})
	assertPropertiesMatch(t, schemaDefinition(t, proof, "violation"), proofbundle.Violation{})
	assertPropertiesMatch(t, schemaDefinition(t, proof, "supersededBlock"), proofbundle.SupersededBlock{})
}

func TestPublishedAssuranceEnumMatchesPolicyKinds(t *testing.T) {
	lock := readLegacyLockSchemaDocument(t)
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

func TestCurrentLockSchemaRequiresPortableIdentity(t *testing.T) {
	lock := readSchemaDocument(t, "policy-lock.schema.json")
	properties := schemaRootProperties(t, lock)
	if got := properties["format_version"].(map[string]interface{})["const"]; got != "2" {
		t.Fatalf("format_version const = %v, want 2", got)
	}
	if got := properties["repo_root"].(map[string]interface{})["const"]; got != "." {
		t.Fatalf("repo_root const = %v, want .", got)
	}
	discovery := properties["discovery"].(map[string]interface{})
	allOf := discovery["allOf"].([]interface{})
	portable := allOf[1].(map[string]interface{})["properties"].(map[string]interface{})
	for _, field := range []string{"repo_root", "start_path"} {
		if got := portable[field].(map[string]interface{})["const"]; got != "." {
			t.Fatalf("discovery.%s const = %v, want .", field, got)
		}
	}
}

func readLegacyLockSchemaDocument(t *testing.T) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "v1", "policy-lock.schema.json"))
	if err != nil {
		t.Fatalf("read legacy lock schema: %v", err)
	}
	var document map[string]interface{}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse legacy lock schema: %v", err)
	}
	return document
}

func readSchemaDocument(t *testing.T, name string) map[string]interface{} {
	t.Helper()
	version := "v1"
	if name == "policy-lock.schema.json" {
		version = "v2"
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", version, name))
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
