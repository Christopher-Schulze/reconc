package schema_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/bootstrap"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/completiongate"
	"reconc.dev/reconc/internal/customruntime"
	"reconc.dev/reconc/internal/harnesspack"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/proofbundle"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/schema"
	"reconc.dev/reconc/internal/usercli"
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
	if got, want := schema.Resolve(schema.PolicyLock), "https://schemas.example.test/schemas/policy-lock/v5"; got != want {
		t.Fatalf("Resolve() = %q, want %q", got, want)
	}
	if got, want := schema.Resolve(schema.PolicyReport), "https://schemas.example.test/schemas/policy-report/v1"; got != want {
		t.Fatalf("Resolve(report) = %q, want %q", got, want)
	}
}

func TestDefaultURLAndResolveCoverEveryArtifact(t *testing.T) {
	t.Setenv("RECONC_SCHEMA_BASE_URL", "")
	for _, contract := range schema.Contracts() {
		if contract.State != schema.StateCurrent {
			continue
		}
		if got := schema.DefaultURL(contract.Artifact); got != contract.DefaultURL {
			t.Errorf("DefaultURL(%q) = %q, want %q", contract.Artifact, got, contract.DefaultURL)
		}
		if got := schema.Resolve(contract.Artifact); got != contract.DefaultURL {
			t.Errorf("Resolve(%q) without override = %q, want %q", contract.Artifact, got, contract.DefaultURL)
		}
	}
	if got := schema.DefaultURL(schema.Artifact("unknown")); got != "" {
		t.Fatalf("DefaultURL(unknown) = %q, want empty", got)
	}
}

func TestPublishedSchemasAreVersionedJSONContracts(t *testing.T) {
	for _, contract := range schema.Contracts() {
		data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(contract.LocalPath)))
		if err != nil {
			t.Fatalf("read %s: %v", contract.LocalPath, err)
		}
		var document map[string]interface{}
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatalf("parse %s: %v", contract.LocalPath, err)
		}
		if got := document["$id"]; got != contract.DefaultURL {
			t.Fatalf("%s $id = %v, want %s", contract.LocalPath, got, contract.DefaultURL)
		}
		if got := document["$schema"]; got != "https://json-schema.org/draft/2020-12/schema" {
			t.Fatalf("%s draft = %v", contract.LocalPath, got)
		}
		if got := document["type"]; got != "object" {
			t.Fatalf("%s root type = %v", contract.LocalPath, got)
		}
	}
}

