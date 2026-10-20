package runtime

import (
	"context"

	"reconc.dev/reconc/internal/policy"
)

func evaluateRuntimePlanWithRootResolver(root string, plan *runtimePlan, inputs ExecutionInputs, allowedKinds map[policy.Kind]struct{}, phase evaluationPhase, resolveRoot func(string) (string, error)) (*CheckReport, error) {
	return evaluateRuntimePlanWithRootResolverContext(context.Background(), root, plan, inputs, allowedKinds, phase, resolveRoot)
}
