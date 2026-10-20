package agentsession

import (
	"time"

	"reconc.dev/reconc/internal/runtime"
)

func preDecisionInputsForPayload(root string, payload *HookPayload) (preDecisionInputs, bool) {
	return preDecisionInputsForPayloadWithEvaluator(root, payload, nil)
}

func preDecisionInputsForPayloadWithEvaluator(root string, payload *HookPayload, evaluator *runtime.Evaluator) (preDecisionInputs, bool) {
	return preDecisionInputsForPayloadWithEvaluatorAndStopCache(root, payload, evaluator, nil)
}

func resamplePreDecisionInputs(
	root string,
	payload *HookPayload,
	baseline preDecisionInputs,
) (preDecisionInputs, bool) {
	return resamplePreDecisionInputsWithEvaluator(root, payload, baseline, nil)
}

func resamplePreDecisionInputsWithEvaluator(
	root string,
	payload *HookPayload,
	baseline preDecisionInputs,
	evaluator *runtime.Evaluator,
) (preDecisionInputs, bool) {
	return resamplePreDecisionInputsWithEvaluatorAndStopCache(root, payload, baseline, evaluator, nil)
}

func preDecisionCachePath(root string, payloadBytes []byte) string {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return ""
	}
	return preDecisionCachePathForPayload(root, payload)
}

func readPreDecisionCache(root string, payloadBytes []byte, expectedKey string) (Result, bool) {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return Result{}, false
	}
	return readPreDecisionCacheForPayload(root, payload, expectedKey)
}

func readPreDecisionCacheForPayload(root string, payload *HookPayload, expectedKey string) (Result, bool) {
	inputs, ok := preDecisionInputsForPayload(root, payload)
	if !ok || inputs.key != expectedKey {
		return Result{}, false
	}
	return readPreDecisionCacheForInputs(root, payload, inputs)
}

func readPreDecisionCacheForInputs(
	root string,
	payload *HookPayload,
	expected preDecisionInputs,
) (Result, bool) {
	cached, ok := readPreDecisionCacheCandidate(root, payload)
	if !ok || cached.Key != expected.key {
		return Result{}, false
	}
	current, ok := resamplePreDecisionInputs(root, payload, expected)
	if !ok || !expected.identity.equal(current.identity) || !current.cacheableAt(time.Now()) {
		return Result{}, false
	}
	return Result{ExitCode: cached.ExitCode, Stderr: cached.Stderr, decisionClass: cached.DecisionClass}, true
}
