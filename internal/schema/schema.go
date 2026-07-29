// Package schema owns the canonical locations of Reconc's public JSON
// contracts and the enterprise base-URL override.
package schema

import (
	"os"
	"strings"
)

// DefaultBaseURL is the release-tagged repository location for Reconc's
// immutable format-1 contracts. Policy lockfiles evolve independently.
const DefaultBaseURL = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.1/schemas/v1"

const PolicyLockBaseURL = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.1/schemas/v3"

// Artifact identifies one stable JSON contract emitted by Reconc.
type Artifact string

const (
	PolicyLock           Artifact = "policy-lock"
	PolicyConfig         Artifact = "policy-config"
	PolicyReport         Artifact = "policy-report"
	PolicyFixPlan        Artifact = "policy-fix-plan"
	CompletionReport     Artifact = "completion-report"
	ProofBundle          Artifact = "proof-bundle"
	InstallationReceipt  Artifact = "installation-receipt"
	GlobalDiagnostic     Artifact = "global-diagnostic"
	GlobalLifecycle      Artifact = "global-lifecycle"
	HarnessPackManifest  Artifact = "harness-pack-manifest"
	RepositoryInstall    Artifact = "repository-install"
	RepositorySyncPlan   Artifact = "repository-sync-plan"
	RepositorySyncReport Artifact = "repository-sync-report"
	ReleaseManifest      Artifact = "release-manifest"

	LegacyPolicyLockURL     = DefaultBaseURL + "/policy-lock.schema.json"
	PolicyLockURL           = PolicyLockBaseURL + "/policy-lock.schema.json"
	PolicyConfigURL         = DefaultBaseURL + "/policy-config.schema.json"
	PolicyReportURL         = DefaultBaseURL + "/policy-report.schema.json"
	PolicyFixPlanURL        = DefaultBaseURL + "/policy-fix-plan.schema.json"
	CompletionReportURL     = DefaultBaseURL + "/completion-report.schema.json"
	ProofBundleURL          = DefaultBaseURL + "/proof-bundle.schema.json"
	InstallationReceiptURL  = DefaultBaseURL + "/installation-receipt.schema.json"
	GlobalDiagnosticURL     = DefaultBaseURL + "/global-diagnostic.schema.json"
	GlobalLifecycleURL      = DefaultBaseURL + "/global-lifecycle.schema.json"
	HarnessPackManifestURL  = DefaultBaseURL + "/harness-pack-manifest.schema.json"
	RepositoryInstallURL    = DefaultBaseURL + "/repository-install.schema.json"
	RepositorySyncPlanURL   = DefaultBaseURL + "/repository-sync-plan.schema.json"
	RepositorySyncReportURL = DefaultBaseURL + "/repository-sync-report.schema.json"
	ReleaseManifestURL      = DefaultBaseURL + "/release-manifest.schema.json"

	LegacyPolicyLockURLUnpinned   = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/main/schemas/v1/policy-lock.schema.json"
	LegacyPolicyLockV2URL         = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.1/schemas/v2/policy-lock.schema.json"
	LegacyPolicyLockV2URLUnpinned = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/main/schemas/v2/policy-lock.schema.json"
)

// DefaultURL returns the repository-hosted, format-versioned schema URL.
func DefaultURL(artifact Artifact) string {
	switch artifact {
	case PolicyLock:
		return PolicyLockURL
	case PolicyConfig:
		return PolicyConfigURL
	case PolicyReport:
		return PolicyReportURL
	case PolicyFixPlan:
		return PolicyFixPlanURL
	case CompletionReport:
		return CompletionReportURL
	case ProofBundle:
		return ProofBundleURL
	case InstallationReceipt:
		return InstallationReceiptURL
	case GlobalDiagnostic:
		return GlobalDiagnosticURL
	case GlobalLifecycle:
		return GlobalLifecycleURL
	case HarnessPackManifest:
		return HarnessPackManifestURL
	case RepositoryInstall:
		return RepositoryInstallURL
	case RepositorySyncPlan:
		return RepositorySyncPlanURL
	case RepositorySyncReport:
		return RepositorySyncReportURL
	case ReleaseManifest:
		return ReleaseManifestURL
	default:
		return ""
	}
}

// Resolve returns the schema URL to stamp on a newly emitted artifact.
// Enterprise mirrors keep the historical /schemas/<artifact>/v1 layout.
func Resolve(artifact Artifact) string {
	base := strings.TrimRight(os.Getenv("RECONC_SCHEMA_BASE_URL"), "/")
	if base == "" {
		return DefaultURL(artifact)
	}
	version := "v1"
	if artifact == PolicyLock {
		version = "v3"
	}
	return base + "/schemas/" + string(artifact) + "/" + version
}
