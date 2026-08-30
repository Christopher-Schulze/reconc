package runtime

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"reconc.dev/reconc/internal/policy"
)

const (
	maxViolationAggregateBytes = 8 << 10
	maxViolationListBytes      = 4 << 10
	violationTextMarker        = "...[truncated]"
	violationOmissionReserve   = 96
)

type violationTextCollector struct {
	items     []string
	bytes     int
	limit     int
	separator string
	noun      string
	omitted   int
	sealed    bool
}

func newViolationTextCollector(limit int, separator, noun string) *violationTextCollector {
	return &violationTextCollector{limit: limit, separator: separator, noun: noun}
}

func (c *violationTextCollector) add(value string) {
	value = strings.ToValidUTF8(value, "�")
	if c.sealed {
		c.omitted++
		return
	}
	separatorBytes := 0
	if len(c.items) > 0 {
		separatorBytes = len(c.separator)
	}
	contentLimit := c.limit - violationOmissionReserve
	if c.bytes+separatorBytes+len(value) <= contentLimit {
		c.items = append(c.items, value)
		c.bytes += separatorBytes + len(value)
		return
	}
	if len(c.items) == 0 {
		c.items = append(c.items, truncateViolationText(value, contentLimit))
		c.bytes = len(c.items[0])
	} else {
		c.omitted++
	}
	c.sealed = true
}

func (c *violationTextCollector) count() int {
	return len(c.items) + c.omitted
}

func (c *violationTextCollector) text() string {
	text := strings.Join(c.items, c.separator)
	if c.omitted == 0 {
		return text
	}
	marker := fmt.Sprintf("...[%d additional %s omitted]", c.omitted, c.noun)
	if text == "" {
		return marker
	}
	return text + c.separator + marker
}

func boundViolationText(violation *Violation) *Violation {
	if violation == nil {
		return nil
	}
	violation.Message = truncateViolationText(violation.Message, MaxViolationTextBytes)
	violation.Explanation = truncateViolationText(violation.Explanation, MaxViolationTextBytes)
	violation.RecommendedAction = truncateViolationText(violation.RecommendedAction, MaxViolationTextBytes)
	return violation
}

func truncateViolationText(value string, limit int) string {
	value = strings.ToValidUTF8(value, "�")
	if len(value) <= limit {
		return value
	}
	if limit <= len(violationTextMarker) {
		return truncateViolationUTF8(value, limit)
	}
	return truncateViolationUTF8(value, limit-len(violationTextMarker)) + violationTextMarker
}

func truncateViolationUTF8(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit]
}

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

	return boundViolationText(&Violation{
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
	})
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
		return fmt.Sprintf("Write activity %s matched deny_write rule %s.", pathList, quote(id)),
			fmt.Sprintf("Avoid writing paths matching %s.", fallback)
	case policy.KindRequireRead:
		return fmt.Sprintf("Write activity %s triggered require_read rule %s, but no required read matched %s.", pathList, quote(id), requiredPathList),
			fmt.Sprintf("Read at least one path matching %s before modifying %s.", requiredPathList, pathList)
	case policy.KindRequireCommand:
		return fmt.Sprintf("Write activity %s triggered require_command rule %s, but no required command matched %s.", pathList, quote(id), requiredCommandList),
			fmt.Sprintf("Run one of the required commands before finishing: %s.", requiredCommandList)
	case policy.KindRequireCommandSuccess:
		return fmt.Sprintf("Write activity %s triggered require_command_success rule %s, but no required successful command matched %s.", pathList, quote(id), requiredCommandList),
			fmt.Sprintf("Run one of the required commands successfully before finishing: %s.", requiredCommandList)
	case policy.KindForbidCommand:
		forbidden := joinForHumans(stringListField(rule, "commands"))
		whenList := joinForHumans(stringListField(rule, "when_paths"))
		if len(matchedPaths) > 0 {
			return fmt.Sprintf("Forbidden command(s) %s ran while writing %s, matching forbid_command rule %s.", commandList, pathList, quote(id)),
				fmt.Sprintf("Do not run %s when touching paths matching %s; revert or replace the invocation with an allowed alternative.", forbidden, whenList)
		}
		return fmt.Sprintf("Forbidden command(s) %s ran, matching forbid_command rule %s.", commandList, quote(id)),
			fmt.Sprintf("Do not run %s in this repository; revert or replace the invocation with an allowed alternative.", forbidden)
	case policy.KindCoupleChange:
		return fmt.Sprintf("Write activity %s triggered couple_change rule %s, but no coupled change matched %s.", pathList, quote(id), requiredPathList),
			fmt.Sprintf("Update at least one path matching %s alongside %s.", requiredPathList, pathList)
	case policy.KindRequireClaim:
		return fmt.Sprintf("Write activity %s triggered require_claim rule %s, but no required claim matched %s.", pathList, quote(id), requiredClaimList),
			fmt.Sprintf("Record one of the required claims before finishing: %s.", requiredClaimList)
	}
	return fmt.Sprintf("Rule %s triggered for paths %s and commands %s.", quote(id), pathList, commandList),
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
	collector := newViolationTextCollector(maxViolationListBytes, ", ", "values")
	for _, value := range values {
		collector.add(value)
	}
	return collector.text()
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
