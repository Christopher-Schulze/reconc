// Package cireport renders provider-neutral policy decisions as CI-native
// SARIF 2.1.0 and JUnit XML artifacts.
package cireport

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
	FormatSARIF Format = "sarif"
	FormatJUnit Format = "junit"
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
	RuleID       string
	Kind         string
	Mode         string
	Level        string
	Message      string
	Remediation  string
	SourcePath   string
	Paths        []string
	OmittedPaths int
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
	limit := min(len(report.Violations), maxFindings)
	model.Findings = make([]Finding, 0, limit)
	for _, violation := range report.Violations[:limit] {
		model.Findings = append(model.Findings, findingFromViolation(violation))
	}
	model.TruncatedFindings = len(report.Violations) - limit
	sortFindings(model.Findings)
	return model
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
		return strings.Join([]string{a.RuleID, a.Mode, a.Message, strings.Join(a.Paths, "\x00")}, "\x00") <
			strings.Join([]string{b.RuleID, b.Mode, b.Message, strings.Join(b.Paths, "\x00")}, "\x00")
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
