package schema_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/schema"
)

func TestRegistryOwnsEveryLocalSchema(t *testing.T) {
	contracts := schema.Contracts()
	paths, err := filepath.Glob(filepath.Join("..", "..", "schemas", "v*", "*.schema.json"))
	if err != nil {
		t.Fatalf("glob schemas: %v", err)
	}
	if len(contracts) != len(paths) {
		t.Fatalf("registry contracts = %d, local schemas = %d", len(contracts), len(paths))
	}

	registeredPaths := make(map[string]bool, len(contracts))
	registeredKeys := make(map[string]bool, len(contracts))
	releaseAssets := make(map[string]bool, len(contracts))
	defaultURLs := make(map[string]bool, len(contracts))
	aliases := make(map[string]bool, len(contracts))
	for _, contract := range contracts {
		assertRegistryContract(t, contract)
		assertUnique(t, registeredPaths, contract.LocalPath, "local path")
		assertUnique(t, registeredKeys, string(contract.Artifact)+"/v"+contract.SchemaVersion, "artifact version")
		assertUnique(t, releaseAssets, contract.ReleaseAsset, "release asset")
		assertUnique(t, defaultURLs, contract.DefaultURL, "default URL")
		for _, alias := range contract.Aliases {
			assertUnique(t, aliases, alias.URL, "compatibility alias")
		}
	}
	for _, path := range paths {
		relative := filepath.ToSlash(filepath.Join("schemas", filepath.Base(filepath.Dir(path)), filepath.Base(path)))
		if !registeredPaths[relative] {
			t.Errorf("local schema is not registered: %s", relative)
		}
	}
}

func TestRegistryHasOneCurrentContractPerArtifact(t *testing.T) {
	current := make(map[schema.Artifact]int)
	for _, contract := range schema.Contracts() {
		if contract.State == schema.StateCurrent {
			current[contract.Artifact]++
		}
	}
	if len(current) != 24 {
		t.Fatalf("current artifact count = %d, want 24", len(current))
	}
	for artifact, count := range current {
		if count != 1 {
			t.Errorf("current contract count for %q = %d, want 1", artifact, count)
		}
		contract, ok := schema.CurrentContract(artifact)
		if !ok {
			t.Errorf("CurrentContract(%q) is absent", artifact)
			continue
		}
		if got := schema.DefaultURL(artifact); got != contract.DefaultURL {
			t.Errorf("DefaultURL(%q) = %q, want %q", artifact, got, contract.DefaultURL)
		}
	}
}

func TestActionLedgerRevisionPreservesPublishedV1(t *testing.T) {
	legacy, legacyOK := schema.ContractVersion(schema.ActionLedger, "1")
	current, currentOK := schema.ContractVersion(schema.ActionLedger, "2")
	if !legacyOK || legacy.State != schema.StateLegacy ||
		legacy.DefaultURL != schema.DefaultBaseURL+"/action-ledger.schema.json" ||
		legacy.SHA256 != "c8d85f2bdc82c51de468cbe7a62cce5251c2e724ec4dd29dd3c9d1535614c1cb" {
		t.Fatalf("immutable Action Ledger v1 contract = %#v, present=%t", legacy, legacyOK)
	}
	if !currentOK || current.State != schema.StateCurrent || current.DefaultURL != schema.ActionLedgerURL ||
		schema.DefaultURL(schema.ActionLedger) != current.DefaultURL {
		t.Fatalf("current Action Ledger v2 contract = %#v, present=%t", current, currentOK)
	}
}

func TestV6PublicationAdvancesWithoutRewritingUnchangedContracts(t *testing.T) {
	if schema.CurrentSchemaTag == schema.PreviousSchemaTag {
		t.Fatal("current and previous schema publication tags must differ")
	}
	v6, ok := schema.ContractVersion(schema.PolicyLock, "6")
	if !ok {
		t.Fatal("current policy-lock v6 contract is absent")
	}
	if v6.IntroductionTag != schema.CurrentSchemaTag || v6.DefaultURL != schema.PolicyLockURL {
		t.Fatalf("v6 publication identity = %#v, want current tag and URL", v6)
	}
	previousFound := false
	for _, alias := range v6.Aliases {
		if alias.URL == schema.PreviousPolicyLockV6URL {
			if alias.Reason != schema.AliasPriorPublication {
				t.Fatalf("v0.9.6 policy-lock v6 alias reason = %q, want %q", alias.Reason, schema.AliasPriorPublication)
			}
			previousFound = true
			break
		}
	}
	if !previousFound {
		t.Fatalf("v6 contract does not retain previous publication alias: %#v", v6.Aliases)
	}
	if !schema.AcceptsFormat(schema.PolicyLock, schema.PreviousPolicyLockV6URL, "6") {
		t.Fatal("v6 compatibility alias is not accepted for format 6")
	}
	unchanged, ok := schema.ContractVersion(schema.PolicyConfig, "4")
	if !ok {
		t.Fatal("current policy-config v4 contract is absent")
	}
	if unchanged.IntroductionTag != schema.PreviousSchemaTag || unchanged.DefaultURL != schema.PolicyConfigURL {
		t.Fatalf("unchanged contract was republished unexpectedly: %#v", unchanged)
	}
}

