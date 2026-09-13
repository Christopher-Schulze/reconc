package agentsession

import "fmt"

// Devin's PostToolUse.success describes the tool callback, not the shell exit:
// the installed host reports success=true even for an exact `exit 7` command.
// Only structured, internally consistent exit metadata can certify a direct
// shell outcome; text output remains a passive observation.
func runDevinPostToolUseResolved(root string, body []byte) Result {
	payload, err := ParsePayload(body)
	if err != nil {
		return Result{Stderr: fmt.Sprintf("reconc hook (Devin post, warn): %s", err)}
	}
	if !payload.IsReadTool() && !payload.IsWriteTool() && !payload.IsCommandTool() {
		return runPassiveEventResolved(root, body)
	}
	bound, err := consumeDevinToolCorrelation(root, payload)
	if err != nil {
		return Result{Stderr: fmt.Sprintf("reconc hook (Devin post, warn): tool correlation failed: %s", err)}
	}
	if !bound {
		return Result{Stderr: "reconc hook (Devin post, warn): tool input has no matching allowed PreToolUse; no evidence recorded"}
	}
	success, explicit := payload.ToolResponse["success"].(bool)
	if !explicit {
		result := runPassiveEventResolved(root, body)
		if result.Stderr == "" {
			result.Stderr = "reconc: Devin tool completion has no native success outcome; no repository evidence recorded"
		}
		return result
	}
	if !success || payload.IsInterrupt != nil && *payload.IsInterrupt || toolResponseFailed(payload) {
		return runPostToolUseFailureResolved(root, body)
	}
	if !payload.IsCommandTool() {
		return runPostToolUseCompleteResolved(root, body)
	}
	_, hasExit, _ := strictExitCode(payload.ToolResponse)
	if hasExit {
		return runPostToolUseCompleteStrictResolved(root, body)
	}
	result := runPassiveEventResolved(root, body)
	if result.Stderr == "" {
		result.Stderr = "reconc: Devin exec completion has no authoritative shell exit status; use reconc exec for command-success evidence"
	}
	return result
}
