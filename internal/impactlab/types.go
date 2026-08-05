// Package impactlab compares current and additive candidate policy decisions
// over explicit privacy-bounded replay evidence.
package impactlab

import "reconc.dev/reconc/internal/runtime"

const (
	CorpusFormatVersion = "reconc-impact-corpus/v1"
	ReportFormatVersion = "reconc-impact-report/v1"
	MaxCorpusBytes      = 8 << 20
	maxCases            = 2048
	maxItemsPerField    = 2048
	maxTotalItems       = 65536
	maxValueBytes       = 4096
	maxCaseIDBytes      = 128
)

// EventClass identifies which evidence surface a capture covered.
type EventClass string

const (
	EventClassRead           EventClass = "read"
	EventClassWrite          EventClass = "write"
	EventClassCommand        EventClass = "command"
	EventClassCommandOutcome EventClass = "command_outcome"
	EventClassClaim          EventClass = "claim"
)

// AllEventClasses returns the canonical replay evidence classes.
func AllEventClasses() []EventClass {
	return []EventClass{
		EventClassRead, EventClassWrite, EventClassCommand,
		EventClassCommandOutcome, EventClassClaim,
	}
}

// Completeness states what was observed, what the capture mechanism covered,
// and what remains unknowable. Redaction always removes completeness.
type Completeness struct {
	ObservedEventClasses []EventClass `json:"observed_event_classes"`
	CompleteEventClasses []EventClass `json:"complete_event_classes"`
	MissingEventClasses  []EventClass `json:"missing_event_classes"`
	RedactedEventClasses []EventClass `json:"redacted_event_classes"`
	RedactionCount       int          `json:"redaction_count"`
	CompleteReplay       bool         `json:"complete_replay"`
}

// Case is one deterministic evidence fixture.
type Case struct {
	ID                   string                  `json:"id"`
	Inputs               runtime.ExecutionInputs `json:"inputs"`
	RedactedEventClasses []EventClass            `json:"redacted_event_classes"`
	RedactionCount       int                     `json:"redaction_count"`
}

// Corpus is the import/export contract for explicit or captured fixtures.
type Corpus struct {
	FormatVersion string       `json:"format_version"`
	CorpusID      string       `json:"corpus_id"`
	Completeness  Completeness `json:"completeness"`
	Cases         []Case       `json:"cases"`
}

// Candidate identifies the additive in-memory candidate without its host path.
type Candidate struct {
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	SourceDigest string `json:"source_digest"`
	RuleCount    int    `json:"rule_count"`
}

// CostDelta compares deterministic structural evaluator work.
type CostDelta struct {
	Current        runtime.EvaluationMetrics `json:"current"`
	Candidate      runtime.EvaluationMetrics `json:"candidate"`
	EstimatedUnits int64                     `json:"estimated_units_delta"`
}

// CaseComparison is the exact current-versus-candidate delta for one fixture.
type CaseComparison struct {
	ID                   string           `json:"id"`
	CurrentDecision      runtime.Decision `json:"current_decision"`
	CandidateDecision    runtime.Decision `json:"candidate_decision"`
	DecisionChanged      bool             `json:"decision_changed"`
	NewlyBlockingRules   []string         `json:"newly_blocking_rules"`
	NewlyWarningRules    []string         `json:"newly_warning_rules"`
	ResolvedRules        []string         `json:"resolved_rules"`
	ActionChanged        bool             `json:"action_changed"`
	CurrentActions       []string         `json:"current_actions"`
	CandidateActions     []string         `json:"candidate_actions"`
	ActionRedactionCount int              `json:"action_redaction_count"`
	Cost                 CostDelta        `json:"cost"`
}

// RuleImpact counts violation matches across the replay corpus.
type RuleImpact struct {
	RuleID           string `json:"rule_id"`
	CurrentMatches   int    `json:"current_matches"`
	CandidateMatches int    `json:"candidate_matches"`
	MatchDelta       int    `json:"match_delta"`
}

// Summary is the bounded quick-scan comparison.
type Summary struct {
	CaseCount               int   `json:"case_count"`
	DecisionChanges         int   `json:"decision_changes"`
	NewlyBlockingCases      int   `json:"newly_blocking_cases"`
	NewlyWarningCases       int   `json:"newly_warning_cases"`
	ResolvedViolationCount  int   `json:"resolved_violation_count"`
	ActionChanges           int   `json:"action_changes"`
	CurrentEstimatedUnits   int64 `json:"current_estimated_units"`
	CandidateEstimatedUnits int64 `json:"candidate_estimated_units"`
	EstimatedUnitsDelta     int64 `json:"estimated_units_delta"`
}

// Report is the deterministic policy-impact result.
type Report struct {
	FormatVersion        string           `json:"format_version"`
	CorpusID             string           `json:"corpus_id"`
	CorpusCompleteness   Completeness     `json:"corpus_completeness"`
	Candidate            Candidate        `json:"candidate"`
	Summary              Summary          `json:"summary"`
	Cases                []CaseComparison `json:"cases"`
	Rules                []RuleImpact     `json:"rules"`
	CorpusUnmatchedRules []string         `json:"corpus_unmatched_rules"`
	SafetyConclusion     string           `json:"safety_conclusion"`
}
