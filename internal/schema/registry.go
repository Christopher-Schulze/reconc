package schema

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Compatibility classifies the relationship between a local schema and the
// publication identity embedded in that schema.
type Compatibility string

const (
	CompatibilityByteIdentical          Compatibility = "byte_identical"
	CompatibilityIDOnlyDrift            Compatibility = "id_only_drift"
	CompatibilitySemanticDrift          Compatibility = "semantic_drift"
	CompatibilityAbsentAtClaimedTag     Compatibility = "absent_at_claimed_tag"
	CompatibilityUnreachableClaimedHost Compatibility = "unreachable_claimed_host"
)

// AliasReason states why a non-canonical schema identity remains accepted as
// input. Aliases are never emitted or presented as publication locations.
type AliasReason string

const (
	AliasUnpinnedLegacy     AliasReason = "unpinned_legacy"
	AliasMisboundReleaseTag AliasReason = "misbound_release_tag"
	AliasPriorPublication   AliasReason = "prior_publication"
	AliasUnreachableHost    AliasReason = "unreachable_host"
)

// Alias is one input-only identity retained for compatibility.
type Alias struct {
	URL    string      `json:"url"`
	Reason AliasReason `json:"reason"`
}

// Observation preserves the exact forensic state of one of the 22 inherited
// schema files before repair. FirstExactTag owns the observed bytes, while
// ClaimedURL is the identity those bytes embedded at the time.
type Observation struct {
	LocalPath     string        `json:"local_path"`
	ClaimedURL    string        `json:"claimed_url"`
	FirstExactTag string        `json:"first_exact_tag"`
	SHA256        string        `json:"sha256"`
	Compatibility Compatibility `json:"compatibility"`
}

// State identifies whether a schema contract is emitted now or retained only
// for compatibility and offline verification.
type State string

const (
	StateCurrent State = "current"
	StateLegacy  State = "legacy"
)

// Contract is one independently versioned public JSON Schema contract.
type Contract struct {
	Artifact        Artifact `json:"artifact"`
	SchemaVersion   string   `json:"schema_version"`
	FormatVersions  []string `json:"format_versions,omitempty"`
	LocalPath       string   `json:"local_path"`
	ReleaseAsset    string   `json:"release_asset"`
	DefaultURL      string   `json:"default_url"`
	EnterprisePath  string   `json:"enterprise_path"`
	IntroductionTag string   `json:"introduction_tag"`
	SHA256          string   `json:"sha256"`
	State           State    `json:"state"`
	PortableDefault bool     `json:"portable_default,omitempty"`
	Aliases         []Alias  `json:"aliases,omitempty"`
}

type registryVersionKey struct {
	artifact      Artifact
	schemaVersion string
}

type registryFormatKey struct {
	artifact      Artifact
	schemaVersion string
	formatVersion string
}

type immutableRegistry struct {
	contracts         []Contract
	observations      []Observation
	byArtifact        map[Artifact][]int
	currentByArtifact map[Artifact]int
	byVersion         map[registryVersionKey]int
	byIdentity        map[string]int
	byEnterprisePath  map[string]int
	byFormat          map[registryFormatKey]struct{}
}

var staticRegistry = buildRegistry()

func buildRegistry() immutableRegistry {
	values := contracts()
	result := immutableRegistry{
		contracts:         values,
		observations:      observations(),
		byArtifact:        make(map[Artifact][]int),
		currentByArtifact: make(map[Artifact]int),
		byVersion:         make(map[registryVersionKey]int, len(values)),
		byIdentity:        make(map[string]int, len(values)),
		byEnterprisePath:  make(map[string]int, len(values)),
		byFormat:          make(map[registryFormatKey]struct{}),
	}
	for index, contract := range values {
		result.byArtifact[contract.Artifact] = append(result.byArtifact[contract.Artifact], index)
		result.byVersion[registryVersionKey{artifact: contract.Artifact, schemaVersion: contract.SchemaVersion}] = index
		if contract.State == StateCurrent {
			result.currentByArtifact[contract.Artifact] = index
		}
		result.byIdentity[contract.DefaultURL] = index
		for _, alias := range contract.Aliases {
			result.byIdentity[alias.URL] = index
		}
		result.byEnterprisePath[contract.EnterprisePath] = index
		for _, formatVersion := range contract.FormatVersions {
			result.byFormat[registryFormatKey{
				artifact: contract.Artifact, schemaVersion: contract.SchemaVersion,
				formatVersion: formatVersion,
			}] = struct{}{}
		}
	}
	return result
}

// Contracts returns a detached, deterministically ordered registry snapshot.
func Contracts() []Contract {
	contracts := make([]Contract, len(staticRegistry.contracts))
	for index, contract := range staticRegistry.contracts {
		contracts[index] = cloneContract(contract)
	}
	return contracts
}

// CurrentContract returns the one schema currently emitted for an artifact.
func CurrentContract(artifact Artifact) (Contract, bool) {
	index, ok := staticRegistry.currentByArtifact[artifact]
	if !ok {
		return Contract{}, false
	}
	return cloneContract(staticRegistry.contracts[index]), true
}