func TestPublishedSchemaPropertiesMatchEmittedGoTypes(t *testing.T) {
	lock := readLegacyLockSchemaDocument(t)
	report := readCurrentSchemaDocument(t, schema.PolicyReport)
	fixPlan := readCurrentSchemaDocument(t, schema.PolicyFixPlan)
	completion := readCurrentSchemaDocument(t, schema.CompletionReport)
	proof := readCurrentSchemaDocument(t, schema.ProofBundle)
	installationReceipt := readCurrentSchemaDocument(t, schema.InstallationReceipt)
	globalDiagnostic := readCurrentSchemaDocument(t, schema.GlobalDiagnostic)
	globalLifecycle := readCurrentSchemaDocument(t, schema.GlobalLifecycle)
	harnessPackManifest := readCurrentSchemaDocument(t, schema.HarnessPackManifest)
	repositoryInstall := readCurrentSchemaDocument(t, schema.RepositoryInstall)
	repositorySyncPlan := readCurrentSchemaDocument(t, schema.RepositorySyncPlan)
	repositorySyncReport := readCurrentSchemaDocument(t, schema.RepositorySyncReport)
	releaseManifest := readCurrentSchemaDocument(t, schema.ReleaseManifest)
	customManifest := readCurrentSchemaDocument(t, schema.CustomRuntimeManifest)
	neutralRequest := readCurrentSchemaDocument(t, schema.NeutralHookRequest)
	neutralResponse := readCurrentSchemaDocument(t, schema.NeutralHookResponse)
	customLiveness := readCurrentSchemaDocument(t, schema.CustomRuntimeLiveness)
	customConformance := readCurrentSchemaDocument(t, schema.CustomRuntimeConformance)

	assertPropertiesMatch(t, schemaDefinition(t, lock, "discovery"), ingest.DiscoveryResult{})
	assertPropertiesMatch(t, schemaDefinition(t, lock, "source"), legacyPolicySource{})
	currentLock := readCurrentLockSchemaDocument(t)
	assertPropertiesMatch(t, schemaDefinition(t, currentLock, "source"), compiler.CompiledSource{})
	assertPropertiesMatch(t, schemaDefinition(t, currentLock, "customRuntime"), customruntime.Summary{})
	assertPropertiesMatch(t, schemaDefinition(t, lock, "requiredFile"), policy.RequiredFile{})
	assertPropertiesMatch(t, schemaDefinition(t, lock, "evidence"), policy.EvidenceCheck{})
	assertPropertiesMatch(t, schemaDefinition(t, currentLock, "check"), policy.Check{})
	assertPropertiesMatch(t, schemaDefinition(t, lock, "assuranceExemption"), policy.AssuranceExemption{})
	assertPropertiesMatch(t, schemaDefinition(t, lock, "assurance"), policy.AssuranceGate{})
	assertPropertiesMatch(t, schemaDefinition(t, currentLock, "rule"), policy.Rule{})

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

	assertPropertiesMatch(t, schemaRootProperties(t, installationReceipt), usercli.Receipt{})
	assertPropertiesMatch(t, schemaRootProperties(t, globalDiagnostic), usercli.GlobalDiagnostic{})
	assertPropertiesMatch(t, schemaDefinition(t, globalDiagnostic, "check"), usercli.DiagnosticCheck{})
	assertPropertiesMatch(t, schemaDefinition(t, globalDiagnostic, "action"), usercli.DiagnosticAction{})
	assertPropertiesMatch(t, schemaRootProperties(t, globalLifecycle), usercli.LifecycleReport{})
	assertPropertiesMatch(t, schemaDefinition(t, globalLifecycle, "check"), usercli.DiagnosticCheck{})
	assertPropertiesMatch(t, schemaDefinition(t, globalLifecycle, "action"), usercli.DiagnosticAction{})
	assertPropertiesMatch(t, schemaRootProperties(t, harnessPackManifest), harnesspack.Manifest{})
	assertPropertiesMatch(t, schemaDefinition(t, harnessPackManifest, "compatibility"), harnesspack.Compatibility{})
	assertPropertiesMatch(t, schemaDefinition(t, harnessPackManifest, "file"), harnesspack.File{})
	assertPropertiesMatch(t, schemaRootProperties(t, repositoryInstall), bootstrap.RepositoryReceipt{})
	assertPropertiesMatch(t, schemaDefinition(t, repositoryInstall, "policyPack"), bootstrap.PolicyPackIdentity{})
	assertPropertiesMatch(t, schemaDefinition(t, repositoryInstall, "harnessPack"), bootstrap.HarnessPackIdentity{})
	assertPropertiesMatch(t, schemaDefinition(t, repositoryInstall, "managedFile"), bootstrap.ManagedFile{})
	assertPropertiesMatch(t, schemaDefinition(t, repositoryInstall, "managedBlock"), bootstrap.ManagedBlock{})
	assertPropertiesMatch(t, schemaDefinition(t, repositoryInstall, "generatedArtifact"), bootstrap.GeneratedArtifact{})
	assertPropertiesMatch(t, schemaRootProperties(t, repositorySyncPlan), bootstrap.SyncPlan{})
	assertPropertiesMatch(t, schemaDefinition(t, repositorySyncPlan, "action"), bootstrap.SyncAction{})
	assertPropertiesMatch(t, schemaDefinition(t, repositorySyncPlan, "migration"), bootstrap.SyncMigration{})
	assertPropertiesMatch(t, schemaDefinition(t, repositorySyncReport, "report"), bootstrap.SyncReport{})
	assertPropertiesMatch(t, schemaDefinition(t, repositorySyncReport, "verification"), bootstrap.SyncVerification{})
	assertPropertiesMatch(t, schemaDefinition(t, repositorySyncReport, "recovery"), bootstrap.SyncRecovery{})
	assertPropertiesMatch(t, schemaDefinition(t, repositorySyncReport, "resolution"), bootstrap.SyncResolutionReport{})
	assertPropertiesMatch(t, schemaRootProperties(t, releaseManifest), usercli.ReleaseManifest{})
	assertPropertiesMatch(t, schemaDefinition(t, releaseManifest, "asset"), usercli.ReleaseAsset{})
	assertPropertiesMatch(t, schemaRootProperties(t, customManifest), customruntime.Manifest{})
	assertPropertiesMatch(t, schemaDefinition(t, customManifest, "route"), customruntime.Route{})
	assertPropertiesMatch(t, schemaDefinition(t, customManifest, "fields"), customruntime.FieldMappings{})
	assertPropertiesMatch(t, schemaDefinition(t, customManifest, "guarantees"), customruntime.HostGuarantees{})
	assertPropertiesMatch(t, schemaRootProperties(t, neutralRequest), customruntime.NeutralRequest{})
	assertPropertiesMatch(t, schemaRootProperties(t, neutralResponse), customruntime.NeutralResponse{})
	assertPropertiesMatch(t, schemaRootProperties(t, customLiveness), customruntime.LivenessRecord{})
	assertPropertiesMatch(t, schemaRootProperties(t, customConformance), customruntime.ConformanceSuite{})
	assertPropertiesMatch(t, schemaDefinition(t, customConformance, "case"), customruntime.ConformanceCase{})
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
	lock := readCurrentLockSchemaDocument(t)
	properties := schemaRootProperties(t, lock)
	if got := properties["format_version"].(map[string]interface{})["const"]; got != "4" {
		t.Fatalf("format_version const = %v, want 4", got)
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

func readCurrentLockSchemaDocument(t *testing.T) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "v4", "policy-lock.schema.json"))
	if err != nil {
		t.Fatalf("read current lock schema: %v", err)
	}
	var document map[string]interface{}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse current lock schema: %v", err)
	}
	return document
}

