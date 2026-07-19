package hooks

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type bunHookRecord struct {
	event   string
	payload map[string]interface{}
}

func TestGeneratedBunPluginsPreserveHostContracts(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatalf("Bun is required to verify generated OpenCode and Kilo plugins: %v", err)
	}

	for _, kind := range []string{KindOpenCode, KindKilo} {
		kind := kind
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			repo := t.TempDir()
			artifact, err := Generate(kind)
			if err != nil {
				t.Fatalf("generate %s plugin: %v", kind, err)
			}
			pluginPath := filepath.Join(repo, filepath.FromSlash(artifact.TargetPath))
			if err := os.MkdirAll(filepath.Dir(pluginPath), 0o755); err != nil {
				t.Fatalf("create plugin directory: %v", err)
			}
			if err := os.WriteFile(pluginPath, []byte(artifact.Content), 0o644); err != nil {
				t.Fatalf("write plugin: %v", err)
			}

			logPath := filepath.Join(repo, "hook-records.jsonl")
			wrapperPath := filepath.Join(repo, "tools", "reconc", "bin", "hook")
			if err := os.MkdirAll(filepath.Dir(wrapperPath), 0o755); err != nil {
				t.Fatalf("create wrapper directory: %v", err)
			}
			wrapper := `#!/bin/sh
set -eu
event="$1"
payload="$(cat)"
printf '%s\t%s\n' "$event" "$payload" >> "$RECONC_TEST_LOG"
case "$event" in
  *-permission-request) printf '%s\n' '{"permissionDecision":"deny"}' ;;
  *-pre-compaction) printf '%s\n' '{"hookSpecificOutput":{"additionalContext":"recovery-packet"}}' ;;
esac
`
			if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
				t.Fatalf("write hook wrapper: %v", err)
			}

			driverPath := filepath.Join(repo, "plugin-contract.js")
			driver := `const pluginPath = Bun.argv[2]
const kind = Bun.argv[3]
const pluginModule = await import("file://" + pluginPath + "?contract=" + Date.now())
const factory = kind === "opencode" ? pluginModule.ReconcOpenCodePlugin : pluginModule.default.server
if (typeof factory !== "function") throw new Error("plugin factory missing")
const hooks = await factory({
  directory: Bun.argv[4],
  worktree: Bun.argv[4],
  client: { session: { prompt: async () => {} } },
})
const sessionID = "ses_contract"
await hooks["chat.message"](
  { sessionID, messageID: "msg_user" },
  { parts: [{ type: "text", text: "ship it" }] },
)
await hooks["tool.execute.after"](
  { sessionID, tool: "bash", callID: "call_ok", args: { command: "printf ok" } },
  { title: "completed", output: "actual-output", metadata: { exitCode: 0 } },
)
const permissionOutput = { status: "ask" }
await hooks["permission.ask"](
  { sessionID, type: "bash", title: "rm protected", pattern: ["protected.txt"] },
  permissionOutput,
)
if (permissionOutput.status !== "deny") throw new Error("permission denial was not applied")
const compactionOutput = { context: [] }
await hooks["experimental.session.compacting"]({ sessionID }, compactionOutput)
if (!compactionOutput.context.includes("recovery-packet")) throw new Error("compaction context was not applied")
const failureEvent = {
  type: "message.part.updated",
  properties: {
    part: {
      type: "tool",
      sessionID,
      messageID: "msg_tool",
      callID: "call_failed",
      tool: "bash",
      state: {
        status: "error",
        input: { command: "false" },
        error: "exit status 1",
        metadata: { exitCode: 1 },
      },
    },
  },
}
await hooks.event({ event: failureEvent })
await hooks.event({ event: failureEvent })
`
			if err := os.WriteFile(driverPath, []byte(driver), 0o644); err != nil {
				t.Fatalf("write Bun contract driver: %v", err)
			}

			cmd := exec.Command(bun, driverPath, pluginPath, kind, repo)
			cmd.Env = append(os.Environ(), "RECONC_TEST_LOG="+logPath)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("run %s plugin contract: %v\n%s", kind, err, output)
			}

			records := readBunHookRecords(t, logPath)
			prefix := kind
			assertBunHookCount(t, records, prefix+"-session-start", 1)
			assertBunHookCount(t, records, prefix+"-user-prompt-submit", 1)
			assertBunHookCount(t, records, prefix+"-post-tool-use", 1)
			assertBunHookCount(t, records, prefix+"-permission-request", 1)
			assertBunHookCount(t, records, prefix+"-pre-compaction", 1)
			assertBunHookCount(t, records, prefix+"-post-tool-use-failure", 1)

			post := bunHookPayload(t, records, prefix+"-post-tool-use")
			response := post["tool_response"].(map[string]interface{})
			if response["title"] != "completed" || response["output"] != "actual-output" {
				t.Fatalf("%s post-tool payload lost host output: %#v", kind, response)
			}
			metadata := response["metadata"].(map[string]interface{})
			if metadata["exitCode"] != float64(0) {
				t.Fatalf("%s post-tool payload lost metadata: %#v", kind, metadata)
			}

			failure := bunHookPayload(t, records, prefix+"-post-tool-use-failure")
			if failure["error"] != "exit status 1" {
				t.Fatalf("%s failure payload lost error: %#v", kind, failure)
			}
			input := failure["tool_input"].(map[string]interface{})
			if input["command"] != "false" {
				t.Fatalf("%s failure payload lost input: %#v", kind, input)
			}
		})
	}
}

func readBunHookRecords(t *testing.T, path string) []bunHookRecord {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read hook records: %v", err)
	}
	var records []bunHookRecord
	for lineNumber, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		event, payloadText, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("record line %d has no event separator: %q", lineNumber+1, line)
		}
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(payloadText), &payload); err != nil {
			t.Fatalf("decode record line %d: %v", lineNumber+1, err)
		}
		records = append(records, bunHookRecord{event: event, payload: payload})
	}
	return records
}

func assertBunHookCount(t *testing.T, records []bunHookRecord, event string, want int) {
	t.Helper()
	got := 0
	for _, record := range records {
		if record.event == event {
			got++
		}
	}
	if got != want {
		t.Fatalf("hook event %s count = %d, want %d; records=%#v", event, got, want, records)
	}
}

func bunHookPayload(t *testing.T, records []bunHookRecord, event string) map[string]interface{} {
	t.Helper()
	for _, record := range records {
		if record.event == event {
			return record.payload
		}
	}
	t.Fatalf("hook event %s not recorded", event)
	return nil
}
