package runtime

import (
	"context"

	"reconc.dev/reconc/internal/policy"
)

type evaluationPhase uint8

const (
	evaluationComplete evaluationPhase = iota
	evaluationPreCommand
	evaluationPreWrite
)

// CheckRepoPolicyForPreWrite evaluates indexed write prevention, including
// composite deny_write checks, without demanding future completion evidence.
func (e *Evaluator) CheckRepoPolicyForPreWrite(startPath string, inputs ExecutionInputs) (*CheckReport, error) {
	return e.CheckRepoPolicyForPreWriteContext(context.Background(), startPath, inputs)
}

// CheckRepoPolicyForPreWriteContext evaluates write prevention under the caller lifecycle.
func (e *Evaluator) CheckRepoPolicyForPreWriteContext(ctx context.Context, startPath string, inputs ExecutionInputs) (*CheckReport, error) {
	return e.checkRepoPolicy(ctx, startPath, inputs, nil, evaluationPreWrite)
}

func runtimeRulePreventsWrite(rule *policy.Rule) bool {
	if rule.Kind == policy.KindDenyWrite || rule.Kind == policy.KindRequireRead {
		return true
	}
	if !rule.Kind.IsComposite() {
		return false
	}
	for _, check := range rule.Checks {
		if check.Kind == policy.KindDenyWrite {
			return true
		}
	}
	return false
}