func readCurrentSchemaDocument(t *testing.T, artifact schema.Artifact) map[string]interface{} {
	t.Helper()
	contract, ok := schema.CurrentContract(artifact)
	if !ok {
		t.Fatalf("registry has no current %s contract", artifact)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(contract.LocalPath)))
	if err != nil {
		t.Fatalf("read %s: %v", contract.LocalPath, err)
	}
	var document map[string]interface{}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse %s: %v", contract.LocalPath, err)
	}
	return document
}

type legacyPolicySource struct {
	Kind      policy.SourceKind `json:"kind"`
	Path      string            `json:"path"`
	Content   string            `json:"content"`
	BlockID   string            `json:"block_id,omitempty"`
	LineStart int               `json:"line_start,omitempty"`
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

// TestReleaseShipsTheCanonicalPolicyLockSchema ties the registry release name
// to the identity the product stamps.
func TestReleaseShipsTheCanonicalPolicyLockSchema(t *testing.T) {
	current, ok := schema.CurrentContract(schema.PolicyLock)
	if !ok {
		t.Fatal("registry has no current policy-lock contract")
	}
	if current.ReleaseAsset != "policy-lock.schema.json" || current.LocalPath != "schemas/v5/policy-lock.schema.json" {
		t.Fatalf("current lock release mapping = %s -> %s", current.ReleaseAsset, current.LocalPath)
	}

	// Every superseded format stays available under its own name.
	legacyAssets := map[string]bool{}
	for _, contract := range schema.Contracts() {
		if contract.Artifact == schema.PolicyLock && contract.State == schema.StateLegacy {
			legacyAssets[contract.ReleaseAsset] = true
		}
	}
	for _, legacy := range []string{"policy-lock-v1.schema.json", "policy-lock-v2.schema.json", "policy-lock-v3.schema.json", "policy-lock-v4.schema.json"} {
		if !legacyAssets[legacy] {
			t.Errorf("release no longer ships %s", legacy)
		}
	}
}
