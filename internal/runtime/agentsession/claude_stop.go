package agentsession

import "strings"

// AdaptClaudeStopResult makes a Claude Code Stop block unambiguous. Claude
// treats exit code 2 with empty stdout and a missing-file message on stderr
// as a non-blocking error, so a blocking Stop also carries the JSON decision.
func AdaptClaudeStopResult(result Result) Result {
	if result.ExitCode != 2 || result.Stdout != "" {
		return result
	}
	return resultWithHookJSON(result, ClaudeStopBlock(result.Stderr))
}

// ClaudeStopBlock is the Stop decision that keeps a Claude Code session
// working with reason as its instruction.
func ClaudeStopBlock(reason string) map[string]string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "reconc hook (stop): the stop gate did not pass"
	}
	return map[string]string{"decision": "block", "reason": boundHostReason(reason)}
}
