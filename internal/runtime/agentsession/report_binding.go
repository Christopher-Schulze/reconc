package agentsession

import (
	"fmt"
	"strings"

	"reconc.dev/reconc/internal/runtime"
)

// SessionReportBindingStatus describes whether a saved session report can
// contribute current remediation to a read-only briefing.
type SessionReportBindingStatus string

const (
	SessionReportCurrent     SessionReportBindingStatus = "current"
	SessionReportHistorical  SessionReportBindingStatus = "historical"
	SessionReportUnavailable SessionReportBindingStatus = "unavailable"
)

// SessionReportBinding contains the exact identities observed while
// classifying one saved report. The candidate fingerprint is the identity
// published with the report by the stable Stop-policy path; it is empty for
// legacy or otherwise unbound reports.
type SessionReportBinding struct {
	Status               SessionReportBindingStatus
	Reason               string
	ReportHash           string
	EvidenceHash         string
	CandidateFingerprint string
}

// InspectSessionReportBinding classifies a decoded report without evaluating
// policy or mutating repository/session state. A current report must carry the
// stable Stop-policy report, evidence, and candidate bindings and must still
// match the current bounded repository fingerprint. Reports without those
// bindings remain readable as historical evidence.
func InspectSessionReportBinding(repoRoot string, state SessionState, report *runtime.CheckReport) (SessionReportBinding, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return SessionReportBinding{}, err
	}
	binding := SessionReportBinding{
		Status:               SessionReportUnavailable,
		CandidateFingerprint: strings.TrimSpace(state.StopPolicyFingerprint),
	}
	if report == nil {
		binding.Reason = "saved report is missing"
		return binding, nil
	}
	reportHash, err := hashCheckReport(report)
	if err != nil {
		return SessionReportBinding{}, fmt.Errorf("hash saved policy report: %w", err)
	}
	binding.ReportHash = reportHash
	if strings.TrimSpace(report.RepoRoot) == "" {
		binding.Reason = "saved report has no repository root"
		return binding, nil
	}
	reportRoot, rootErr := ResolveRepoRoot(report.RepoRoot)
	if rootErr != nil || reportRoot != root {
		binding.Reason = "saved report belongs to a different repository"
		return binding, nil
	}

	evidenceHash, err := stopPolicyEvidenceHash(state)
	if err != nil {
		return SessionReportBinding{}, err
	}
	binding.EvidenceHash = evidenceHash
	if strings.TrimSpace(state.StopPolicyReportHash) == "" {
		binding.Status = SessionReportHistorical
		binding.Reason = "saved report has no stable Stop-policy binding"
		return binding, nil
	}
	if state.StopPolicyReportHash != reportHash {
		binding.Status = SessionReportHistorical
		binding.Reason = "saved report hash does not match its session binding"
		return binding, nil
	}
	if strings.TrimSpace(state.StopPolicyEvidenceHash) == "" {
		binding.Status = SessionReportHistorical
		binding.Reason = "saved report has no evidence binding"
		return binding, nil
	}
	if state.StopPolicyEvidenceHash != evidenceHash {
		binding.Status = SessionReportHistorical
		binding.Reason = "active session evidence changed after the saved report"
		return binding, nil
	}
	if binding.CandidateFingerprint == "" {
		binding.Status = SessionReportHistorical
		binding.Reason = "saved report has no candidate binding"
		return binding, nil
	}
	if stopPolicyReportExpired(state.StopPolicyExpiresAt) {
		binding.Status = SessionReportHistorical
		binding.Reason = "saved report expired"
		return binding, nil
	}
	currentFingerprint := stopPolicyFingerprint(root, state)
	if strings.HasPrefix(currentFingerprint, "error:") {
		binding.Status = SessionReportHistorical
		binding.Reason = "current candidate fingerprint is unavailable"
		return binding, nil
	}
	if currentFingerprint != binding.CandidateFingerprint {
		binding.Status = SessionReportHistorical
		binding.Reason = "repository candidate changed after the saved report"
		return binding, nil
	}
	binding.Status = SessionReportCurrent
	return binding, nil
}
