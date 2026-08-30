// Package cireport renders provider-neutral policy decisions as bounded
// SARIF 2.1.0, JUnit XML, and GitHub summary artifacts.
package cireport

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/impactlab"
	"reconc.dev/reconc/internal/runtime"
)

const (
	FormatVersion = "reconc-ci-report/v1"
	MaxBytes      = 8 << 20
	maxFindings   = 1024
	maxPaths      = 128
)

// Format selects one CI-native report serialization.
type Format string

const (
	FormatSARIF  Format = "sarif"
	FormatJUnit  Format = "junit"
	FormatGitHub Format = "github"
)

// Candidate identifies the evaluated state without exposing its host path.
type Candidate struct {
	Fingerprint     string `json:"fingerprint,omitempty"`
	PolicyLockHash  string `json:"policy_lock_hash,omitempty"`
	WorktreeHash    string `json:"worktree_hash,omitempty"`
	GitAvailable    bool   `json:"git_available"`
	WorktreeTrusted bool   `json:"worktree_trusted"`
	DirtyPathCount  int    `json:"dirty_path_count"`
}

// Git identifies the Git-derived input range used by reconc ci.
type Git struct {
	Mode           string `json:"mode"`
	Base           string `json:"base,omitempty"`
	Head           string `json:"head,omitempty"`
	WritePathCount int    `json:"write_path_count"`
}

// Finding is the provider-independent representation of one violation.
type Finding struct {
	RuleID         string
	Kind           string
	Mode           string
	Level          string
	Message        string
	Remediation    string
	SourcePath     string
	Paths          []string
	OmittedPaths   int
	CaseID         string
	DeltaKind      string
	Current        string
	Candidate      string
	ReviewRequired bool
	Reviewed       bool
}

// FromImpact converts one privacy-bounded policy-impact report into the shared
// CI-native model. An unreviewed permission or block delta is an error; other
// changes remain review-visible without inventing a failure.
func FromImpact(version string, report impactlab.Report) Model {
	decision := "pass"
	if !report.DeltaGate.Passed {
		decision = "block"
	} else if impactChanged(report) {
		decision = "warn"
	}
	model := Model{
		FormatVersion: FormatVersion, Command: "impact", ToolVersion: cleanText(version),
		Decision: decision, ExitCode: decisionExitCode(decision),
		Summary: cleanText(fmt.Sprintf(
			"%s Action delta gate: reviewed %d/%d; unreviewed %d.",
			report.SafetyConclusion, report.DeltaGate.ReviewedCount,
			report.DeltaGate.RequiredCount, len(report.DeltaGate.UnreviewedCases),
		)),
		Candidate: Candidate{
			Fingerprint:    report.Candidate.ActionPlanIdentity,
			PolicyLockHash: report.Candidate.LockDigest, GitAvailable: false,
			WorktreeTrusted: false,
		},
		Findings: []Finding{},
	}
	appendFinding := func(finding Finding) {
		var truncated bool
		model.Findings, truncated = appendBoundedFinding(model.Findings, finding)
		if truncated {
			model.TruncatedFindings++
		}
	}
	for _, comparison := range report.Cases {
		if comparison.Action != nil {
			if len(comparison.Action.Deltas) == 0 {
				appendFinding(Finding{
					RuleID: "reconc/impact/action-case", Kind: "action_impact", Mode: "unchanged", Level: "note",
					Message: "Action policy outcome is unchanged for " + comparison.ID + ".",
					CaseID:  comparison.ID, Current: string(comparison.Action.Current.Outcome.Decision),
					Candidate: string(comparison.Action.Candidate.Outcome.Decision), Paths: []string{},
				})
			}
			for _, delta := range comparison.Action.Deltas {
				reviewRequired := delta == impactlab.DeltaNewlyAllowed || delta == impactlab.DeltaNewlyBlocked
				level := "warning"
				if reviewRequired && !comparison.Action.Reviewed {
					level = "error"
				}
				appendFinding(Finding{
					RuleID: "reconc/impact/" + string(delta), Kind: "action_impact", Mode: string(delta),
					Level: level, Message: "Action policy delta " + string(delta) + " for " + comparison.ID + ".",
					CaseID: comparison.ID, DeltaKind: string(delta),
					Current:        string(comparison.Action.Current.Outcome.Decision),
					Candidate:      string(comparison.Action.Candidate.Outcome.Decision),
					ReviewRequired: reviewRequired,
					Reviewed:       reviewRequired && comparison.Action.Reviewed,
					Paths:          []string{},
				})
			}
		}
		if comparison.Repository != nil {
			if !comparison.Repository.DecisionChanged && !comparison.Repository.ActionChanged {
				appendFinding(Finding{
					RuleID: "reconc/impact/repository-case", Kind: "repository_impact", Mode: "unchanged", Level: "note",
					Message: "Repository policy outcome is unchanged for " + comparison.ID + ".",
					CaseID:  comparison.ID, Current: string(comparison.Repository.CurrentDecision),
					Candidate: string(comparison.Repository.CandidateDecision), Paths: []string{},
				})
			}
			if comparison.Repository.DecisionChanged {
				appendFinding(Finding{
					RuleID: "reconc/impact/repository-decision", Kind: "repository_impact",
					Mode: "decision_changed", Level: "warning",
					Message: "Repository policy decision changed for " + comparison.ID + ".",
					CaseID:  comparison.ID, DeltaKind: "repository_decision",
					Current:   string(comparison.Repository.CurrentDecision),
					Candidate: string(comparison.Repository.CandidateDecision), Paths: []string{},
				})
			}
			if comparison.Repository.ActionChanged {
				appendFinding(Finding{
					RuleID: "reconc/impact/repository-action", Kind: "repository_impact",
					Mode: "action_changed", Level: "warning",
					Message: "Repository remediation actions changed for " + comparison.ID + ".",
					CaseID:  comparison.ID, DeltaKind: "repository_action",
					Current:   string(comparison.Repository.CurrentDecision),
					Candidate: string(comparison.Repository.CandidateDecision), Paths: []string{},
				})
			}
		}
	}
	sortFindings(model.Findings)
	return model
}

