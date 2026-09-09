package runtime

import (
	"fmt"
	"strconv"
	"strings"

	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/schema"
)

// ResolveFixPlanSchema returns the URL to stamp on newly produced
// fix plans. Honors $RECONC_SCHEMA_BASE_URL (W24); defaults to
// DefaultFixPlanSchema.
func ResolveFixPlanSchema() string {
	return schema.Resolve(schema.PolicyFixPlan)
}

// FixPlanSchema + FormatVersion are the contract for `reconc fix`
// JSON output. Bumped only on shape-breaking changes.
const (
	// FixPlanSchema / DefaultFixPlanSchema: the JSON-schema URL for
	// `reconc fix` output. Use ResolveFixPlanSchema() on write paths
	// so $RECONC_SCHEMA_BASE_URL (W24) is honoured.
	FixPlanSchema              = DefaultFixPlanSchema
	DefaultFixPlanSchema       = schema.PolicyFixPlanURL
	FixPlanFormatVersion       = "2"
	LegacyFixPlanSchema        = schema.PolicyFixPlanV1URL
	LegacyFixPlanFormatVersion = "1"
)

// RemediationActionCode identifies the stable operation an agent may take
// after reviewing a remediation. Actions never grant authorization.
type RemediationActionCode string

const (
	ActionRunCommand      RemediationActionCode = "run_command"
	ActionAssertClaim     RemediationActionCode = "assert_claim"
	ActionProvideEvidence RemediationActionCode = "provide_evidence"
	ActionRefresh         RemediationActionCode = "refresh"
	ActionRetry           RemediationActionCode = "retry"
	ActionInspect         RemediationActionCode = "inspect"
	ActionRequestApproval RemediationActionCode = "request_approval"
)

// RemediationActionKind identifies how an action's payload must be treated.
// A shell action is a literal script and must never be split into argv by a
// consumer; argv actions are already separated argument values.
type RemediationActionKind string

const (
	ActionKindArgv       RemediationActionKind = "argv"
	ActionKindShell      RemediationActionKind = "shell"
	ActionKindEvidence   RemediationActionKind = "evidence"
	ActionKindInspection RemediationActionKind = "inspection"
	ActionKindApproval   RemediationActionKind = "approval"
)

// RemediationAction is the executable or evidence-bearing part of a fix
// recipe. Authorization and evidence requirements are explicit so agents do
// not infer permission from RecommendedAction prose.
type RemediationAction struct {
	Code             RemediationActionCode `json:"code"`
	Kind             RemediationActionKind `json:"kind"`
	Argv             []string              `json:"argv,omitempty"`
	Shell            string                `json:"shell,omitempty"`
	Cwd              string                `json:"cwd,omitempty"`
	Authorization    string                `json:"authorization,omitempty"`
	RequiredEvidence []string              `json:"required_evidence,omitempty"`
	RequiredClaims   []string              `json:"required_claims,omitempty"`
}

// FixPlan is the structured remediation report for one CheckReport.
//
// FixPlan is designed for agent consumption: each remediation has step-by-step
// instructions plus suggested commands / claims / files to surface.
type FixPlan struct {
	Schema                 string          `json:"$schema"`
	FormatVersion          string          `json:"format_version"`
	Decision               Decision        `json:"decision"`
	Summary                string          `json:"summary"`
	RepoRoot               string          `json:"repo_root"`
	LockfilePath           string          `json:"lockfile_path"`
	Inputs                 ExecutionInputs `json:"inputs"`
	ViolationCount         int             `json:"violation_count"`
	BlockingViolationCount int             `json:"blocking_violation_count"`
	RemediationCount       int             `json:"remediation_count"`
	Remediations           []Remediation   `json:"remediations"`
}

