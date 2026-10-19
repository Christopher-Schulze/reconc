package agentsession

import (
	"encoding/json"
	"fmt"
)

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
		return codexPostToolUseOutput(runPostToolUseFailureResolved(root, body))
	}
	_, hasExit, _ := strictExitCode(payload.ToolResponse)
	if hasExit || toolResponseFailed(payload) {
		return codexPostToolUseOutput(runPostToolUseCompleteStrictResolved(root, body))
	}
	result := runPassiveEventResolved(root, body)
	if result.Stderr == "" {
		result.Stderr = "reconc: Codex shell completion has no authoritative exit status; use reconc exec for command-success evidence"
	}
	return result
}

// codexPostToolUseOutput names Codex's own event in a failure response. Codex
// has no PostToolUseFailure event: its PostToolUse output must say
// PostToolUse, or Codex rejects the JSON and drops the failure context.
func codexPostToolUseOutput(result Result) Result {
	if result.Stdout == "" {
		return result
	}
	var output struct {
		HookSpecificOutput map[string]any `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &output); err != nil || output.HookSpecificOutput == nil {
		return result
	}
	if output.HookSpecificOutput["hookEventName"] != "PostToolUseFailure" {
		return result
	}
	output.HookSpecificOutput["hookEventName"] = "PostToolUse"
	return resultWithHookJSON(result, output)
}
