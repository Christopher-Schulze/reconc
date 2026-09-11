package policy

import "fmt"

// ValidateCompositeWritePhase rejects disjunctions whose write authorization
// depends on evidence that can only be completed after the write. Conjunctions
// retain necessary write checks before execution and all checks at completion.
func ValidateCompositeWritePhase(kind Kind, checks []Check) error {
	if kind != KindAnyOf {
		return nil
	}
	writeChecks := 0
	for _, check := range checks {
		if check.Kind == KindDenyWrite {
			writeChecks++
		}
	}
	if writeChecks > 0 && writeChecks != len(checks) {
		return fmt.Errorf("any_of cannot mix deny_write with other check kinds across enforcement phases; separate prevention and completion rules, or use all_of when every check is required")
	}
	return nil
}

// ValidateCompositeCommandPhase rejects command-prevention shapes that cannot
// be decided before execution without silently weakening polarity. Nested
// composite checks are rejected by authoring. Mixed forbid_command checks under
// all_of and any_of keep positive-match pre-command triggering; not requires
// exactly one primitive check, so double negation is not representable.
func ValidateCompositeCommandPhase(kind Kind, checks []Check) error {
	if kind != KindNot {
		return nil
	}
	if len(checks) != 1 {
		return fmt.Errorf("kind not requires exactly one check")
	}
	if checks[0].Kind.IsComposite() {
		return fmt.Errorf("nested composite kinds are not supported in v1; flatten the rule")
	}
	return nil
}