// Remediation is one structured fix recipe per violation.
//
// Priority is "blocking" or "non-blocking" so callers can sort by
// urgency without re-checking modes.
type Remediation struct {
	RuleID            string              `json:"rule_id"`
	Kind              policy.Kind         `json:"kind"`
	Mode              policy.Mode         `json:"mode"`
	Priority          string              `json:"priority"`
	Message           string              `json:"message"`
	Why               string              `json:"why"`
	RecommendedAction string              `json:"recommended_action"`
	SuggestedCommands []string            `json:"suggested_commands,omitempty"`
	ForbiddenCommands []string            `json:"forbidden_commands,omitempty"`
	SuggestedClaims   []string            `json:"suggested_claims,omitempty"`
	FilesToInspect    []string            `json:"files_to_inspect,omitempty"`
	Steps             []string            `json:"steps"`
	Actions           []RemediationAction `json:"actions,omitempty"`
	SourcePath        string              `json:"source_path,omitempty"`
	SourceBlockID     string              `json:"source_block_id,omitempty"`
	CanAutofix        bool                `json:"can_autofix"`
}

// BuildFixPlan converts a CheckReport into a structured fix plan.
// Pure function: no IO, no globals, deterministic ordering.
//
// One remediation is emitted per violation; non-blocking violations
// still get remediations so callers see the full picture (priority
// distinguishes them).
func BuildFixPlan(report *CheckReport) *FixPlan {
	return buildFixPlan(report, true, ResolveFixPlanSchema(), FixPlanFormatVersion)
}

// BuildLegacyFixPlan emits the original v1 shape for consumers that cannot
// yet read typed actions. It intentionally omits Actions while the current
// BuildFixPlan path emits v2.
func BuildLegacyFixPlan(report *CheckReport) *FixPlan {
	return buildFixPlan(report, false, schema.ResolveVersion(schema.PolicyFixPlan, "1"), LegacyFixPlanFormatVersion)
}

func buildFixPlan(report *CheckReport, includeActions bool, schemaURL, formatVersion string) *FixPlan {
	if report == nil {
		return &FixPlan{
			Schema:        schemaURL,
			FormatVersion: formatVersion,
			Remediations:  []Remediation{},
		}
	}

	remediations := make([]Remediation, 0, len(report.Violations))
	for _, v := range report.Violations {
		remediations = append(remediations, buildRemediation(v, report.RepoRoot, includeActions))
	}

	return &FixPlan{
		Schema:                 schemaURL,
		FormatVersion:          formatVersion,
		Decision:               report.Decision,
		Summary:                renderFixPlanSummary(report),
		RepoRoot:               report.RepoRoot,
		LockfilePath:           report.LockfilePath,
		Inputs:                 report.Inputs,
		ViolationCount:         report.ViolationCount,
		BlockingViolationCount: report.BlockingViolationCount,
		RemediationCount:       len(remediations),
		Remediations:           remediations,
	}
}

func buildRemediation(v Violation, repoRoot string, includeActions bool) Remediation {
	priority := "non-blocking"
	if v.IsBlocking() {
		priority = "blocking"
	}

	rem := Remediation{
		RuleID:            v.RuleID,
		Kind:              v.Kind,
		Mode:              v.Mode,
		Priority:          priority,
		Message:           v.Message,
		Why:               v.Explanation,
		RecommendedAction: v.RecommendedAction,
		SourcePath:        v.SourcePath,
		SourceBlockID:     v.SourceBlockID,
		CanAutofix:        false, // typed actions still require explicit authorization
	}

	// Per-kind remediation hints.
	switch v.Kind {
	case policy.KindRequireCommand, policy.KindRequireCommandSuccess:
		rem.SuggestedCommands = dedupeStrings(v.RequiredCommands)
	case policy.KindForbidCommand:
		rem.ForbiddenCommands = dedupeStrings(v.MatchedCommands)
		// Surface the rule's forbidden commands too if any
		if len(rem.ForbiddenCommands) == 0 {
			rem.ForbiddenCommands = dedupeStrings(v.RequiredCommands)
		}
	case policy.KindRequireClaim:
		rem.SuggestedClaims = dedupeStrings(v.RequiredClaims)
	}

	// Files to inspect: the rule's source location (where the rule
	// was authored) + matched + required paths. Helpful for the agent
	// to know which files to read for full context.
	files := []string{}
	if v.SourcePath != "" {
		files = append(files, v.SourcePath)
	}
	files = append(files, v.MatchedPaths...)
	files = append(files, v.RequiredPaths...)
	rem.FilesToInspect = dedupeStrings(files)

	rem.Steps = buildStepsForKind(v)
	if includeActions {
		rem.Actions = buildActionsForViolation(v, repoRoot)
	}
	return rem
}