func TestRegistryRecordsExactForensicClassification(t *testing.T) {
	got := make(map[schema.Compatibility]int)
	for _, observation := range schema.Observations() {
		got[observation.Compatibility]++
	}
	want := map[schema.Compatibility]int{
		schema.CompatibilityByteIdentical:          1,
		schema.CompatibilityIDOnlyDrift:            11,
		schema.CompatibilitySemanticDrift:          4,
		schema.CompatibilityAbsentAtClaimedTag:     1,
		schema.CompatibilityUnreachableClaimedHost: 5,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("registry classifications = %v, want %v", got, want)
	}
}

func TestRegistrySnapshotsAreDetached(t *testing.T) {
	first := schema.Contracts()
	if len(first) == 0 || len(first[0].FormatVersions) == 0 {
		t.Fatal("registry fixture has no format version")
	}
	first[0].FormatVersions[0] = "mutated"
	aliasIndex := -1
	for index := range first {
		if len(first[index].Aliases) > 0 {
			aliasIndex = index
			break
		}
	}
	if aliasIndex < 0 {
		t.Fatal("registry fixture has no compatibility alias")
	}
	first[aliasIndex].Aliases[0].URL = "mutated"
	second := schema.Contracts()
	if second[0].FormatVersions[0] == "mutated" {
		t.Fatal("Contracts returned shared format-version storage")
	}
	if second[aliasIndex].Aliases[0].URL == "mutated" {
		t.Fatal("Contracts returned shared alias storage")
	}
	current, ok := schema.CurrentContract(second[0].Artifact)
	if !ok {
		t.Fatal("current contract is absent")
	}
	current.FormatVersions[0] = "mutated"
	third, _ := schema.CurrentContract(second[0].Artifact)
	if third.FormatVersions[0] == "mutated" {
		t.Fatal("CurrentContract returned shared format-version storage")
	}
}

func TestVersionResolutionAndCompatibilityAreRegistryDriven(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		version string
		value   string
		want    bool
	}{
		{name: "default", version: "1", value: schema.LegacyPolicyLockURL, want: true},
		{name: "unpinned alias", version: "1", value: schema.LegacyPolicyLockURLUnpinned, want: true},
		{name: "enterprise", base: "https://schemas.example.test", version: "1", value: "https://schemas.example.test/schemas/policy-lock/v1", want: true},
		{name: "wrong version", version: "1", value: schema.LegacyPolicyLockV2URL},
		{name: "unknown version", version: "99", value: schema.LegacyPolicyLockURL},
		{name: "empty", version: "1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("RECONC_SCHEMA_BASE_URL", test.base)
			if got := schema.AcceptsVersion(schema.PolicyLock, test.version, test.value); got != test.want {
				t.Fatalf("AcceptsVersion() = %t, want %t", got, test.want)
			}
		})
	}
	t.Setenv("RECONC_SCHEMA_BASE_URL", "https://schemas.example.test/")
	if got, want := schema.ResolveVersion(schema.PolicyLock, "3"), "https://schemas.example.test/schemas/policy-lock/v3"; got != want {
		t.Fatalf("ResolveVersion() = %q, want %q", got, want)
	}
	if got := schema.ResolveVersion(schema.PolicyLock, "99"); got != "" {
		t.Fatalf("ResolveVersion(unknown) = %q, want empty", got)
	}
	if !schema.Accepts(schema.PolicyLock, schema.LegacyPolicyLockV2URLUnpinned) {
		t.Fatal("Accepts rejected a registered legacy version alias")
	}
	if schema.Accepts(schema.PolicyLock, "https://example.invalid/schema") {
		t.Fatal("Accepts accepted an unregistered URL")
	}
}

func TestFormatCompatibilityCannotCrossSchemaVersions(t *testing.T) {
	t.Setenv("RECONC_SCHEMA_BASE_URL", "https://schemas.example.test")
	tests := []struct {
		name    string
		value   string
		format  string
		accepts bool
	}{
		{name: "default exact pair", value: schema.LegacyPolicyLockV2URL, format: "2", accepts: true},
		{name: "alias exact pair", value: schema.LegacyPolicyLockV2URLUnpinned, format: "2", accepts: true},
		{name: "enterprise exact pair", value: "https://schemas.example.test/schemas/policy-lock/v2", format: "2", accepts: true},
		{name: "crossed pair", value: schema.LegacyPolicyLockV2URL, format: "1"},
		{name: "future format", value: schema.PolicyLockURL, format: "99"},
		{name: "unknown URL", value: "https://example.invalid/policy-lock", format: "4"},
		{name: "empty URL", format: "4"},
		{name: "empty format", value: schema.PolicyLockURL},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := schema.AcceptsFormat(schema.PolicyLock, test.value, test.format); got != test.accepts {
				t.Fatalf("AcceptsFormat() = %t, want %t", got, test.accepts)
			}
		})
	}
	if schema.AcceptsFormat(schema.PolicyConfig, schema.PolicyConfigURL, "1") {
		t.Fatal("schema without a format_version accepted an invented pairing")
	}
}