// ContractVersion returns one exact artifact schema version.
func ContractVersion(artifact Artifact, schemaVersion string) (Contract, bool) {
	index, ok := staticRegistry.byVersion[registryVersionKey{artifact: artifact, schemaVersion: schemaVersion}]
	if !ok {
		return Contract{}, false
	}
	return cloneContract(staticRegistry.contracts[index]), true
}

// Observations returns the detached, path-ordered inherited-schema inventory.
func Observations() []Observation {
	return append([]Observation(nil), staticRegistry.observations...)
}

// ResolveVersion returns the default or enterprise URL for one exact schema
// version. Unknown artifact/version pairs resolve to an empty string.
func ResolveVersion(artifact Artifact, schemaVersion string) string {
	index, ok := staticRegistry.byVersion[registryVersionKey{artifact: artifact, schemaVersion: schemaVersion}]
	if !ok {
		return ""
	}
	return resolveContract(staticRegistry.contracts[index])
}

// AcceptsVersion reports whether a URL is a registered default, compatibility
// alias, or configured enterprise identity for one exact schema version.
func AcceptsVersion(artifact Artifact, schemaVersion string, value string) bool {
	index, ok := staticRegistry.byVersion[registryVersionKey{artifact: artifact, schemaVersion: schemaVersion}]
	if !ok {
		return false
	}
	return acceptsContractIndex(index, value)
}

// Accepts reports whether a URL belongs to any registered version of an
// artifact. Use AcceptsVersion when a decoder already knows the exact version.
func Accepts(artifact Artifact, value string) bool {
	_, ok := lookupContractIndex(artifact, value)
	return ok
}

// AcceptsFormat reports whether a URL and format-version pair belongs to one
// registered schema contract. It prevents a known URL from being combined
// with a format version owned by a different schema version.
func AcceptsFormat(artifact Artifact, value string, formatVersion string) bool {
	index, ok := lookupContractIndex(artifact, value)
	if !ok {
		return false
	}
	contract := staticRegistry.contracts[index]
	_, ok = staticRegistry.byFormat[registryFormatKey{
		artifact: contract.Artifact, schemaVersion: contract.SchemaVersion,
		formatVersion: formatVersion,
	}]
	return ok
}

// ValidateRegistry verifies the complete static registry before release tools
// expose it as an asset inventory.
func ValidateRegistry() error {
	if err := validateContracts(staticRegistry.contracts); err != nil {
		return err
	}
	return validateObservations(staticRegistry.observations)
}

func cloneContract(contract Contract) Contract {
	contract.FormatVersions = append([]string(nil), contract.FormatVersions...)
	contract.Aliases = append([]Alias(nil), contract.Aliases...)
	return contract
}

func validateContracts(values []Contract) error {
	if len(values) == 0 {
		return fmt.Errorf("schema registry is empty")
	}
	owners := newRegistryOwners()
	artifacts := map[Artifact]bool{}
	current := map[Artifact]int{}
	for index, contract := range values {
		if err := validateContractShape(contract); err != nil {
			return fmt.Errorf("schema contract %q v%s: %w", contract.Artifact, contract.SchemaVersion, err)
		}
		if err := owners.claim(contract); err != nil {
			return err
		}
		if index > 0 && !contractLess(values[index-1], contract) {
			return fmt.Errorf("schema registry contracts are not uniquely ordered by artifact and schema version")
		}
		artifacts[contract.Artifact] = true
		if contract.State == StateCurrent {
			current[contract.Artifact]++
		}
	}
	for artifact := range artifacts {
		if current[artifact] != 1 {
			return fmt.Errorf("schema artifact %q has %d current contracts, want 1", artifact, current[artifact])
		}
	}
	return nil
}

func contractLess(left Contract, right Contract) bool {
	if left.Artifact != right.Artifact {
		return left.Artifact < right.Artifact
	}
	leftVersion, _ := strconv.Atoi(left.SchemaVersion)
	rightVersion, _ := strconv.Atoi(right.SchemaVersion)
	return leftVersion < rightVersion
}

type registryOwners struct {
	versions   map[string]string
	localPaths map[string]string
	assets     map[string]string
	identities map[string]string
	enterprise map[string]string
}

func newRegistryOwners() registryOwners {
	return registryOwners{
		versions: map[string]string{}, localPaths: map[string]string{},
		assets: map[string]string{}, identities: map[string]string{}, enterprise: map[string]string{},
	}
}

func (owners registryOwners) claim(contract Contract) error {
	key := string(contract.Artifact) + "/v" + contract.SchemaVersion
	for _, owned := range []struct {
		values map[string]string
		value  string
		label  string
	}{
		{owners.versions, key, "artifact version"},
		{owners.localPaths, contract.LocalPath, "local path"},
		{owners.assets, contract.ReleaseAsset, "release asset"},
		{owners.enterprise, contract.EnterprisePath, "enterprise path"},
		{owners.identities, contract.DefaultURL, "schema identity"},
	} {
		if err := claimRegistryValue(owned.values, owned.value, key, owned.label); err != nil {
			return err
		}
	}
	for _, alias := range contract.Aliases {
		if err := claimRegistryValue(owners.identities, alias.URL, key, "schema identity"); err != nil {
			return err
		}
	}
	return nil
}

