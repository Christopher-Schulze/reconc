package impactlab

import (
	"fmt"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/runtime"
)

// BindCapturedActionExpectation evaluates one already privacy-bounded action
// fixture through the production runtime and binds its exact current outcome.
// The caller must independently prove that the minimized fixture represents
// the retained observation it is derived from.
func BindCapturedActionExpectation(
	replayCase Case,
	compiled runtime.CompiledActionRuntime,
) (Case, error) {
	if replayCase.Action == nil ||
		(replayCase.Kind != CaseActionPre && replayCase.Kind != CaseActionPost) {
		return Case{}, fmt.Errorf("captured action case is missing its typed fixture")
	}
	if compiled.Evaluator == nil {
		return Case{}, fmt.Errorf("compiled action evaluator is unavailable")
	}
	if replayCase.Action.Expected.Decision == action.DecisionRequireApproval &&
		replayCase.Action.Expected.Approval == nil {
		replayCase.Action.Expected.Approval = &ActionApprovalAssertion{}
	}
	observation, err := evaluateActionScenario(*replayCase.Action, compiled)
	if err != nil {
		return Case{}, err
	}
	replayCase.Action.Expected = observation.Outcome
	if _, err := validateActionCase(replayCase.Kind, *replayCase.Action); err != nil {
		return Case{}, fmt.Errorf("validate captured action case: %w", err)
	}
	return replayCase, nil
}