func impactChanged(report impactlab.Report) bool {
	return report.Summary.DecisionChanges > 0 || report.Summary.ActionChanges > 0 ||
		report.Summary.ActionDecisionChanges > 0 ||
		report.Summary.ActionRuleTraceChanges > 0 || report.Summary.ActionCacheChanges > 0 ||
		report.Summary.ActionPhaseOutcomeChanges > 0 || report.Summary.ActionCompletenessChanges > 0 ||
		report.Summary.ActionReasonChanges > 0 || report.Summary.ActionToolIdentityChanges > 0 ||
		report.Summary.ActionFailureChanges > 0 || report.Summary.ActionApprovalChanges > 0
}

// Model is the complete deterministic input shared by both renderers.
type Model struct {
	FormatVersion     string
	Command           string
	ToolVersion       string
	Decision          string
	ExitCode          int
	Summary           string
	Candidate         Candidate
	Git               *Git
	Findings          []Finding
	OperationalError  string
	TruncatedFindings int
}

// FromCheck converts one completed policy report into the neutral model.
func FromCheck(command, version string, candidate Candidate, git *Git, report *runtime.CheckReport) Model {
	model := Model{
		FormatVersion: FormatVersion, Command: cleanText(command), ToolVersion: cleanText(version),
		Candidate: cleanCandidate(candidate), Git: cleanGit(git), Findings: []Finding{},
	}
	if report == nil {
		model.OperationalError = "policy evaluation returned no report"
		return model
	}
	model.Decision, model.Summary = string(report.Decision), cleanText(report.Summary)
	model.ExitCode = decisionExitCode(model.Decision)
	model.Findings = make([]Finding, 0, min(len(report.Violations), maxFindings))
	for _, violation := range report.Violations {
		if len(model.Findings) == maxFindings && !violation.IsBlocking() {
			model.TruncatedFindings++
			continue
		}
		var truncated bool
		model.Findings, truncated = appendBoundedFinding(model.Findings, findingFromViolation(violation))
		if truncated {
			model.TruncatedFindings++
		}
	}
	sortFindings(model.Findings)
	return model
}

func appendBoundedFinding(findings []Finding, finding Finding) ([]Finding, bool) {
	if len(findings) < maxFindings {
		return append(findings, finding), false
	}
	if finding.Level == "error" {
		for index := len(findings) - 1; index >= 0; index-- {
			if findings[index].Level != "error" {
				findings[index] = finding
				break
			}
		}
	}
	return findings, true
}

