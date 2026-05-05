package runtime

import (
	"os"
	"strings"

	"reconc.dev/reconc/internal/policy"
)

// Decision is the top-level verdict of a check run.
//
//	pass  -- no violations or only observe-mode violations
//	warn  -- at least one warn-mode violation, no blocking ones
//	block -- at least one block- or fix-mode violation
type Decision string

const (
	DecisionPass  Decision = "pass"
	DecisionWarn  Decision = "warn"
	DecisionBlock Decision = "block"
)

// DefaultCheckReportSchema is the default JSON-schema URL stamped on every
// saved CheckReport. Deployments can override the base via
// $RECONC_SCHEMA_BASE_URL (W24).
const DefaultCheckReportSchema = "https://reconc.dev/schemas/policy-report/v1"

// CheckReportSchema is a compile-time constant for callers that need
// the literal URL (tests, doc references). Runtime code paths that
// write reports should use ResolveCheckReportSchema() so the env
// override is honoured.
//
// Kept for back-compat with existing callers; equals
// DefaultCheckReportSchema.
const CheckReportSchema = DefaultCheckReportSchema

// ResolveCheckReportSchema returns the URL to stamp on newly produced
// CheckReports. Honors $RECONC_SCHEMA_BASE_URL; defaults to
// DefaultCheckReportSchema.
func ResolveCheckReportSchema() string {
	if base := os.Getenv("RECONC_SCHEMA_BASE_URL"); base != "" {
		return strings.TrimRight(base, "/") + "/schemas/policy-report/v1"
	}
	return DefaultCheckReportSchema
}

// CheckReportFormatVersion is the explicit format-version field in the
// report payload. Together with CheckReportSchema it lets future
// versions of reconc refuse stale or drifted reports rather than
// silently misinterpret them.
const CheckReportFormatVersion = "1"

// Violation describes one rule that fired during evaluation.
//
// "Matched" fields capture what the agent actually did that triggered
// the rule. "Required" fields capture what the rule demands. Both are
// populated where applicable so reports can show "you wrote X, the
// rule required also writing Y".
type Violation struct {
	RuleID            string      `json:"rule_id"`
	Kind              policy.Kind `json:"kind"`
	Mode              policy.Mode `json:"mode"`
	Message           string      `json:"message"`
	Explanation       string      `json:"explanation"`
	RecommendedAction string      `json:"recommended_action"`
	MatchedPaths      []string    `json:"matched_paths"`
	MatchedCommands   []string    `json:"matched_commands"`
	MatchedClaims     []string    `json:"matched_claims"`
	RequiredPaths     []string    `json:"required_paths"`
	RequiredCommands  []string    `json:"required_commands"`
	RequiredClaims    []string    `json:"required_claims"`
	SourcePath        string      `json:"source_path,omitempty"`
	SourceBlockID     string      `json:"source_block_id,omitempty"`
}

// IsBlocking reports whether this violation alone would force a
// block decision (mode is block or fix).
func (v Violation) IsBlocking() bool {
	return v.Mode.IsBlocking()
}

// CheckReport is the full structured outcome of a check run, suitable
// for both human consumption (via reporting) and machine consumption
// (via JSON output, audit logs, fix-plan generation).
//
// The schema is stable so existing tooling can consume reconc reports without
// translation.
type CheckReport struct {
	Schema        string      `json:"$schema"`
	FormatVersion string      `json:"format_version"`
	OK            bool        `json:"ok"`
	RepoRoot      string      `json:"repo_root"`
	LockfilePath  string      `json:"lockfile_path"`
	Decision      Decision    `json:"decision"`
	DefaultMode   policy.Mode `json:"default_mode"`
	Summary       string      `json:"summary"`
	// Actions is the progressive-disclosure quick-scan slice (W42):
	// one recommended action per violation (or the message when no
	// recommended action was set). Index-aligned with RuleIDs so
	// rule_ids[i] is the rule for actions[i]. Populated by Finalize
	// so agents can scan Actions first and only drill into Violations
	// when they need the full context. Stable API contract.
	Actions []string `json:"actions"`
	// RuleIDs mirrors the rule ids from Violations for fast filtering
	// without decoding Actions or Violations. Index-aligned with Actions.
	RuleIDs                []string        `json:"rule_ids"`
	NextAction             string          `json:"next_action,omitempty"`
	Inputs                 ExecutionInputs `json:"inputs"`
	ViolationCount         int             `json:"violation_count"`
	BlockingViolationCount int             `json:"blocking_violation_count"`
	Violations             []Violation     `json:"violations"`
}

