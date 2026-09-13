// Package schema owns the canonical locations of Reconc's public JSON
// contracts and the enterprise base-URL override.
package schema

// PreviousSchemaTag identifies the last published source release whose
// unchanged schema contracts remain canonical compatibility inputs and
// outputs.
const PreviousSchemaTag = "reconc-v0.9.6"

const DefaultBaseURL = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/" + PreviousSchemaTag + "/schemas/v1"

const Version2BaseURL = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/" + PreviousSchemaTag + "/schemas/v2"

const Version3BaseURL = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/" + PreviousSchemaTag + "/schemas/v3"

const Version4BaseURL = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/" + PreviousSchemaTag + "/schemas/v4"

const Version5BaseURL = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/" + PreviousSchemaTag + "/schemas/v5"

// Artifact identifies one stable JSON contract emitted by Reconc.
type Artifact string

const (
	ActionLedger                Artifact = "action-ledger"
	ActionControlMap            Artifact = "action-control-map"
	ActionControlMapSignature   Artifact = "action-control-map-signature"
	ActionControlMapAuthorities Artifact = "action-control-map-authorities"
	ActionEvidence              Artifact = "action-evidence"
	CIEvidence                  Artifact = "ci-evidence"
	CIRequirement               Artifact = "ci-requirement"
	CIStatement                 Artifact = "ci-statement"
	CIVerification              Artifact = "ci-verification"
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
	CIEvidenceURL                  = "urn:reconc:schema:ci-evidence:v1:sha256:fbad8b6936a64d2839151cbe44d6fbadacaf3dadf51bac9d686f09ec911a85b3"
	CIRequirementURL               = "urn:reconc:schema:ci-requirement:v1:sha256:9331a35a551a2bc4ab76758c173f57e6fffbb67368d5edb1c7e106c0ae2edfa8"
	CIStatementURL                 = "urn:reconc:schema:ci-statement:v1:sha256:0bafe23b80bb194315766f0a9612f780a6b31e24c1b9b0a0d58e18e568ded7d9"
	CIVerificationURL              = "urn:reconc:schema:ci-verification:v1:sha256:82a55a94302df2678d2b145ca9cb0b370e92a04ba0591d3f2186658213b3c57e"
	LegacyPolicyLockURL            = DefaultBaseURL + "/policy-lock.schema.json"
	PolicyLockURL                  = "urn:reconc:schema:policy-lock:v6:sha256:c2634f6083726b5563de867e4d4ffee325f9336f5dd99bbb6f705223e014cec7"
	PreviousPolicyLockV6URL        = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/" + PreviousSchemaTag + "/schemas/v6/policy-lock.schema.json"
	PolicyLockV6URLV097            = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.7/schemas/v6/policy-lock.schema.json"
	PolicyConfigURL                = "urn:reconc:schema:policy-config:v4:sha256:7ee24299b5a1b9d270aa2b5bd75603c25a91711b01a88224a14c664df817d0db"
	PreviousPolicyConfigV4URL      = Version4BaseURL + "/policy-config.schema.json"
	PolicyReportURL                = DefaultBaseURL + "/policy-report.schema.json"
	PolicyFixPlanV1URL             = DefaultBaseURL + "/policy-fix-plan.schema.json"
	PolicyFixPlanURL               = "urn:reconc:schema:policy-fix-plan:v2:sha256:851c5fee27f7392b2129084d59788e0c844233d3b7f73bb7023945672b47c14f"
	CompletionReportURL            = DefaultBaseURL + "/completion-report.schema.json"
	ProofBundleURL                 = DefaultBaseURL + "/proof-bundle.schema.json"
	InstallationReceiptURL         = "urn:reconc:schema:installation-receipt:v2:sha256:2a5259b3820e737b64ddbf77cbe57be2b904d1924a114fcb67ba0726a94c002d"
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
	LegacyPolicyLockV3URL         = Version3BaseURL + "/policy-lock.schema.json"
	LegacyPolicyLockV3URLUnpinned = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/main/schemas/v3/policy-lock.schema.json"
	LegacyPolicyLockV4URL         = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.4/schemas/v4/policy-lock.schema.json"
	LegacyPolicyLockV5URL         = Version5BaseURL + "/policy-lock.schema.json"

	LegacyPolicyLockURLV091   = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.1/schemas/v1/policy-lock.schema.json"
	LegacyPolicyLockV2URLV091 = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.1/schemas/v2/policy-lock.schema.json"
	LegacyPolicyLockV3URLV091 = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.1/schemas/v3/policy-lock.schema.json"
)

// DefaultURL returns the registered, format-versioned schema identity.
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