func buildActionsForViolation(v Violation, repoRoot string) []RemediationAction {
	actions := make([]RemediationAction, 0, 2)
	switch v.Kind {
	case policy.KindRequireCommand, policy.KindRequireCommandSuccess:
		evidence := "command_executed"
		if v.Kind == policy.KindRequireCommandSuccess {
			evidence = "command_success"
		}
		for _, command := range dedupeStrings(v.RequiredCommands) {
			actions = append(actions, RemediationAction{
				Code: ActionRunCommand, Kind: ActionKindShell, Shell: command,
				Cwd: repoRoot, Authorization: "operator_approval",
				RequiredEvidence: []string{evidence},
			})
		}
	case policy.KindRequireClaim:
		for _, claim := range dedupeStrings(v.RequiredClaims) {
			actions = append(actions, RemediationAction{
				Code: ActionAssertClaim, Kind: ActionKindArgv,
				Argv: []string{"reconc", "check", "--claim", claim},
				Cwd:  repoRoot, Authorization: "operator_approval",
				RequiredClaims: []string{claim},
			})
		}
	case policy.KindDenyWrite:
		actions = append(actions, RemediationAction{
			Code: ActionRequestApproval, Kind: ActionKindApproval,
			Authorization: "policy_update", RequiredEvidence: dedupeStrings(v.MatchedPaths),
		})
	case policy.KindRequireRead, policy.KindCoupleChange, policy.KindRequireEvidence:
		actions = append(actions, RemediationAction{
			Code: ActionProvideEvidence, Kind: ActionKindEvidence,
			Cwd: repoRoot, RequiredEvidence: dedupeStrings(v.RequiredPaths),
		})
	case policy.KindRequireFreshFile:
		actions = append(actions, RemediationAction{
			Code: ActionRefresh, Kind: ActionKindEvidence,
			Cwd: repoRoot, RequiredEvidence: dedupeStrings(v.RequiredPaths),
		})
	case policy.KindAllOf, policy.KindAnyOf, policy.KindNot, policy.KindRequireScript, policy.KindRequireAssurance:
		actions = append(actions, RemediationAction{
			Code: ActionRetry, Kind: ActionKindInspection,
			Cwd: repoRoot, RequiredEvidence: dedupeStrings(append(append([]string{}, v.MatchedPaths...), v.RequiredPaths...)),
		})
	default:
		actions = append(actions, RemediationAction{
			Code: ActionInspect, Kind: ActionKindInspection,
			Cwd: repoRoot, RequiredEvidence: dedupeStrings(append(append([]string{}, v.MatchedPaths...), v.RequiredPaths...)),
		})
	}
	return actions
}