func claimRegistryValue(owners map[string]string, value string, owner string, label string) error {
	if previous := owners[value]; previous != "" {
		return fmt.Errorf("duplicate schema registry %s %q owned by %s and %s", label, value, previous, owner)
	}
	owners[value] = owner
	return nil
}

func validateContractShape(contract Contract) error {
	if !validArtifact(contract.Artifact) || !canonicalVersion(contract.SchemaVersion) {
		return fmt.Errorf("artifact or schema version is invalid")
	}
	if !validLocalSchemaPath(contract.LocalPath, contract.SchemaVersion) || !validReleaseAsset(contract.ReleaseAsset) {
		return fmt.Errorf("local path or release asset is invalid")
	}
	if !validReleaseTag(contract.IntroductionTag) {
		return fmt.Errorf("introduction tag is invalid")
	}
	if !validSchemaURL(contract.DefaultURL) || strings.Contains(contract.DefaultURL, "/main/") ||
		contract.DefaultURL != taggedSchemaURL(contract.IntroductionTag, contract.LocalPath) {
		return fmt.Errorf("default URL is not immutable HTTPS")
	}
	if contract.EnterprisePath != "/schemas/"+string(contract.Artifact)+"/v"+contract.SchemaVersion {
		return fmt.Errorf("enterprise path is invalid")
	}
	if !validRegistryDigest(contract.SHA256) || !validState(contract.State) {
		return fmt.Errorf("digest or state is invalid")
	}
	if !sort.StringsAreSorted(contract.FormatVersions) || containsDuplicate(contract.FormatVersions) {
		return fmt.Errorf("format versions must be unique and sorted")
	}
	for _, format := range contract.FormatVersions {
		if format == "" || strings.TrimSpace(format) != format {
			return fmt.Errorf("format versions must be non-empty, unique, and sorted")
		}
	}
	for _, alias := range contract.Aliases {
		if !validSchemaURL(alias.URL) || alias.URL == contract.DefaultURL || !validAliasReason(alias.Reason) {
			return fmt.Errorf("compatibility alias is invalid")
		}
	}
	return nil
}

func validateObservations(values []Observation) error {
	if len(values) != 22 {
		return fmt.Errorf("schema observation inventory has %d entries, want 22", len(values))
	}
	seen := make(map[string]bool, len(values))
	for index, observation := range values {
		if observation.LocalPath == "" || !validSchemaURL(observation.ClaimedURL) ||
			!validReleaseTag(observation.FirstExactTag) ||
			!validRegistryDigest(observation.SHA256) || !validCompatibility(observation.Compatibility) {
			return fmt.Errorf("schema observation %q is invalid", observation.LocalPath)
		}
		if seen[observation.LocalPath] {
			return fmt.Errorf("duplicate schema observation path %q", observation.LocalPath)
		}
		if index > 0 && values[index-1].LocalPath >= observation.LocalPath {
			return fmt.Errorf("schema observations are not uniquely ordered by local path")
		}
		seen[observation.LocalPath] = true
	}
	return nil
}

func validArtifact(artifact Artifact) bool {
	value := string(artifact)
	if value == "" || strings.Trim(value, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
		return false
	}
	return value[0] != '-' && value[len(value)-1] != '-'
}

func canonicalVersion(value string) bool {
	version, err := strconv.Atoi(value)
	return err == nil && version > 0 && strconv.Itoa(version) == value
}

func validLocalSchemaPath(value string, schemaVersion string) bool {
	return value == path.Clean(value) && !path.IsAbs(value) &&
		strings.HasPrefix(value, "schemas/v"+schemaVersion+"/") && strings.HasSuffix(value, ".schema.json")
}