// Operational builds a machine-readable failed invocation without leaking a
// local repository or home path.
func Operational(command, version, repo string, candidate Candidate, exitCode int, err error) Model {
	detail := "unknown operational error"
	if err != nil {
		detail = redactHostText(err.Error(), repo)
	}
	return Model{
		FormatVersion: FormatVersion, Command: cleanText(command), ToolVersion: cleanText(version),
		Decision: "error", ExitCode: exitCode, Summary: "Reconc could not complete policy evaluation.",
		Candidate: cleanCandidate(candidate), Findings: []Finding{}, OperationalError: detail,
	}
}

func findingFromViolation(violation runtime.Violation) Finding {
	paths, omittedPaths := cleanPaths(violation.MatchedPaths)
	return Finding{
		RuleID: cleanText(violation.RuleID), Kind: cleanText(string(violation.Kind)),
		Mode: cleanText(string(violation.Mode)), Level: levelForMode(string(violation.Mode)),
		Message: cleanText(violation.Message), Remediation: cleanText(violation.RecommendedAction),
		SourcePath: cleanRelativePath(violation.SourcePath), Paths: paths, OmittedPaths: omittedPaths,
	}
}

func cleanCandidate(candidate Candidate) Candidate {
	candidate.Fingerprint = cleanText(candidate.Fingerprint)
	candidate.PolicyLockHash = cleanText(candidate.PolicyLockHash)
	candidate.WorktreeHash = cleanText(candidate.WorktreeHash)
	if candidate.DirtyPathCount < 0 {
		candidate.DirtyPathCount = 0
	}
	return candidate
}

func cleanGit(git *Git) *Git {
	if git == nil {
		return nil
	}
	copy := *git
	copy.Mode, copy.Base, copy.Head = cleanText(copy.Mode), cleanText(copy.Base), cleanText(copy.Head)
	if copy.WritePathCount < 0 {
		copy.WritePathCount = 0
	}
	return &copy
}

func sortFindings(findings []Finding) {
	sort.Slice(findings, func(left, right int) bool {
		a, b := findings[left], findings[right]
		return strings.Join([]string{a.CaseID, a.RuleID, a.Mode, a.Message, strings.Join(a.Paths, "\x00")}, "\x00") <
			strings.Join([]string{b.CaseID, b.RuleID, b.Mode, b.Message, strings.Join(b.Paths, "\x00")}, "\x00")
	})
}

func levelForMode(mode string) string {
	switch mode {
	case "block", "fix":
		return "error"
	case "warn":
		return "warning"
	default:
		return "note"
	}
}

func redactHostText(value, repo string) string {
	replacements := []string{repo}
	if absolute, err := filepath.Abs(repo); err == nil {
		replacements = append(replacements, absolute)
	}
	if home, err := os.UserHomeDir(); err == nil {
		replacements = append(replacements, home)
	}
	for _, private := range replacements {
		if private != "" && private != "." && private != string(filepath.Separator) {
			value = strings.ReplaceAll(value, private, ".")
		}
	}
	return cleanText(redactAbsoluteTokens(value))
}

func redactAbsoluteTokens(value string) string {
	fields := strings.Fields(value)
	for index, field := range fields {
		if start := absolutePathStart(field); start >= 0 {
			prefix := field[:start]
			suffix := strings.TrimRight(field[start:], "\"'`()[]{}<>,;:")
			fields[index] = prefix + strings.Replace(field[start:], suffix, "<path>", 1)
		}
	}
	return strings.Join(fields, " ")
}

func absolutePathStart(value string) int {
	for index := range value {
		if value[index] == '/' {
			return index
		}
		if index+2 < len(value) && value[index+1] == ':' &&
			(value[index+2] == '\\' || value[index+2] == '/') {
			return index
		}
	}
	return -1
}

func validateModel(model Model) error {
	if model.FormatVersion != FormatVersion {
		return fmt.Errorf("unsupported CI report model %q", model.FormatVersion)
	}
	if model.Command == "" || model.ToolVersion == "" {
		return fmt.Errorf("CI report tool identity is incomplete")
	}
	if model.OperationalError == "" && model.Decision != "pass" && model.Decision != "warn" && model.Decision != "block" {
		return fmt.Errorf("CI report decision %q is invalid", model.Decision)
	}
	return nil
}