// buildStepsForKind produces 2-4 actionable items per violation
// tailored to its kind. Keeps remediation discoverable without
// needing to read the full violation prose.
func buildStepsForKind(v Violation) []string {
	switch v.Kind {
	case policy.KindDenyWrite:
		return []string{
			"Stop writing to: " + joinForHumans(v.MatchedPaths),
			"If the write is intentional, the rule '" + v.RuleID + "' must be amended (requires policy update)",
		}
	case policy.KindRequireRead:
		return []string{
			"Read at least one file matching: " + joinForHumans(v.RequiredPaths),
			"Then re-attempt the write to: " + joinForHumans(v.MatchedPaths),
		}
	case policy.KindRequireCommand:
		return []string{
			"Run one of: " + joinForHumans(v.RequiredCommands),
			"Then assert the command via --command flag (or run `reconc check` again)",
		}
	case policy.KindRequireCommandSuccess:
		return []string{
			"Run one of: " + joinForHumans(v.RequiredCommands) + " AND ensure success",
			"Assert the success outcome via --command-success flag",
		}
	case policy.KindForbidCommand:
		hits := joinForHumans(v.MatchedCommands)
		return []string{
			"Do not run: " + hits,
			"Replace with an allowed alternative or revert the change that motivated it",
		}
	case policy.KindCoupleChange:
		return []string{
			"Add a coupled change matching: " + joinForHumans(v.RequiredPaths),
			"The coupled write must be a SEPARATE path from " + joinForHumans(v.MatchedPaths),
		}
	case policy.KindRequireClaim:
		return []string{
			"Assert one of these claims via --claim flag: " + joinForHumans(v.RequiredClaims),
			"Claims are typically asserted by CI / harness after a successful gate (e.g. ci-green)",
		}
	case policy.KindRequireFreshFile:
		return []string{
			"Refresh or create the listed required files: " + joinForHumans(v.RequiredPaths),
			"Files must be regular files; freshness is checked against MaxAgeHours",
		}
	case policy.KindRequireEvidence:
		return []string{
			"Update the evidence files to satisfy the assertions",
			"Inspect: " + joinForHumans(v.RequiredPaths),
		}
	case policy.KindAllOf, policy.KindAnyOf, policy.KindNot:
		return []string{
			"Composite rule failed; see explanation for which sub-checks failed",
			"Resolve each listed sub-check failure individually",
		}
	case policy.KindRequireScript:
		return []string{
			"Inspect the script output above; resolve the reported failure",
			"Re-run after fixing whatever the script flagged",
		}
	case policy.KindRequireAssurance:
		return []string{
			"Apply the exact remediation from the first native assurance finding",
			"Rerun the same successful verification commands and policy check",
		}
	}
	return []string{
		"Inspect the matched rule and input evidence, then rerun the policy check",
	}
}

func renderFixPlanSummary(r *CheckReport) string {
	if r.ViolationCount == 0 {
		return "No remediation needed; policy check passed."
	}
	if r.BlockingViolationCount > 0 {
		return fmt.Sprintf("%d remediation(s); %d blocking. Address the blocking items first.",
			r.ViolationCount, r.BlockingViolationCount)
	}
	return fmt.Sprintf("%d non-blocking remediation(s). No action strictly required.",
		r.ViolationCount)
}

