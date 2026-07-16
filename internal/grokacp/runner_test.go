package grokacp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestRunContinuesSameGrokACPSessionUntilReconcIsClean(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake ACP executable is a POSIX script")
	}
	repo := configuredGrokRepo(t)
	fake := fakeGrokBinary(t)
	var stdout, stderr bytes.Buffer
	var stops atomic.Int32
	dependencies := defaultDependencies
	dependencies.preflight = func(context.Context, string, string, commandRunner) error { return nil }
	dependencies.stop = func(_ string, payload []byte) agentsession.Result {
		var body map[string]interface{}
		if err := json.Unmarshal(payload, &body); err != nil {
			t.Fatal(err)
		}
		if body["session_id"] != "grok-test" || body["strict_continuation"] != true {
			t.Fatalf("strict stop payload = %#v", body)
		}
		if stops.Add(1) == 1 {
			return agentsession.Result{Stdout: `{"decision":"block","reason":"continue the exact task"}`}
		}
		return agentsession.Result{}
	}

	err := run(context.Background(), Options{
		RepoRoot:         repo,
		GrokBinary:       fake,
		Prompt:           "do the work",
		MaxContinuations: 2,
		Stdout:           &stdout,
		Stderr:           &stderr,
	}, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if stops.Load() != 2 {
		t.Fatalf("Stop checks = %d, want 2", stops.Load())
	}
	if stdout.String() != "first turn\nsecond turn\n" {
		t.Fatalf("streamed output = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "continuation 1/2") {
		t.Fatalf("continuation status missing: %q", stderr.String())
	}
}

func TestRunReturnsPolicyBlockedAfterContinuationLimit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake ACP executable is a POSIX script")
	}
	repo := configuredGrokRepo(t)
	dependencies := defaultDependencies
	dependencies.preflight = func(context.Context, string, string, commandRunner) error { return nil }
	dependencies.stop = func(_ string, _ []byte) agentsession.Result {
		return agentsession.Result{Stdout: `{"decision":"block","reason":"still blocked"}`}
	}
	err := run(context.Background(), Options{
		RepoRoot:         repo,
		GrokBinary:       fakeGrokBinary(t),
		Prompt:           "do the work",
		MaxContinuations: 1,
	}, dependencies)
	var blocked *PolicyBlockedError
	if !errors.As(err, &blocked) || !strings.Contains(blocked.Error(), "still blocked") {
		t.Fatalf("expected PolicyBlockedError, got %v", err)
	}
}

func TestGrokClientDoesNotAdvertiseUnimplementedReverseTools(t *testing.T) {
	body, err := json.Marshal(grokClientCapabilities())
	if err != nil {
		t.Fatal(err)
	}
	var capabilities struct {
		FS struct {
			Read  bool `json:"readTextFile"`
			Write bool `json:"writeTextFile"`
		} `json:"fs"`
		Terminal bool `json:"terminal"`
	}
	if err := json.Unmarshal(body, &capabilities); err != nil {
		t.Fatal(err)
	}
	if capabilities.FS.Read || capabilities.FS.Write || capabilities.Terminal {
		t.Fatalf("unimplemented ACP reverse tools were advertised: %s", body)
	}
}

func TestPreflightRejectsUntrustedOrUnloadedGrokHook(t *testing.T) {
	repo := configuredGrokRepo(t)
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{name: "untrusted", output: `{"projectTrusted":false,"hooks":[]}`, want: "does not trust"},
		{name: "unloaded", output: `{"projectTrusted":true,"hooks":[]}`, want: "did not load"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
				return exec.CommandContext(ctx, "sh", "-c", "printf '%s' \"$GROK_INSPECT\"")
			}
			t.Setenv("GROK_INSPECT", test.output)
			err := preflight(context.Background(), repo, "grok", command)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("preflight error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestPreflightAcceptsEveryLoadedNativeGrokRoute(t *testing.T) {
	repo := configuredGrokRepo(t)
	platform, ok := hooks.PlatformForKind(hooks.KindGrok)
	if !ok {
		t.Fatal("Grok platform is not registered")
	}
	type source struct {
		Type string `json:"type"`
		Path string `json:"path"`
	}
	type inspectedHook struct {
		Target string `json:"target"`
		Source source `json:"source"`
	}
	inspection := struct {
		ProjectTrusted bool            `json:"projectTrusted"`
		Hooks          []inspectedHook `json:"hooks"`
	}{ProjectTrusted: true}
	for _, capability := range platform.Capabilities {
		for _, event := range capability.RuntimeEvents {
			inspection.Hooks = append(inspection.Hooks, inspectedHook{
				Target: event,
				Source: source{Type: "project", Path: filepath.Join(repo, ".grok", "hooks")},
			})
		}
	}
	body, err := json.Marshal(inspection)
	if err != nil {
		t.Fatal(err)
	}
	command := func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "printf '%s' \"$GROK_INSPECT\"")
	}
	t.Setenv("GROK_INSPECT", string(body))
	if err := preflight(context.Background(), repo, "grok", command); err != nil {
		t.Fatal(err)
	}
}

func configuredGrokRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if _, err := hooks.Install(hooks.KindGrok, repo, false); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(repo, filepath.FromSlash(hooks.WrapperPath))
	if err := os.MkdirAll(filepath.Dir(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wrapper, []byte(hooks.GenerateWrapper().Content), 0o755); err != nil {
		t.Fatal(err)
	}
	return repo
}

func fakeGrokBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "grok")
	script := `#!/bin/sh
if [ "${1:-}" = "--cwd" ]; then
  repo="$2"
  printf '{"projectTrusted":true,"hooks":[{"target":"tools/reconc/bin/hook grok-pre-tool-use .","source":{"type":"project","path":"%s/.grok/hooks"}}]}\n' "$repo"
  exit 0
fi
count=0
while IFS= read -r line; do
  count=$((count + 1))
  case "$count" in
    1) printf '{"jsonrpc":"2.0","id":1,"result":{"authMethods":[{"id":"cached_token"}]}}\n' ;;
    2) printf '{"jsonrpc":"2.0","id":2,"result":{}}\n' ;;
    3) printf '{"jsonrpc":"2.0","id":3,"result":{"sessionId":"grok-test"}}\n' ;;
    4)
      printf '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"grok-test","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"first turn"}}}}\n'
      printf '{"jsonrpc":"2.0","id":4,"result":{"stopReason":"end_turn"}}\n'
      ;;
    5)
      printf '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"grok-test","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"second turn"}}}}\n'
      printf '{"jsonrpc":"2.0","id":5,"result":{"stopReason":"end_turn"}}\n'
      ;;
  esac
done
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
