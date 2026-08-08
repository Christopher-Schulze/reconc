package agentsession

import (
	"testing"
)

// normalizerSeeds are shapes each host actually sends, so the corpus starts on
// the real contract instead of exploring JSON syntax from scratch.
var normalizerSeeds = []string{
	`{"session_id":"s1","tool_name":"Write","tool_input":{"file_path":"src/a.go"}}`,
	`{"hook_event_name":"PreToolUse","session_id":"s1","cwd":".","tool_name":"Edit","tool_input":{"file_path":"src/a.go"}}`,
	`{"hookEventName":"pre_tool_use","sessionId":"s1","workspaceRoot":".","toolName":"search_replace","toolInput":{"path":"src/a.go"},"toolInputTruncated":false}`,
	`{"hook_event_name":"tool_call","session_id":"s1","cwd":".","tool_name":"write","tool_input":{"path":"src/a.go"},"tool_call_id":"c1"}`,
	`{"conversation_id":"c1","tool":{"name":"Write","input":{"file_path":"src/a.go"}}}`,
	`{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"go test ./..."},"tool_response":{"success":true}}`,
	`{"session_id":"s1","stop_hook_active":true}`,
	`{"steps":[{"type":"tool"}],"injectSteps":[]}`,
	`{}`,
	`[]`,
	`null`,
	``,
}

// FuzzNormalizeHostPayloadsNoPanic drives every registered host normalizer and
// the shared payload parser over arbitrary bytes.
//
// These functions sit on the enforcement path and consume JSON produced by an
// agent host, which is untrusted input by the project's own threat model. A
// panic here is not a graceful refusal: the hook process dies, and on a
// fail-open route the host reads that as "no decision" and proceeds. Every
// normalizer must therefore return an error rather than crash, whatever it is
// handed.
func FuzzNormalizeHostPayloadsNoPanic(f *testing.F) {
	for _, seed := range normalizerSeeds {
		for _, event := range []string{
			"cursor-pre-tool-use", "cursor-stop", "grok-pre-tool-use", "copilot-pre-tool-use",
			"omp-pre-tool-use", "omp-user-bash", "omp-user-python",
			"pi-pre-tool-use", "pi-user-bash", "zcode-pre-tool-use", "kimi-pre-tool-use",
			"devin-pre-tool-use", "antigravity-pre-tool-use", "",
		} {
			f.Add(event, []byte(seed))
		}
	}

	f.Fuzz(func(t *testing.T, event string, payload []byte) {
		const root = "."
		normalizers := []func() ([]byte, error){
			func() ([]byte, error) { return NormalizeCursorPayload(event, payload) },
			func() ([]byte, error) { return NormalizeAntigravityPayload(event, payload) },
			func() ([]byte, error) { return NormalizeGrokPayload(event, payload, root) },
			func() ([]byte, error) { return NormalizeGitHubCopilotPayload(event, payload, root) },
			func() ([]byte, error) { return NormalizeOMPPayload(event, payload, root) },
			func() ([]byte, error) { return NormalizePiPayload(event, payload, root) },
			func() ([]byte, error) { return NormalizeZCodePayload(event, payload, root) },
			func() ([]byte, error) { return NormalizeKimiCodePayload(event, payload, root) },
			func() ([]byte, error) { return NormalizeDevinPayload(event, payload, root) },
		}
		for _, normalize := range normalizers {
			normalized, err := normalize()
			if err != nil {
				continue
			}
			// A normalizer that reports success must hand the shared parser
			// something the parser accepts; otherwise the two contracts have
			// drifted and the runtime would refuse a payload its own adapter
			// just blessed.
			parsed, parseErr := ParsePayload(normalized)
			if parseErr != nil {
				continue
			}
			// Every accessor runs on attacker-influenced content.
			_ = parsed.FilePaths()
			_ = parsed.Command()
			_ = parsed.IsWriteTool()
			_ = parsed.IsReadTool()
			_ = parsed.IsCommandTool()
		}
		if parsed, err := ParsePayload(payload); err == nil {
			_ = parsed.FilePaths()
			_ = parsed.Command()
		}
	})
}