func validReleaseAsset(value string) bool {
	if value == "" || path.Base(value) != value || value == "." || value == ".." {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '.' || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func validReleaseTag(value string) bool {
	core := strings.TrimPrefix(value, "reconc-v")
	parts := strings.Split(core, ".")
	if core == value || len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 || strconv.Itoa(number) != part {
			return false
		}
	}
	return true
}

func validSchemaURL(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func validRegistryDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && strings.ToLower(value) == value
}

func validCompatibility(value Compatibility) bool {
	switch value {
	case CompatibilityByteIdentical, CompatibilityIDOnlyDrift, CompatibilitySemanticDrift,
		CompatibilityAbsentAtClaimedTag, CompatibilityUnreachableClaimedHost:
		return true
	default:
		return false
	}
}

func validAliasReason(value AliasReason) bool {
	return value == AliasUnpinnedLegacy || value == AliasMisboundReleaseTag ||
		value == AliasPriorPublication || value == AliasUnreachableHost
}

func validState(value State) bool {
	return value == StateCurrent || value == StateLegacy
}

func containsDuplicate(values []string) bool {
	for index := 1; index < len(values); index++ {
		if values[index-1] == values[index] {
			return true
		}
	}
	return false
}

func acceptsContractIndex(index int, value string) bool {
	if index < 0 || index >= len(staticRegistry.contracts) {
		return false
	}
	matched, ok := lookupContractIndex(staticRegistry.contracts[index].Artifact, value)
	return ok && matched == index
}

func lookupContractIndex(artifact Artifact, value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	if index, ok := staticRegistry.byIdentity[value]; ok && staticRegistry.contracts[index].Artifact == artifact {
		return index, true
	}
	base := strings.TrimRight(os.Getenv("RECONC_SCHEMA_BASE_URL"), "/")
	if base == "" || !strings.HasPrefix(value, base) {
		return 0, false
	}
	enterprisePath := strings.TrimPrefix(value, base)
	index, ok := staticRegistry.byEnterprisePath[enterprisePath]
	if !ok || base+enterprisePath != value || staticRegistry.contracts[index].Artifact != artifact {
		return 0, false
	}
	return index, true
}

func acceptsContract(contract Contract, value string) bool {
	if value == "" {
		return false
	}
	if value == contract.DefaultURL || value == enterpriseContractURL(contract) {
		return true
	}
	for _, alias := range contract.Aliases {
		if value == alias.URL {
			return true
		}
	}
	return false
}

func taggedSchemaURL(tag string, localPath string) string {
	return "https://raw.githubusercontent.com/Christopher-Schulze/reconc/" + tag + "/" + localPath
}

func contracts() []Contract {
	return []Contract{
		contract(ActionControlMap, "1", []string{"1"}, "schemas/v1/action-control-map.schema.json", "action-control-map.schema.json", ActionControlMapURL, PreviousSchemaTag, "2116b35ea99397c47b2a5fc64a150564d775ba23417eef7e83ab95e74eab0c57", StateCurrent),
		contract(ActionControlMapAuthorities, "1", []string{"1"}, "schemas/v1/action-control-map-authorities.schema.json", "action-control-map-authorities.schema.json", ActionControlMapAuthoritiesURL, PreviousSchemaTag, "a40e9e641ea75cd3158a31026ef78d72206bf88d11a63d2a55b9a7988eb09207", StateCurrent),
		contract(ActionControlMapSignature, "1", []string{"1"}, "schemas/v1/action-control-map-signature.schema.json", "action-control-map-signature.schema.json", ActionControlMapSignatureURL, PreviousSchemaTag, "cac8a8400bcb923a89f749e23a099a1f07d19bd16537b49d47e5c33284348ba1", StateCurrent),
		contract(ActionEvidence, "1", []string{"1"}, "schemas/v1/action-evidence.schema.json", "action-evidence.schema.json", ActionEvidenceURL, PreviousSchemaTag, "19a5687a3032cce72a0db66e14dc739e811ed80ba6b1f583c59d4a742582c558", StateCurrent),
		contract(ActionLedger, "1", []string{"1"}, "schemas/v1/action-ledger.schema.json", "action-ledger-v1.schema.json", DefaultBaseURL+"/action-ledger.schema.json", PreviousSchemaTag, "c8d85f2bdc82c51de468cbe7a62cce5251c2e724ec4dd29dd3c9d1535614c1cb", StateLegacy),
		contract(ActionLedger, "2", []string{"1"}, "schemas/v2/action-ledger.schema.json", "action-ledger.schema.json", ActionLedgerURL, PreviousSchemaTag, "02a2e2c5ac76d77709ab3f33f600a99a0119288dfbb8a4166ffacf0aeed916a8", StateCurrent),
		contract(CompletionReport, "1", []string{"1"}, "schemas/v1/completion-report.schema.json", "completion-report.schema.json", CompletionReportURL, PreviousSchemaTag, "811f87716131ce2561b55917805ce0c84146521778a30c316389e401e03843c1", StateCurrent, misbound("schemas/v1/completion-report.schema.json"), unpinned("schemas/v1/completion-report.schema.json")),
		contract(CustomRuntimeConformance, "1", []string{"reconc-custom-runtime-conformance/v1"}, "schemas/v1/custom-runtime-conformance.schema.json", "custom-runtime-conformance.schema.json", CustomRuntimeConformanceURL, PreviousSchemaTag, "3931edb9a5c61ea3f423a933cb40cae435b1f3dab09295dd37db101c511c919b", StateCurrent, unreachable("https://reconc.dev/schemas/custom-runtime-conformance/v1")),
		contract(CustomRuntimeLiveness, "1", []string{"reconc-custom-runtime-liveness/v1"}, "schemas/v1/custom-runtime-liveness.schema.json", "custom-runtime-liveness.schema.json", CustomRuntimeLivenessURL, PreviousSchemaTag, "321e760512ea5332bc6830d297968a5fb6f238e2f4e70035d54b1e545a1358c4", StateCurrent, unreachable("https://reconc.dev/schemas/custom-runtime-liveness/v1")),
		contract(CustomRuntimeManifest, "1", []string{"reconc-custom-runtime/v1"}, "schemas/v1/custom-runtime-manifest.schema.json", "custom-runtime-manifest-v1.schema.json", DefaultBaseURL+"/custom-runtime-manifest.schema.json", PreviousSchemaTag, "58d55b01f2aeb4d0a63f7512df338fd94be494e581f64bc24e14213bdf4b6fd0", StateLegacy, unreachable("https://reconc.dev/schemas/custom-runtime-manifest/v1")),
		contract(CustomRuntimeManifest, "2", []string{"reconc-custom-runtime/v2"}, "schemas/v2/custom-runtime-manifest.schema.json", "custom-runtime-manifest.schema.json", CustomRuntimeManifestURL, PreviousSchemaTag, "fb32c2cd624f7c26c0b02699cdd938ca2c51e5caea48b6732c1a47882668b773", StateCurrent),
		contract(GlobalDiagnostic, "1", []string{"reconc.global-diagnostic/v1"}, "schemas/v1/global-diagnostic.schema.json", "global-diagnostic.schema.json", GlobalDiagnosticURL, PreviousSchemaTag, "2e6ef698e59e1df7b18107d3e2d8b57d947f3fff64175b188158ea081e7e8cd7", StateCurrent, misbound("schemas/v1/global-diagnostic.schema.json"), unpinned("schemas/v1/global-diagnostic.schema.json")),
		contract(GlobalLifecycle, "1", []string{"reconc.global-lifecycle/v1"}, "schemas/v1/global-lifecycle.schema.json", "global-lifecycle.schema.json", GlobalLifecycleURL, PreviousSchemaTag, "cc422e2e78ebaf3ea94dcef15ce09acef4bca0e2bd2e9fe0d130925b1e0bc341", StateCurrent, misbound("schemas/v1/global-lifecycle.schema.json"), unpinned("schemas/v1/global-lifecycle.schema.json")),
		contract(HarnessPackManifest, "1", []string{"reconc.harness-pack/v1"}, "schemas/v1/harness-pack-manifest.schema.json", "harness-pack-manifest.schema.json", HarnessPackManifestURL, PreviousSchemaTag, "b902a974a2ff488ed275adc3b6e07895be88ccb6ec19d49f4d366cf760cdbb24", StateCurrent, misbound("schemas/v1/harness-pack-manifest.schema.json"), unpinned("schemas/v1/harness-pack-manifest.schema.json")),
		contract(InstallationReceipt, "1", []string{"1"}, "schemas/v1/installation-receipt.schema.json", "installation-receipt.schema.json", InstallationReceiptURL, PreviousSchemaTag, "de380599f45e979381cdf9957a611d33b96c6d0ddb0d4fcea35c72e0c98e568c", StateCurrent, misbound("schemas/v1/installation-receipt.schema.json"), unpinned("schemas/v1/installation-receipt.schema.json")),
		contract(NeutralHookRequest, "1", []string{"reconc-neutral-hook-request/v1"}, "schemas/v1/neutral-hook-request.schema.json", "neutral-hook-request.schema.json", NeutralHookRequestURL, PreviousSchemaTag, "521133dd1e6e9d5dc76092fda320a41016e8e009b9ca77d9fbc9638a318f877e", StateCurrent, unreachable("https://reconc.dev/schemas/neutral-hook-request/v1")),
		contract(NeutralHookResponse, "1", []string{"reconc-neutral-hook-response/v1"}, "schemas/v1/neutral-hook-response.schema.json", "neutral-hook-response.schema.json", NeutralHookResponseURL, PreviousSchemaTag, "6271d3d728edeb372f0de921f02aa7ed0e3681a796dcd622848352ea5404e7b3", StateCurrent, unreachable("https://reconc.dev/schemas/neutral-hook-response/v1")),
		contract(PolicyConfig, "1", nil, "schemas/v1/policy-config.schema.json", "policy-config-v1.schema.json", DefaultBaseURL+"/policy-config.schema.json", PreviousSchemaTag, "7904398abf27b06418a51048926526786755a89132268ec25f7ecf398e6f68b1", StateLegacy, unpinned("schemas/v1/policy-config.schema.json")),
		contract(PolicyConfig, "2", nil, "schemas/v2/policy-config.schema.json", "policy-config-v2.schema.json", Version2BaseURL+"/policy-config.schema.json", PreviousSchemaTag, "e5856413af32bea5f8b0fc108b3e5dcdfc84faf9d5e7e09bada79e7bdb5cad03", StateLegacy, misbound("schemas/v1/policy-config.schema.json")),
		contract(PolicyConfig, "3", nil, "schemas/v3/policy-config.schema.json", "policy-config-v3.schema.json", Version3BaseURL+"/policy-config.schema.json", PreviousSchemaTag, "194e00de2e112680f3cf683e18b679a6bd926ef6fad8cdebb49b38379784fef8", StateLegacy),
		contract(PolicyConfig, "4", nil, "schemas/v4/policy-config.schema.json", "policy-config.schema.json", PolicyConfigURL, PreviousSchemaTag, "fe87ab8b32ece847df6974cbacdcac3ca9aafac85c04d028928ea4f5e91b4b0f", StateCurrent),
		contract(PolicyFixPlan, "1", []string{"1"}, "schemas/v1/policy-fix-plan.schema.json", "policy-fix-plan.schema.json", PolicyFixPlanURL, PreviousSchemaTag, "79e352562cadcf1fc84dbd438dfc9e884c3eb3d3a5b2db6aeb581bda1c9e99c1", StateCurrent, misbound("schemas/v1/policy-fix-plan.schema.json"), unpinned("schemas/v1/policy-fix-plan.schema.json")),
		contract(PolicyLock, "1", []string{"1"}, "schemas/v1/policy-lock.schema.json", "policy-lock-v1.schema.json", LegacyPolicyLockURL, PreviousSchemaTag, "58215033576329d41d2d80fd3c8f8d7c43a54571f9b14f18ec7040225667e325", StateLegacy, Alias{URL: LegacyPolicyLockURLV091, Reason: AliasMisboundReleaseTag}, Alias{URL: LegacyPolicyLockURLUnpinned, Reason: AliasUnpinnedLegacy}),
		contract(PolicyLock, "2", []string{"2"}, "schemas/v2/policy-lock.schema.json", "policy-lock-v2.schema.json", LegacyPolicyLockV2URL, PreviousSchemaTag, "b1bcbd2c7b1ae25a6e8e26aaa40ea39ce83cfe23daea5b62007c34e55ea355c7", StateLegacy, Alias{URL: LegacyPolicyLockV2URLV091, Reason: AliasMisboundReleaseTag}, Alias{URL: LegacyPolicyLockV2URLUnpinned, Reason: AliasUnpinnedLegacy}),
		contract(PolicyLock, "3", []string{"3"}, "schemas/v3/policy-lock.schema.json", "policy-lock-v3.schema.json", LegacyPolicyLockV3URL, PreviousSchemaTag, "71f098011740601759e93193217e875bae5861859e8bf25102530e37b833d099", StateLegacy, Alias{URL: LegacyPolicyLockV3URLV091, Reason: AliasMisboundReleaseTag}, Alias{URL: LegacyPolicyLockV3URLUnpinned, Reason: AliasUnpinnedLegacy}),
		contract(PolicyLock, "4", []string{"4"}, "schemas/v4/policy-lock.schema.json", "policy-lock-v4.schema.json", LegacyPolicyLockV4URL, "reconc-v0.9.4", "32f16bde36b7e8e5d0671c1e3f8bcbf35f810ad7699d93291d1ebb29831b3450", StateLegacy),
		contract(PolicyLock, "5", []string{"5"}, "schemas/v5/policy-lock.schema.json", "policy-lock-v5.schema.json", LegacyPolicyLockV5URL, PreviousSchemaTag, "86838b49f01d254f0d6fc652105304fbd653c12b8f51a32674013d6bfa87c8f9", StateLegacy),
		contract(PolicyLock, "6", []string{"6"}, "schemas/v6/policy-lock.schema.json", "policy-lock.schema.json", PolicyLockURL, CurrentSchemaTag, "e54368e7c046303798ebab0bbbf3d16e4a68f4fdaeb3e98043b54ad5e64a08a4", StateCurrent, Alias{URL: PreviousPolicyLockV6URL, Reason: AliasPriorPublication}),
		contract(PolicyReport, "1", []string{"1"}, "schemas/v1/policy-report.schema.json", "policy-report.schema.json", PolicyReportURL, PreviousSchemaTag, "96cb3ebd87dfd06c904daece64ed40178d5c826c211f3bf101065701e090918a", StateCurrent, misbound("schemas/v1/policy-report.schema.json"), unpinned("schemas/v1/policy-report.schema.json")),
		portableContract(contract(ProofBundle, "1", []string{"1"}, "schemas/v1/proof-bundle.schema.json", "proof-bundle.schema.json", ProofBundleURL, PreviousSchemaTag, "83abb361727ec94993b840b7d6cb1f9a7935692c4282244f2de33b67a6d2fbac", StateCurrent, misbound("schemas/v1/proof-bundle.schema.json"), unpinned("schemas/v1/proof-bundle.schema.json"))),
		contract(ReleaseManifest, "1", []string{"reconc.release/v1"}, "schemas/v1/release-manifest.schema.json", "release-manifest.schema.json", ReleaseManifestURL, PreviousSchemaTag, "dad8261a8464ebfb8b6011a53ac5c6c55afeb65e494565a04ea3d0c13f5831e2", StateCurrent, misbound("schemas/v1/release-manifest.schema.json"), unpinned("schemas/v1/release-manifest.schema.json")),
		contract(RepositoryInstall, "1", []string{"1"}, "schemas/v1/repository-install.schema.json", "repository-install.schema.json", RepositoryInstallURL, PreviousSchemaTag, "6c79dc47b374e83142e95adf10913c6ff2074141c240821a5f26b9a6260cdb04", StateCurrent, misbound("schemas/v1/repository-install.schema.json"), unpinned("schemas/v1/repository-install.schema.json")),
		contract(RepositorySyncPlan, "1", []string{"reconc.repository-sync-plan/v1"}, "schemas/v1/repository-sync-plan.schema.json", "repository-sync-plan-v1.schema.json", DefaultBaseURL+"/repository-sync-plan.schema.json", PreviousSchemaTag, "a194fefd31b436c1410f753d5f1c48f4dd6bef565a5b01ec973f6290ae28acd5", StateLegacy, unpinned("schemas/v1/repository-sync-plan.schema.json")),
		contract(RepositorySyncPlan, "2", []string{"reconc.repository-sync-plan/v1"}, "schemas/v2/repository-sync-plan.schema.json", "repository-sync-plan.schema.json", RepositorySyncPlanURL, PreviousSchemaTag, "9cb0f21e49cb1d2abb8642bf5e01a18f7e8530ab6f0ddffe75b87463e364bbc8", StateCurrent, misbound("schemas/v1/repository-sync-plan.schema.json")),
		contract(RepositorySyncReport, "1", []string{"reconc.repository-sync-report/v1", "reconc.repository-sync-verify/v1"}, "schemas/v1/repository-sync-report.schema.json", "repository-sync-report-v1.schema.json", DefaultBaseURL+"/repository-sync-report.schema.json", PreviousSchemaTag, "917670e92c1d93a30de8b7a51c5bb54d372883a747a87219ad6135581f423016", StateLegacy, unpinned("schemas/v1/repository-sync-report.schema.json")),
		contract(RepositorySyncReport, "2", []string{"reconc.repository-sync-recovery/v1", "reconc.repository-sync-report/v1", "reconc.repository-sync-resolution/v1", "reconc.repository-sync-verify/v1"}, "schemas/v2/repository-sync-report.schema.json", "repository-sync-report.schema.json", RepositorySyncReportURL, PreviousSchemaTag, "26abc83ad42bd0b1ac6b501425f8a2b85533e5fbf9f35d63ec8e710a85cfb550", StateCurrent, misbound("schemas/v1/repository-sync-report.schema.json")),
	}
}

func observations() []Observation {
	return []Observation{
		observation("schemas/v1/completion-report.schema.json", legacyPinned("schemas/v1/completion-report.schema.json"), "reconc-v0.9.2", "fe82ea1a5a0a1129494fd9097aa8d3253ae7ef1e36e142d5fb0e61551e007874", CompatibilityIDOnlyDrift),
		observation("schemas/v1/custom-runtime-conformance.schema.json", "https://reconc.dev/schemas/custom-runtime-conformance/v1", "reconc-v0.9.3", "9f0ee8fb2d82c237bd25a3355c9a194032894b9bfc4031b4a7c586a0e8d78b46", CompatibilityUnreachableClaimedHost),
		observation("schemas/v1/custom-runtime-liveness.schema.json", "https://reconc.dev/schemas/custom-runtime-liveness/v1", "reconc-v0.9.3", "3cff6f5eef64cac02ccd37c9f4f2392b164d046c86df078b6cd5697323a7ba2f", CompatibilityUnreachableClaimedHost),
		observation("schemas/v1/custom-runtime-manifest.schema.json", "https://reconc.dev/schemas/custom-runtime-manifest/v1", "reconc-v0.9.3", "daacba83c2ebb7be6db5f32d3cded322d263eb47fc2f8ef56042cb7611ae0c05", CompatibilityUnreachableClaimedHost),
		observation("schemas/v1/global-diagnostic.schema.json", legacyPinned("schemas/v1/global-diagnostic.schema.json"), "reconc-v0.9.2", "53fceb314ccd050e586dc8faac5146b885367c9a970d9c45a10a4cb98ce18683", CompatibilityIDOnlyDrift),
		observation("schemas/v1/global-lifecycle.schema.json", legacyPinned("schemas/v1/global-lifecycle.schema.json"), "reconc-v0.9.2", "a2e2e71a5bc8542ee2e226b5e93e29c2ae4499b0b1cd98fe768fecb5fa3620e5", CompatibilityIDOnlyDrift),
		observation("schemas/v1/harness-pack-manifest.schema.json", legacyPinned("schemas/v1/harness-pack-manifest.schema.json"), "reconc-v0.9.2", "9a58ba1f66c6d6ffe1c52188ebfb0fa1f5feb35a4e6585a4267dfc61911f0dbb", CompatibilityIDOnlyDrift),
		observation("schemas/v1/installation-receipt.schema.json", legacyPinned("schemas/v1/installation-receipt.schema.json"), "reconc-v0.9.2", "76b4a54610c4473894f168ab623a6e1aa50c145ba4b6e8d8f829a8aaca697687", CompatibilityIDOnlyDrift),
		observation("schemas/v1/neutral-hook-request.schema.json", "https://reconc.dev/schemas/neutral-hook-request/v1", "reconc-v0.9.3", "32f74a432bdddfa624e9dd0c99ebb75938ef03fd55586dd817a09566b916ec0b", CompatibilityUnreachableClaimedHost),
		observation("schemas/v1/neutral-hook-response.schema.json", "https://reconc.dev/schemas/neutral-hook-response/v1", "reconc-v0.9.3", "7805e58e63f5a20bca8d1f0eb99ad22143d0b5f39b41d77bb4f70c32bd220667", CompatibilityUnreachableClaimedHost),
		observation("schemas/v1/policy-config.schema.json", legacyPinned("schemas/v1/policy-config.schema.json"), "reconc-v0.9.4", "52cc057e0c898b3a178d0d2dacee306cfd33c4b74c794161c7aa29ed60f1cf7e", CompatibilitySemanticDrift),
		observation("schemas/v1/policy-fix-plan.schema.json", legacyPinned("schemas/v1/policy-fix-plan.schema.json"), "reconc-v0.9.2", "7865122e5eb3828f31c05d068474b715216c5f238b238dfcc842fe0945b7bcaf", CompatibilityIDOnlyDrift),
		observation("schemas/v1/policy-lock.schema.json", legacyPinned("schemas/v1/policy-lock.schema.json"), "reconc-v0.9.2", "5e5fd7b5840dddd9426a75936d7bd8946ff670c97dab49a5be41ff46209970c3", CompatibilityIDOnlyDrift),
		observation("schemas/v1/policy-report.schema.json", legacyPinned("schemas/v1/policy-report.schema.json"), "reconc-v0.9.2", "d12e2b25e4288e743555b2f086c2dc3c5a5216342e70a0f6391172d7c86a46f9", CompatibilityIDOnlyDrift),
		observation("schemas/v1/proof-bundle.schema.json", legacyPinned("schemas/v1/proof-bundle.schema.json"), "reconc-v0.9.2", "0c5c85fee98cc3f56b14e3bbfc190b6be977f58511b1e5d98d31895570e1644b", CompatibilityIDOnlyDrift),
		observation("schemas/v1/release-manifest.schema.json", legacyPinned("schemas/v1/release-manifest.schema.json"), "reconc-v0.9.2", "cdd958f6eb1a65e1e8139887230cc5f163a42c58cc9675bd963b9881e860da04", CompatibilityIDOnlyDrift),
		observation("schemas/v1/repository-install.schema.json", legacyPinned("schemas/v1/repository-install.schema.json"), "reconc-v0.9.2", "643f5c9bc87a3c565ab12466e1e9d59aa278ee4e7ade785d7b15bc654c3cd125", CompatibilityIDOnlyDrift),
		observation("schemas/v1/repository-sync-plan.schema.json", legacyPinned("schemas/v1/repository-sync-plan.schema.json"), "reconc-v0.9.2", "0f150f0663d9d99a52b5468e9914dc9532eb3561c4ddf7ddabc61b570d923eaf", CompatibilitySemanticDrift),
		observation("schemas/v1/repository-sync-report.schema.json", legacyPinned("schemas/v1/repository-sync-report.schema.json"), "reconc-v0.9.2", "fcfed1219105ad14384c02be53696dbc8a0aaf174760ae0b60566344ca01b1a9", CompatibilitySemanticDrift),
		observation("schemas/v2/policy-lock.schema.json", legacyPinned("schemas/v2/policy-lock.schema.json"), "reconc-v0.9.3", "b4d330a204af12e33974d635760a53a05cdb4089254af45627f50ecf5c760443", CompatibilitySemanticDrift),
		observation("schemas/v3/policy-lock.schema.json", legacyPinned("schemas/v3/policy-lock.schema.json"), "reconc-v0.9.3", "95362c1fefcb55d882d560d7b4fb16e20a2000858363f6934da4beb0fb772e1b", CompatibilityAbsentAtClaimedTag),
		observation("schemas/v4/policy-lock.schema.json", LegacyPolicyLockV4URL, "reconc-v0.9.4", "32f16bde36b7e8e5d0671c1e3f8bcbf35f810ad7699d93291d1ebb29831b3450", CompatibilityByteIdentical),
	}
}

func observation(localPath string, claimedURL string, firstExactTag string, digest string, compatibility Compatibility) Observation {
	return Observation{
		LocalPath: localPath, ClaimedURL: claimedURL, FirstExactTag: firstExactTag,
		SHA256: digest, Compatibility: compatibility,
	}
}

func contract(artifact Artifact, schemaVersion string, formatVersions []string, localPath string, releaseAsset string, defaultURL string, introductionTag string, digest string, state State, aliases ...Alias) Contract {
	return Contract{
		Artifact: artifact, SchemaVersion: schemaVersion,
		FormatVersions: formatVersions, LocalPath: localPath,
		ReleaseAsset: releaseAsset, DefaultURL: defaultURL,
		EnterprisePath:  "/schemas/" + string(artifact) + "/v" + schemaVersion,
		IntroductionTag: introductionTag, SHA256: digest,
		State: state, Aliases: aliases,
	}
}

func portableContract(contract Contract) Contract {
	contract.PortableDefault = true
	return contract
}

func unpinned(localPath string) Alias {
	return Alias{URL: "https://raw.githubusercontent.com/Christopher-Schulze/reconc/main/" + localPath, Reason: AliasUnpinnedLegacy}
}

func misbound(localPath string) Alias {
	return Alias{URL: legacyPinned(localPath), Reason: AliasMisboundReleaseTag}
}

func unreachable(schemaURL string) Alias {
	return Alias{URL: schemaURL, Reason: AliasUnreachableHost}
}

func legacyPinned(localPath string) string {
	return "https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.1/" + localPath
}

func resolveContract(contract Contract) string {
	if contract.PortableDefault {
		return contract.DefaultURL
	}
	return enterpriseContractURL(contract)
}

func enterpriseContractURL(contract Contract) string {
	base := strings.TrimRight(os.Getenv("RECONC_SCHEMA_BASE_URL"), "/")
	if base == "" {
		return contract.DefaultURL
	}
	return base + contract.EnterprisePath
}
