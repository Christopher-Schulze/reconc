// Package schema owns the canonical locations of Reconc's public JSON
// contracts and the enterprise base-URL override.
package schema

import (
	"os"
	"strings"
)

// DefaultBaseURL is the format-versioned repository location for Reconc's
// public JSON contracts.
const DefaultBaseURL = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/main/schemas/v1"

// Artifact identifies one stable JSON contract emitted by Reconc.
type Artifact string

const (
	PolicyLock    Artifact = "policy-lock"
	PolicyConfig  Artifact = "policy-config"
	PolicyReport  Artifact = "policy-report"
	PolicyFixPlan Artifact = "policy-fix-plan"

	PolicyLockURL    = DefaultBaseURL + "/policy-lock.schema.json"
	PolicyConfigURL  = DefaultBaseURL + "/policy-config.schema.json"
	PolicyReportURL  = DefaultBaseURL + "/policy-report.schema.json"
	PolicyFixPlanURL = DefaultBaseURL + "/policy-fix-plan.schema.json"
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
	return base + "/schemas/" + string(artifact) + "/v1"
}
