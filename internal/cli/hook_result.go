package cli

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

func boundHookResult(result agentsession.Result, route hooks.RuntimeRoute) agentsession.Result {
	limit := route.MaxOutputBytes
	if limit <= 0 {
		return result
	}
	stderrLimit := limit / 2
	stdoutLimit := limit - stderrLimit
	result.Stderr = truncateWithSuffix(result.Stderr, stderrLimit, "\n[reconc stderr truncated]")
	if len(result.Stdout) <= stdoutLimit {
		return result
	}
	// An oversized result must still deliver a decision, and it must deliver it
	// through the channel the host actually reads.
	//
	// Cursor, GitHub Copilot, and Grok express deny/block as exit 0 plus JSON:
	// clearing stdout there hands the host an undecided non-zero exit, which
	// several of them treat as a failed hook rather than a block, and on Copilot
	// the non-zero exit additionally re-triggers the installed shell fallback so
	// two decision bodies reach the host. Those routes get a compact envelope
	// that fits the whole budget, with the adapter's exit code preserved.
	//
	// Everywhere else the exit code carries the decision, where empty stdout
	// plus exit 2 already is the fail-closed shape.
	if route.ErrorPolicy != hooks.FailureBlock {
		result.Stdout = ""
		result.Stderr = oversizeDiagnostic(result.Stderr, stderrLimit)
		result.ExitCode = 0
		return result
	}
	if envelope := failClosedOversizeEnvelope(route); envelope != "" && len(envelope) <= limit {
		result.Stdout = envelope
		result.Stderr = oversizeDiagnostic(result.Stderr, limit-len(envelope))
		result.ExitCode = 0
		return result
	}
	result.Stdout = ""
	result.Stderr = oversizeDiagnostic(result.Stderr, stderrLimit)
	result.ExitCode = 2
	return result
}

const hookOversizeDiagnostic = "reconc hook output exceeded the platform byte budget"

// oversizeDiagnostic keeps the runtime's own explanation when it has one. The
// oversized stream is stdout, so a bounded stderr still carries the reason the
// decision was made, and replacing it with the byte-budget notice alone would
// leave the operator with a symptom instead of a cause.
func oversizeDiagnostic(stderr string, limit int) string {
	reason := strings.TrimSpace(stderr)
	if reason == "" {
		return truncateUTF8(hookOversizeDiagnostic, limit)
	}
	marker := "\n[" + hookOversizeDiagnostic + "]"
	if len(reason)+len(marker) <= limit {
		return reason + marker
	}
	return truncateWithSuffix(reason, limit, marker)
}

// failClosedOversizeEnvelope returns the smallest JSON body that denies or
// blocks on a platform whose decision travels in stdout, mirroring the shape
// the matching adapter emits. An empty return means the platform carries its
// decision in the exit code instead.
func failClosedOversizeEnvelope(route hooks.RuntimeRoute) string {
	const reason = "reconc hook output budget exceeded"
	stopEvent := route.Event == hooks.EventStop || route.Event == hooks.EventSubagentStop
	var body map[string]interface{}
	switch route.PlatformKind {
	case hooks.KindCursor:
		if stopEvent {
			body = map[string]interface{}{"continue": false, "user_message": reason, "agent_message": reason}
			break
		}
		body = map[string]interface{}{"permission": "deny", "user_message": reason, "agent_message": reason}
	case hooks.KindGitHubCopilot:
		switch {
		case route.Event == hooks.EventPermissionRequest:
			body = map[string]interface{}{"behavior": "deny", "message": reason}
		case stopEvent:
			body = map[string]interface{}{"decision": "block", "reason": reason}
		default:
			body = map[string]interface{}{"permissionDecision": "deny", "permissionDecisionReason": reason}
		}
	case hooks.KindGrok:
		if stopEvent {
			body = map[string]interface{}{"decision": "block", "reason": reason}
			break
		}
		body = map[string]interface{}{"decision": "deny", "reason": reason}
	default:
		return ""
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func truncateWithSuffix(value string, limit int, suffix string) string {
	if len(value) <= limit {
		return value
	}
	if limit <= len(suffix) {
		return truncateUTF8(value, limit)
	}
	return truncateUTF8(value, limit-len(suffix)) + suffix
}

func truncateUTF8(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit]
}
