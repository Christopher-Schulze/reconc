package runtime

import (
	"fmt"
	"strings"

	"reconc.dev/reconc/internal/policy"
)

func buildViolation(
	rule *policy.Rule,
	defaultMode policy.Mode,
	matchedPaths, matchedCommands, matchedClaims, requiredPaths, requiredCommands, requiredClaims []string,
) *Violation {
	mode := defaultMode
	if rule.Mode != "" {
		mode = rule.Mode
	}

	explanation, recommended := explainViolation(
		rule.ID, rule.Kind, rule,
		matchedPaths, matchedCommands,
		requiredPaths, requiredCommands, requiredClaims,
	)

	return &Violation{
		RuleID:            rule.ID,
		Kind:              rule.Kind,
		Mode:              mode,
		Message:           rule.Message,
		Explanation:       explanation,
		RecommendedAction: recommended,
		MatchedPaths:      coalesce(matchedPaths),
		MatchedCommands:   coalesce(matchedCommands),
		MatchedClaims:     coalesce(matchedClaims),
		RequiredPaths:     coalesce(requiredPaths),
		RequiredCommands:  coalesce(requiredCommands),
		RequiredClaims:    coalesce(requiredClaims),
		SourcePath:        rule.SourcePath,
		SourceBlockID:     rule.SourceBlockID,
	}
}

func coalesce(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func explainViolation(
	id string, kind policy.Kind, rule *policy.Rule,
	matchedPaths, matchedCommands, requiredPaths, requiredCommands, requiredClaims []string,
) (string, string) {
	pathList := joinForHumans(matchedPaths)
	commandList := joinForHumans(matchedCommands)
	requiredPathList := joinForHumans(requiredPaths)
	requiredCommandList := joinForHumans(requiredCommands)
	requiredClaimList := joinForHumans(requiredClaims)

	switch kind {
	case policy.KindDenyWrite:
		fallback := joinForHumans(stringListField(rule, "paths"))
		return fmt.Sprintf("Write activity %s matched deny_write rule '%s'.", pathList, id),
			fmt.Sprintf("Avoid writing paths matching %s.", fallback)
	case policy.KindRequireRead:
		return fmt.Sprintf("Write activity %s triggered require_read rule '%s', but no required read matched %s.", pathList, id, requiredPathList),
			fmt.Sprintf("Read at least one path matching %s before modifying %s.", requiredPathList, pathList)
	case policy.KindRequireCommand:
		return fmt.Sprintf("Write activity %s triggered require_command rule '%s', but no required command matched %s.", pathList, id, requiredCommandList),
			fmt.Sprintf("Run one of the required commands before finishing: %s.", requiredCommandList)
	case policy.KindRequireCommandSuccess:
		return fmt.Sprintf("Write activity %s triggered require_command_success rule '%s', but no required successful command matched %s.", pathList, id, requiredCommandList),
			fmt.Sprintf("Run one of the required commands successfully before finishing: %s.", requiredCommandList)
	case policy.KindForbidCommand:
		forbidden := joinForHumans(stringListField(rule, "commands"))
		whenList := joinForHumans(stringListField(rule, "when_paths"))
		if len(matchedPaths) > 0 {
			return fmt.Sprintf("Forbidden command(s) %s ran while writing %s, matching forbid_command rule '%s'.", commandList, pathList, id),
				fmt.Sprintf("Do not run %s when touching paths matching %s; revert or replace the invocation with an allowed alternative.", forbidden, whenList)
		}
		return fmt.Sprintf("Forbidden command(s) %s ran, matching forbid_command rule '%s'.", commandList, id),
			fmt.Sprintf("Do not run %s in this repository; revert or replace the invocation with an allowed alternative.", forbidden)
	case policy.KindCoupleChange:
		return fmt.Sprintf("Write activity %s triggered couple_change rule '%s', but no coupled change matched %s.", pathList, id, requiredPathList),
			fmt.Sprintf("Update at least one path matching %s alongside %s.", requiredPathList, pathList)
	case policy.KindRequireClaim:
		return fmt.Sprintf("Write activity %s triggered require_claim rule '%s', but no required claim matched %s.", pathList, id, requiredClaimList),
			fmt.Sprintf("Record one of the required claims before finishing: %s.", requiredClaimList)
	}
	return fmt.Sprintf("Rule '%s' triggered for paths %s and commands %s.", id, pathList, commandList),
		"Inspect the matched rule and input evidence, then rerun the policy check."
}

func stringListField(rule *policy.Rule, key string) []string {
	if rule == nil {
		return nil
	}
	switch key {
	case "paths":
		return rule.Paths
	case "before_paths":
		return rule.BeforePaths
	case "when_paths":
		return rule.WhenPaths
	case "commands":
		return rule.Commands
	case "claims":
		return rule.Claims
	case "scope_paths":
		return rule.ScopePaths
	default:
		return nil
	}
}

func joinForHumans(values []string) string {
	if len(values) == 0 {
		return "<none>"
	}
	if len(values) == 1 {
		return values[0]
	}
	return strings.Join(values, ", ")
}

// --- Summary + next-action ---

func summarizeReport(decision Decision, total, blocking int) string {
	if total == 0 {
		return "Policy check passed with no violations."
	}
	if decision == DecisionBlock {
		return fmt.Sprintf("Policy check found %d violation(s), including %d blocking violation(s).", total, blocking)
	}
	return fmt.Sprintf("Policy check found %d non-blocking violation(s).", total)
}

func nextActionForViolations(violations []Violation) string {
	if len(violations) == 0 {
		return ""
	}
	for _, v := range violations {
		if v.IsBlocking() {
			return v.RecommendedAction
		}
	}
	return violations[0].RecommendedAction
}