// RenderFixPlanText produces a human-readable text rendering of a fix
// plan. Used by `reconc fix` when --json is not specified.
func RenderFixPlanText(p *FixPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Fix plan: %s\n", p.Summary)
	if p.RemediationCount == 0 {
		return b.String()
	}
	fmt.Fprintf(&b, "\nRemediations (%d total):\n\n", p.RemediationCount)
	for i, r := range p.Remediations {
		fmt.Fprintf(&b, "%d. [%s | %s] %s\n", i+1, r.Priority, r.Kind, r.RuleID)
		fmt.Fprintf(&b, "   Why:  %s\n", r.Why)
		fmt.Fprintf(&b, "   Do:   %s\n", r.RecommendedAction)
		for _, step := range r.Steps {
			fmt.Fprintf(&b, "    - %s\n", step)
		}
		if len(r.SuggestedCommands) > 0 && len(r.Actions) == 0 {
			fmt.Fprintf(&b, "   Shell (literal): %s\n", joinForHumans(r.SuggestedCommands))
		}
		if len(r.ForbiddenCommands) > 0 {
			fmt.Fprintf(&b, "   Avoid (literal shell): %s\n", joinForHumans(r.ForbiddenCommands))
		}
		if len(r.SuggestedClaims) > 0 {
			fmt.Fprintf(&b, "   Claim: %s\n", joinForHumans(r.SuggestedClaims))
		}
		for _, action := range r.Actions {
			fmt.Fprintf(&b, "   Action: %s (%s)\n", action.Code, action.Kind)
			if action.Cwd != "" {
				fmt.Fprintf(&b, "   Cwd:   %s\n", action.Cwd)
			}
			if action.Shell != "" {
				fmt.Fprintf(&b, "   Shell (literal): %s\n", strconv.Quote(action.Shell))
			}
			if len(action.Argv) > 0 {
				fmt.Fprintf(&b, "   Argv:  %s\n", renderActionArgv(action.Argv))
			}
			if action.Authorization != "" {
				fmt.Fprintf(&b, "   Auth:  %s\n", action.Authorization)
			}
			if len(action.RequiredEvidence) > 0 {
				fmt.Fprintf(&b, "   Need:  %s\n", joinForHumans(action.RequiredEvidence))
			}
			if len(action.RequiredClaims) > 0 {
				fmt.Fprintf(&b, "   Claims: %s\n", joinForHumans(action.RequiredClaims))
			}
		}
		if len(r.FilesToInspect) > 0 {
			fmt.Fprintf(&b, "   See:  %s\n", joinForHumans(r.FilesToInspect))
		}
		if i < len(p.Remediations)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func renderActionArgv(argv []string) string {
	quoted := make([]string, len(argv))
	for index, value := range argv {
		quoted[index] = "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
	}
	return strings.Join(quoted, " ")
}

// RenderCheckReportMarkdown produces a markdown rendering of a
// CheckReport. Used by `reconc explain --format markdown`.
func RenderCheckReportMarkdown(r *CheckReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Policy Check Report\n\n")
	fmt.Fprintf(&b, "- **Decision:** `%s`\n", r.Decision)
	fmt.Fprintf(&b, "- **Repo:** `%s`\n", r.RepoRoot)
	fmt.Fprintf(&b, "- **Lockfile:** `%s`\n", r.LockfilePath)
	fmt.Fprintf(&b, "- **Default mode:** `%s`\n", r.DefaultMode)
	fmt.Fprintf(&b, "- **Summary:** %s\n", r.Summary)
	if r.NextAction != "" {
		fmt.Fprintf(&b, "- **Next:** %s\n", r.NextAction)
	}
	fmt.Fprintf(&b, "\n## Inputs\n\n")
	if len(r.Inputs.WritePaths) > 0 {
		fmt.Fprintf(&b, "- writes: %s\n", joinForHumans(r.Inputs.WritePaths))
	}
	if len(r.Inputs.ReadPaths) > 0 {
		fmt.Fprintf(&b, "- reads: %s\n", joinForHumans(r.Inputs.ReadPaths))
	}
	if len(r.Inputs.Commands) > 0 {
		fmt.Fprintf(&b, "- commands: %s\n", joinForHumans(r.Inputs.Commands))
	}
	if len(r.Inputs.Claims) > 0 {
		fmt.Fprintf(&b, "- claims: %s\n", joinForHumans(r.Inputs.Claims))
	}
	fmt.Fprintf(&b, "\n## Violations (%d total, %d blocking)\n\n", r.ViolationCount, r.BlockingViolationCount)
	if r.ViolationCount == 0 {
		fmt.Fprintf(&b, "_None._\n")
		return b.String()
	}
	for i, v := range r.Violations {
		fmt.Fprintf(&b, "### %d. `%s` (%s, %s)\n\n", i+1, v.RuleID, v.Kind, v.Mode)
		fmt.Fprintf(&b, "%s\n\n", v.Explanation)
		fmt.Fprintf(&b, "**Action:** %s\n\n", v.RecommendedAction)
		if v.SourcePath != "" {
			fmt.Fprintf(&b, "_Defined in `%s`_\n\n", v.SourcePath)
		}
	}
	return b.String()
}

// dedupeStrings removes duplicates while preserving each value byte-for-byte
// and retaining order of first occurrence. Empty strings are dropped.
func dedupeStrings(xs []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(xs))
	for _, s := range xs {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