// NewEmptyReport returns a CheckReport pre-populated with the schema
// constants and zeroed counters. Used as a starting point in the
// evaluator.
func NewEmptyReport(repoRoot, lockfilePath string, defaultMode policy.Mode, inputs ExecutionInputs) CheckReport {
	return CheckReport{
		Schema:        ResolveCheckReportSchema(),
		FormatVersion: CheckReportFormatVersion,
		OK:            true,
		RepoRoot:      repoRoot,
		LockfilePath:  lockfilePath,
		Decision:      DecisionPass,
		DefaultMode:   defaultMode,
		Inputs:        inputs,
		Violations:    []Violation{},
		Actions:       []string{},
		RuleIDs:       []string{},
	}
}

// Finalize computes the derived fields (counts, decision, ok, summary,
// actions, rule_ids) from the violations slice. Call after appending
// all violations and before returning the report.
func (r *CheckReport) Finalize() {
	r.ViolationCount = len(r.Violations)
	r.BlockingViolationCount = 0
	hasNonBlocking := false
	// Reset progressive-disclosure slices so Finalize is idempotent.
	r.Actions = make([]string, 0, len(r.Violations))
	r.RuleIDs = make([]string, 0, len(r.Violations))
	for _, v := range r.Violations {
		if v.IsBlocking() {
			r.BlockingViolationCount++
		} else if v.Mode == policy.ModeWarn {
			hasNonBlocking = true
		}
		r.RuleIDs = append(r.RuleIDs, v.RuleID)
		if v.RecommendedAction != "" {
			r.Actions = append(r.Actions, v.RecommendedAction)
		} else {
			r.Actions = append(r.Actions, v.Message)
		}
	}
	switch {
	case r.BlockingViolationCount > 0:
		r.Decision = DecisionBlock
		r.OK = false
	case hasNonBlocking:
		r.Decision = DecisionWarn
		r.OK = true
	default:
		r.Decision = DecisionPass
		r.OK = true
	}
	r.Summary = renderSummary(*r)
}

// TerseReport is the minimal-token machine-readable form of a
// CheckReport. ~50 tokens vs ~500+ for the full report.
//
// Used by --terse mode; designed so an agent can decide pass/block
// + know what to do in one decode of a tiny JSON object.
type TerseReport struct {
	Decision Decision `json:"decision"`
	OK       bool     `json:"ok"`
	RuleIDs  []string `json:"rule_ids"`
	Actions  []string `json:"actions"`
}

// Terse converts a CheckReport into its minimal form. Reuses the
// progressive-disclosure slices populated by Finalize so the two
// representations are guaranteed to agree.
func (r *CheckReport) Terse() TerseReport {
	ruleIDs := r.RuleIDs
	if ruleIDs == nil {
		ruleIDs = []string{}
	}
	actions := r.Actions
	if actions == nil {
		actions = []string{}
	}
	return TerseReport{
		Decision: r.Decision,
		OK:       r.OK,
		RuleIDs:  ruleIDs,
		Actions:  actions,
	}
}

// renderSummary produces a one-line human summary, e.g.
// "1 blocking, 2 warnings; see violations.".
func renderSummary(r CheckReport) string {
	switch r.Decision {
	case DecisionPass:
		return "All policy rules satisfied."
	case DecisionWarn:
		return formatCount(r.ViolationCount, "non-blocking violation")
	case DecisionBlock:
		return formatCount(r.BlockingViolationCount, "blocking violation") + " (see violations)"
	default:
		return ""
	}
}

func formatCount(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return itoa(n) + " " + noun + "s"
}

// itoa is a tiny stdlib-free integer-to-string used here to avoid
// pulling strconv into a hot path. Same shape as parser.itoa.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}
