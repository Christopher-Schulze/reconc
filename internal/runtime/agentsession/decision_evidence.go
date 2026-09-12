package agentsession

import "reconc.dev/reconc/internal/runtime"

// PolicyDecision reports a complete, error-free policy evaluation, including a
// validated cache hit. Call before host response adaptation, which may change
// the exit code or discard the internal classification. An unclassified allow
// or fail-closed error is not evidence of a policy evaluation.
func (r Result) PolicyDecision() (runtime.Decision, bool) {
	if r.Err != nil {
		return "", false
	}
	switch {
	case r.decisionClass == preDecisionResultPass && r.ExitCode == 0:
		return runtime.DecisionPass, true
	case r.decisionClass == preDecisionResultBlock && r.ExitCode == 2:
		return runtime.DecisionBlock, true
	default:
		return "", false
	}
}
