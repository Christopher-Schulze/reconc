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