func TestResolveUsesRegisteredEnterprisePath(t *testing.T) {
	t.Setenv("RECONC_SCHEMA_BASE_URL", "https://schemas.example.test/")
	for _, contract := range schema.Contracts() {
		if contract.State != schema.StateCurrent {
			continue
		}
		want := "https://schemas.example.test" + contract.EnterprisePath
		if contract.PortableDefault {
			want = contract.DefaultURL
		}
		if got := schema.Resolve(contract.Artifact); got != want {
			t.Errorf("Resolve(%q) = %q, want %q", contract.Artifact, got, want)
		}
	}
	if got := schema.Resolve(schema.Artifact("unknown")); got != "" {
		t.Fatalf("Resolve(unknown) = %q, want empty", got)
	}
}

func TestPortableDefaultStillAcceptsConfiguredEnterpriseIdentity(t *testing.T) {
	t.Setenv("RECONC_SCHEMA_BASE_URL", "https://schemas.example.test")
	contract, ok := schema.CurrentContract(schema.ProofBundle)
	if !ok || !contract.PortableDefault {
		t.Fatal("proof-bundle contract is not registered as portable-default")
	}
	if got := schema.Resolve(schema.ProofBundle); got != contract.DefaultURL {
		t.Fatalf("portable Resolve() = %q, want %q", got, contract.DefaultURL)
	}
	enterprise := "https://schemas.example.test" + contract.EnterprisePath
	if !schema.AcceptsFormat(schema.ProofBundle, enterprise, "1") {
		t.Fatal("configured enterprise proof identity was not accepted")
	}
}

func assertRegistryContract(t *testing.T, contract schema.Contract) {
	t.Helper()
	if contract.Artifact == "" || contract.SchemaVersion == "" || contract.LocalPath == "" ||
		contract.ReleaseAsset == "" || contract.DefaultURL == "" || contract.EnterprisePath == "" ||
		contract.IntroductionTag == "" || contract.SHA256 == "" {
		t.Fatalf("incomplete registry contract: %+v", contract)
	}
	if contract.State != schema.StateCurrent && contract.State != schema.StateLegacy {
		t.Errorf("invalid state for %s: %q", contract.LocalPath, contract.State)
	}
	if !strings.HasPrefix(contract.DefaultURL, "https://") || !strings.HasPrefix(contract.IntroductionTag, "reconc-v") {
		t.Errorf("invalid publication identity for %s", contract.LocalPath)
	}
	if got, want := contract.EnterprisePath, "/schemas/"+string(contract.Artifact)+"/v"+contract.SchemaVersion; got != want {
		t.Errorf("enterprise path for %s = %q, want %q", contract.LocalPath, got, want)
	}
	body, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(contract.LocalPath)))
	if err != nil {
		t.Fatalf("read %s: %v", contract.LocalPath, err)
	}
	digest := sha256.Sum256(body)
	if got := hex.EncodeToString(digest[:]); got != contract.SHA256 {
		t.Errorf("digest for %s = %s, want %s", contract.LocalPath, got, contract.SHA256)
	}
	var document map[string]interface{}
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatalf("parse %s: %v", contract.LocalPath, err)
	}
	if document["$id"] != contract.DefaultURL {
		t.Errorf("$id for %s = %q, want %q", contract.LocalPath, document["$id"], contract.DefaultURL)
	}
	if !sort.StringsAreSorted(contract.FormatVersions) {
		t.Errorf("format versions for %s are not sorted: %v", contract.LocalPath, contract.FormatVersions)
	}
	if got := schemaFormatVersions(document); !slices.Equal(got, contract.FormatVersions) {
		t.Errorf("format versions in %s = %v, registry has %v", contract.LocalPath, got, contract.FormatVersions)
	}
}

func schemaFormatVersions(document map[string]interface{}) []string {
	values := map[string]bool{}
	var visit func(interface{})
	visit = func(value interface{}) {
		switch typed := value.(type) {
		case map[string]interface{}:
			if properties, ok := typed["properties"].(map[string]interface{}); ok {
				if format, ok := properties["format_version"].(map[string]interface{}); ok {
					if constant, ok := format["const"].(string); ok {
						values[constant] = true
						return
					}
				}
			}
			for _, nested := range typed {
				visit(nested)
			}
		case []interface{}:
			for _, nested := range typed {
				visit(nested)
			}
		}
	}
	visit(document)
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func assertUnique(t *testing.T, seen map[string]bool, value string, label string) {
	t.Helper()
	if seen[value] {
		t.Errorf("duplicate registry %s: %s", label, value)
	}
	seen[value] = true
}
