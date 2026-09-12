package agentsession

import (
	"fmt"
	"strings"

	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/runtime"
)

// PermissionRequest uses the same effect classification as the pre-tool MCP
// route, but returns only Codex's nested deny decision. Reconc never auto-allows.
func runCodexPermissionResolved(root string, body []byte, evaluator *runtime.Evaluator, stopCache *StopDecisionCache) Result {
	payload, err := ParsePayload(body)
	if err != nil {
		return adaptPreDecision(Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc permission: %s", err)}, true)
	}
	if !strings.HasPrefix(payload.ToolName, namespacedMCPPrefix) {
		return runPreDecisionResolvedWithEvaluatorAndStopCache(root, body, true, evaluator, stopCache)
	}
	normalized, err := NormalizeNamespacedMCPPayload(policy.MCPPlatformCodex, true, body)
	if err != nil {
		return adaptPreDecision(Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc MCP permission: %s", err)}, true)
	}
	return adaptPreDecision(runMCPBeforeResolvedWithEvaluatorAndStopCache(root, normalized, true, evaluator, stopCache), true)
}
