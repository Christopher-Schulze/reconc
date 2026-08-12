// Package schema owns the canonical locations of Reconc's public JSON
// contracts and the enterprise base-URL override.
package schema

// CurrentSchemaTag is the explicitly authorized immutable publication tag for
// repaired and newly versioned schema contracts.
const CurrentSchemaTag = "reconc-v0.9.6"

const DefaultBaseURL = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/" + CurrentSchemaTag + "/schemas/v1"

const Version2BaseURL = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/" + CurrentSchemaTag + "/schemas/v2"

const Version3BaseURL = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/" + CurrentSchemaTag + "/schemas/v3"

const Version4BaseURL = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/" + CurrentSchemaTag + "/schemas/v4"

const Version5BaseURL = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/" + CurrentSchemaTag + "/schemas/v5"

const Version6BaseURL = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/" + CurrentSchemaTag + "/schemas/v6"

const PolicyLockBaseURL = Version6BaseURL

// Artifact identifies one stable JSON contract emitted by Reconc.
type Artifact string

const (
	ActionLedger                Artifact = "action-ledger"
	ActionControlMap            Artifact = "action-control-map"
	ActionControlMapSignature   Artifact = "action-control-map-signature"
	ActionControlMapAuthorities Artifact = "action-control-map-authorities"
	ActionEvidence              Artifact = "action-evidence"
	PolicyLock                  Artifact = "policy-lock"
	PolicyConfig                Artifact = "policy-config"
	PolicyReport                Artifact = "policy-report"
	PolicyFixPlan               Artifact = "policy-fix-plan"
	CompletionReport            Artifact = "completion-report"
	ProofBundle                 Artifact = "proof-bundle"
	InstallationReceipt         Artifact = "installation-receipt"
	GlobalDiagnostic            Artifact = "global-diagnostic"
	GlobalLifecycle             Artifact = "global-lifecycle"
	HarnessPackManifest         Artifact = "harness-pack-manifest"
	RepositoryInstall           Artifact = "repository-install"
	RepositorySyncPlan          Artifact = "repository-sync-plan"
	RepositorySyncReport        Artifact = "repository-sync-report"
	ReleaseManifest             Artifact = "release-manifest"
	CustomRuntimeManifest       Artifact = "custom-runtime-manifest"
	CustomRuntimeLiveness       Artifact = "custom-runtime-liveness"
	CustomRuntimeConformance    Artifact = "custom-runtime-conformance"
	NeutralHookRequest          Artifact = "neutral-hook-request"
	NeutralHookResponse         Artifact = "neutral-hook-response"

	ActionLedgerURL                = Version2BaseURL + "/action-ledger.schema.json"
	ActionControlMapURL            = DefaultBaseURL + "/action-control-map.schema.json"
	ActionControlMapSignatureURL   = DefaultBaseURL + "/action-control-map-signature.schema.json"
	ActionControlMapAuthoritiesURL = DefaultBaseURL + "/action-control-map-authorities.schema.json"
	ActionEvidenceURL              = DefaultBaseURL + "/action-evidence.schema.json"
	LegacyPolicyLockURL            = DefaultBaseURL + "/policy-lock.schema.json"
	PolicyLockURL                  = PolicyLockBaseURL + "/policy-lock.schema.json"
	PolicyConfigURL                = Version4BaseURL + "/policy-config.schema.json"
	PolicyReportURL                = DefaultBaseURL + "/policy-report.schema.json"
	PolicyFixPlanURL               = DefaultBaseURL + "/policy-fix-plan.schema.json"
	CompletionReportURL            = DefaultBaseURL + "/completion-report.schema.json"
	ProofBundleURL                 = DefaultBaseURL + "/proof-bundle.schema.json"
	InstallationReceiptURL         = DefaultBaseURL + "/installation-receipt.schema.json"
	GlobalDiagnosticURL            = DefaultBaseURL + "/global-diagnostic.schema.json"
	GlobalLifecycleURL             = DefaultBaseURL + "/global-lifecycle.schema.json"
	HarnessPackManifestURL         = DefaultBaseURL + "/harness-pack-manifest.schema.json"
	RepositoryInstallURL           = DefaultBaseURL + "/repository-install.schema.json"
	RepositorySyncPlanURL          = Version2BaseURL + "/repository-sync-plan.schema.json"
	RepositorySyncReportURL        = Version2BaseURL + "/repository-sync-report.schema.json"
	ReleaseManifestURL             = DefaultBaseURL + "/release-manifest.schema.json"
	CustomRuntimeManifestURL       = Version2BaseURL + "/custom-runtime-manifest.schema.json"
	CustomRuntimeLivenessURL       = DefaultBaseURL + "/custom-runtime-liveness.schema.json"
	CustomRuntimeConformanceURL    = DefaultBaseURL + "/custom-runtime-conformance.schema.json"
	NeutralHookRequestURL          = DefaultBaseURL + "/neutral-hook-request.schema.json"
	NeutralHookResponseURL         = DefaultBaseURL + "/neutral-hook-response.schema.json"

	LegacyPolicyLockURLUnpinned   = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/main/schemas/v1/policy-lock.schema.json"
	LegacyPolicyLockV2URL         = Version2BaseURL + "/policy-lock.schema.json"
	LegacyPolicyLockV2URLUnpinned = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/main/schemas/v2/policy-lock.schema.json"
	LegacyPolicyLockV3URL         = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/" + CurrentSchemaTag + "/schemas/v3/policy-lock.schema.json"
	LegacyPolicyLockV3URLUnpinned = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/main/schemas/v3/policy-lock.schema.json"
	LegacyPolicyLockV4URL         = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.4/schemas/v4/policy-lock.schema.json"
	LegacyPolicyLockV5URL         = Version5BaseURL + "/policy-lock.schema.json"

	LegacyPolicyLockURLV091   = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.1/schemas/v1/policy-lock.schema.json"
	LegacyPolicyLockV2URLV091 = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.1/schemas/v2/policy-lock.schema.json"
	LegacyPolicyLockV3URLV091 = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.1/schemas/v3/policy-lock.schema.json"
)

// DefaultURL returns the repository-hosted, format-versioned schema URL.
func DefaultURL(artifact Artifact) string {
	contract, ok := CurrentContract(artifact)
	if !ok {
		return ""
	}
	return contract.DefaultURL
}

// Resolve returns the schema URL to stamp on a newly emitted artifact.
// Enterprise mirrors use the exact per-contract path owned by the registry.
func Resolve(artifact Artifact) string {
	contract, ok := CurrentContract(artifact)
	if !ok {
		return ""
	}
	return resolveContract(contract)
}
