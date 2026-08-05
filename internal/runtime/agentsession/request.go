package agentsession

import "reconc.dev/reconc/internal/runtime"

// HookHandler identifies one normalized agent-session operation. Platform
// adapters select the handler after normalizing their payload; this package
// owns the resolved-root dispatch so a request never falls back to string-based
// APIs that rediscover the same repository identity.
type HookHandler string

const (
	HookHandlerSessionStart          HookHandler = "session-start"
	HookHandlerPassive               HookHandler = "passive"
	HookHandlerWorkspaceOpen         HookHandler = "workspace-open"
	HookHandlerPreToolUse            HookHandler = "pre-tool-use"
	HookHandlerPermissionRequest     HookHandler = "permission-request"
	HookHandlerPostToolUse           HookHandler = "post-tool-use"
	HookHandlerPostToolUseFailure    HookHandler = "post-tool-use-failure"
	HookHandlerPostToolUseComplete   HookHandler = "post-tool-use-complete"
	HookHandlerPostToolUseStrict     HookHandler = "post-tool-use-strict"
	HookHandlerMCPBefore             HookHandler = "mcp-before"
	HookHandlerMCPAfter              HookHandler = "mcp-after"
	HookHandlerMCPAwarePreToolUse    HookHandler = "mcp-aware-pre-tool-use"
	HookHandlerMCPAwarePostToolUse   HookHandler = "mcp-aware-post-tool-use"
	HookHandlerStop                  HookHandler = "stop"
	HookHandlerSessionEnd            HookHandler = "session-end"
	HookHandlerPostCompaction        HookHandler = "post-compaction"
	HookHandlerAntigravityPreInvoke  HookHandler = "antigravity-pre-invocation"
	HookHandlerAntigravityPreTool    HookHandler = "antigravity-pre-tool-use"
	HookHandlerAntigravityPostTool   HookHandler = "antigravity-post-tool-use"
	HookHandlerAntigravityPostInvoke HookHandler = "antigravity-post-invocation"
	HookHandlerAntigravityStop       HookHandler = "antigravity-stop"
)

// RunHookRequest executes a normalized hook payload against one validated
// repository root. runtimeEvent is explicit request data, never process-global
// environment state, so concurrent in-process requests cannot cross-contaminate
// runtime attribution.
func RunHookRequest(root ResolvedRepoRoot, handler HookHandler, runtimeEvent string, payload []byte) Result {
	return RunHookRequestWithEvaluator(root, handler, runtimeEvent, payload, runtime.NewEvaluator())
}

// RunHookRequestWithEvaluator lets a session-owned hook worker reuse one
// immutable policy-plan owner while preserving the same normalized dispatch.
func RunHookRequestWithEvaluator(root ResolvedRepoRoot, handler HookHandler, runtimeEvent string, payload []byte, evaluator *runtime.Evaluator) Result {
	if evaluator == nil {
		evaluator = runtime.NewEvaluator()
	}
	if root.path == "" {
		return Result{ExitCode: 2, Stderr: "reconc hook: resolved repository root is empty"}
	}
	switch handler {
	case HookHandlerSessionStart:
		return runSessionStartResolved(root.path, payload, normalizeRuntimeName(runtimeEvent))
	case HookHandlerPassive:
		return runPassiveEventResolved(root.path, payload)
	case HookHandlerWorkspaceOpen:
		return Result{}
	case HookHandlerPreToolUse:
		return runPreDecisionResolvedWithEvaluator(root.path, payload, false, evaluator)
	case HookHandlerPermissionRequest:
		return runPreDecisionResolvedWithEvaluator(root.path, payload, true, evaluator)
	case HookHandlerPostToolUse:
		return runPostToolUseResolved(root.path, payload)
	case HookHandlerPostToolUseFailure:
		return runPostToolUseFailureResolved(root.path, payload)
	case HookHandlerPostToolUseComplete:
		return runPostToolUseCompleteResolved(root.path, payload)
	case HookHandlerPostToolUseStrict:
		return runPostToolUseCompleteStrictResolved(root.path, payload)
	case HookHandlerMCPBefore:
		return runMCPBeforeResolvedWithEvaluator(root.path, payload, true, evaluator)
	case HookHandlerMCPAfter:
		return runMCPAfterResolvedWithEvaluator(root.path, payload, true, evaluator)
	case HookHandlerMCPAwarePreToolUse:
		return runMCPBeforeResolvedWithEvaluator(root.path, payload, false, evaluator)
	case HookHandlerMCPAwarePostToolUse:
		return runMCPAfterResolvedWithEvaluator(root.path, payload, false, evaluator)
	case HookHandlerStop:
		return runStopResolvedWithEvaluator(root.path, payload, normalizeRuntimeName(runtimeEvent), evaluator)
	case HookHandlerSessionEnd:
		return runSessionEndResolved(root.path, payload)
	case HookHandlerPostCompaction:
		return runPostCompactionResolved(root.path, payload)
	case HookHandlerAntigravityPreInvoke:
		return runAntigravityPreInvocationResolved(root.path, payload)
	case HookHandlerAntigravityPreTool:
		return runAntigravityPreToolUseResolvedWithEvaluator(root.path, payload, evaluator)
	case HookHandlerAntigravityPostTool:
		return runAntigravityPostToolUseResolved(root.path, payload)
	case HookHandlerAntigravityPostInvoke:
		return runAntigravityPostInvocationResolved(payload)
	case HookHandlerAntigravityStop:
		return runAntigravityStopResolvedWithEvaluator(root.path, payload, normalizeRuntimeName(runtimeEvent), evaluator)
	default:
		return Result{ExitCode: 1, Stderr: "reconc hook: unsupported normalized handler"}
	}
}
