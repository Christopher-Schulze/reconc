package agentsession

import "fmt"

// Codex's unified-exec PostToolUse response is stdout text, including for
// nonzero exits. Its terminal callback proves completion, not shell success.
// Preserve explicit outcomes from compatible hosts without parsing command
// output as trusted execution metadata.
func runCodexPostToolUseResolved(root string, body []byte) Result {
	payload, err := ParsePayload(body)
	if err != nil {
		return Result{Stderr: fmt.Sprintf("reconc hook (Codex post, warn): %s", err)}
	}
	if !payload.IsCommandTool() {
		return runPostToolUseCompleteResolved(root, body)
	}
	if payload.IsInterrupt != nil && *payload.IsInterrupt {
		return runPostToolUseFailureResolved(root, body)
	}
	_, hasExit, _ := strictExitCode(payload.ToolResponse)
	if hasExit || toolResponseFailed(payload) {
		return runPostToolUseCompleteStrictResolved(root, body)
	}
	result := runPassiveEventResolved(root, body)
	if result.Stderr == "" {
		result.Stderr = "reconc: Codex shell completion has no authoritative exit status; use reconc exec for command-success evidence"
	}
	return result
}
