package grokacp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
	var agentCmd *exec.Cmd
	dependencies.command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		agentCmd = exec.CommandContext(ctx, name, args...)
		return agentCmd
	}
	dependencies.stop = func(_ string, payload []byte) agentsession.Result {
		var body map[string]interface{}
		if err := json.Unmarshal(payload, &body); err != nil {
			t.Fatal(err)
		}
		if body["session_id"] != "grok-test" || body["strict_continuation"] != true {
			t.Fatalf("strict stop payload = %#v", body)
		}
		switch stops.Add(1) {
		case 1:
			return agentsession.Result{Stdout: `{"decision":"block","reason":"continue the exact task"}`}
		case 2:
			return agentsession.Result{Stdout: "continue once more"}
		}
		return agentsession.Result{}
	}

	err := run(context.Background(), Options{
		RepoRoot:         repo,
		GrokBinary:       fake,
		Prompt:           "do the work",
		MaxContinuations: 3,
		Stdout:           &stdout,
		Stderr:           &stderr,
	}, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if stops.Load() != 3 {
		t.Fatalf("Stop checks = %d, want 3", stops.Load())
	}
	if stdout.String() != "first turn\nsecond turn\nthird turn\n" {
		t.Fatalf("streamed output = %q", stdout.String())
	}
	steerEntries := 0
	for _, entry := range agentCmd.Env {
		if entry == SteerEnv+"=0" {
			steerEntries++
		}
	}
	if steerEntries != 1 {
		t.Fatalf("spawned agent must receive exactly one %s=0 so hooks never leader-steer, env=%v", SteerEnv, agentCmd.Env)
	}
	if !strings.Contains(stderr.String(), "continuation 1/3") || !strings.Contains(stderr.String(), "continuation 2/3") {
		t.Fatalf("continuation status missing: %q", stderr.String())
	}
}

func TestPromptValidationAndContinuationExtractionBoundaries(t *testing.T) {
	exact := strings.Repeat("x", maxPromptBytes)
	if err := validatePrompt(exact); err != nil {
		t.Fatalf("exact prompt boundary: %v", err)
	}
	if err := validatePrompt(strings.Repeat("é", maxPromptBytes/2)); err != nil {
		t.Fatalf("exact multibyte UTF-8 prompt boundary: %v", err)
	}
	for _, test := range []struct {
		name   string
		prompt string
		want   string
	}{
		{name: "limit plus one", prompt: exact + "x", want: "prompt exceeds 1048576 bytes"},
		{name: "empty", prompt: " \n\t", want: "prompt must be non-empty"},
		{name: "invalid UTF-8", prompt: string([]byte{0xff}), want: "prompt must be valid UTF-8"},
	} {
		t.Run("prompt "+test.name, func(t *testing.T) {
			if err := validatePrompt(test.prompt); err == nil || err.Error() != test.want {
				t.Fatalf("validatePrompt() error = %v, want %q", err, test.want)
			}
		})
	}

	exactJSON, err := json.Marshal(map[string]string{"reason": exact})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		stdout     string
		wantReason string
		wantBytes  int
		wantError  string
	}{
		{name: "raw reason", stdout: "  continue raw  ", wantReason: "continue raw"},
		{name: "JSON reason", stdout: `{"reason":"continue JSON"}`, wantReason: "continue JSON"},
		{name: "JSON followup", stdout: `{"followup_message":"continue followup"}`, wantReason: "continue followup"},
		{name: "JSON message", stdout: `{"message":"continue message"}`, wantReason: "continue message"},
		{name: "exact raw limit", stdout: exact, wantBytes: maxPromptBytes},
		{name: "exact JSON reason limit", stdout: string(exactJSON), wantBytes: maxPromptBytes},
		{name: "reason limit plus one", stdout: exact + "x", wantError: "prompt exceeds 1048576 bytes"},
		{name: "output envelope overflow", stdout: strings.Repeat("x", maxContinuationBytes+1), wantError: "Stop output exceeds 1052672 bytes"},
		{name: "malformed structured output", stdout: `{"reason":`, wantError: "Stop output contains malformed structured continuation data"},
		{name: "invalid UTF-8 output", stdout: string([]byte{0xff}), wantError: "Stop output must be valid UTF-8"},
		{name: "structured clean stop", stdout: `{"decision":"allow"}`},
	}
	for _, test := range tests {
		t.Run("continuation "+test.name, func(t *testing.T) {
			reason, err := continuationReason(test.stdout)
			if test.wantError != "" {
				if err == nil || err.Error() != test.wantError || reason != "" {
					t.Fatalf("continuationReason() = %d bytes, %v, want error %q", len(reason), err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("continuationReason() error = %v", err)
			}
			if test.wantBytes > 0 {
				if len(reason) != test.wantBytes || reason != exact {
					t.Fatalf("continuationReason() = %d bytes, want %d exact bytes", len(reason), test.wantBytes)
				}
			} else if reason != test.wantReason {
				t.Fatalf("continuationReason() = %q, want %q", reason, test.wantReason)
			}
		})
	}
}

func TestRunRejectsOversizedContinuationBeforeSecondPrompt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake ACP executable is a POSIX script")
	}
	repo := configuredGrokRepo(t)
	promptLog := filepath.Join(t.TempDir(), "prompts.log")
	t.Setenv("GROK_PROMPT_LOG", promptLog)
	dependencies := defaultDependencies
	dependencies.preflight = func(context.Context, string, string, commandRunner) error { return nil }
	var stops atomic.Int32
	dependencies.stop = func(_ string, _ []byte) agentsession.Result {
		stops.Add(1)
		return agentsession.Result{Stdout: strings.Repeat("x", maxPromptBytes+1)}
	}
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), Options{
		RepoRoot: repo, GrokBinary: fakeGrokBinary(t), Prompt: "do the work",
		MaxContinuations: 2, Stdout: &stdout, Stderr: &stderr,
	}, dependencies)
	if err == nil || err.Error() != "validate Grok continuation: prompt exceeds 1048576 bytes" {
		t.Fatalf("oversized continuation error = %v", err)
	}
	if stops.Load() != 1 || stdout.String() != "first turn\n" || stderr.Len() != 0 {
		t.Fatalf("oversized continuation dispatch = stops:%d stdout:%q stderr:%q", stops.Load(), stdout.String(), stderr.String())
	}
	body, readErr := os.ReadFile(promptLog)
	if readErr != nil || string(body) != "4\n" {
		t.Fatalf("ACP prompt log = %q, %v, want one initial prompt", body, readErr)
	}
}

func TestReplaceEnvironmentValueRemovesEveryPriorSpelling(t *testing.T) {
	tests := []struct {
		name            string
		caseInsensitive bool
		environment     []string
		want            []string
	}{
		{
			name:        "unix exact name",
			environment: []string{"A=1", SteerEnv + "=1", SteerEnv + "=off", "B=2"},
			want:        []string{"A=1", "B=2", SteerEnv + "=0"},
		},
		{
			name:            "windows case insensitive name",
			caseInsensitive: true,
			environment:     []string{"A=1", "reconc_grok_steer=1", SteerEnv + "=off", "B=2"},
			want:            []string{"A=1", "B=2", SteerEnv + "=0"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := replaceEnvironmentValue(test.environment, SteerEnv, "0", test.caseInsensitive)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("replaceEnvironmentValue() = %#v, want %#v", got, test.want)
			}
		})
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

func TestRunTreatsContextCancellationAsUserStop(t *testing.T) {
	repo := configuredGrokRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	dependencies := defaultDependencies
	dependencies.preflight = func(context.Context, string, string, commandRunner) error {
		cancel()
		return context.Canceled
	}
	err := run(ctx, Options{
		RepoRoot:   repo,
		GrokBinary: "grok",
		Prompt:     "do the work",
	}, dependencies)
	if err != nil {
		t.Fatalf("user cancellation must stop cleanly, got %v", err)
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

func TestPreflightRequiresGeneratorExactWrapper(t *testing.T) {
	repo := configuredGrokRepo(t)
	wrapper := filepath.Join(repo, filepath.FromSlash(hooks.WrapperPath))
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := preflight(context.Background(), repo, "grok", exec.CommandContext)
	if err == nil || !strings.Contains(err.Error(), "differs from the current Reconc generator") {
		t.Fatalf("drifted wrapper must fail preflight before grok inspect, got %v", err)
	}
}

func TestPreflightAcceptsEveryLoadedNativeGrokRoute(t *testing.T) {
	repo := configuredGrokRepo(t)
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
	for _, event := range hooks.GrokRuntimeEvents() {
		inspection.Hooks = append(inspection.Hooks, inspectedHook{
			Target: event,
			Source: source{Type: "project", Path: filepath.Join(repo, ".grok", "hooks")},
		})
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

func TestPreflightParsesInspectStdoutIndependentlyFromStderr(t *testing.T) {
	repo := configuredGrokRepo(t)
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
	for _, event := range hooks.GrokRuntimeEvents() {
		inspection.Hooks = append(inspection.Hooks, inspectedHook{
			Target: "tools/reconc/bin/hook " + event + " .",
			Source: source{Type: "project", Path: filepath.Join(repo, ".grok", "hooks")},
		})
	}
	body, err := json.Marshal(inspection)
	if err != nil {
		t.Fatal(err)
	}
	command := func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "printf '%s' \"$GROK_INSPECT\"; printf 'diagnostic\\n' >&2")
	}
	t.Setenv("GROK_INSPECT", string(body))
	if err := preflight(context.Background(), repo, "grok", command); err != nil {
		t.Fatalf("successful JSON stdout must not be corrupted by stderr diagnostics: %v", err)
	}
}

func TestPreflightRejectsPrefixCollisionForMissingNativeRoute(t *testing.T) {
	repo := configuredGrokRepo(t)
	hooksList := make([]map[string]interface{}, 0, len(hooks.GrokRuntimeEvents())-1)
	for _, event := range hooks.GrokRuntimeEvents() {
		if event == "grok-stop" {
			continue
		}
		hooksList = append(hooksList, map[string]interface{}{
			"target": "tools/reconc/bin/hook " + event + " .",
			"source": map[string]string{
				"type": "project",
				"path": filepath.Join(repo, ".grok", "hooks"),
			},
		})
	}
	body, err := json.Marshal(map[string]interface{}{
		"projectTrusted": true,
		"hooks":          hooksList,
	})
	if err != nil {
		t.Fatal(err)
	}
	command := func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "printf '%s' \"$GROK_INSPECT\"")
	}
	t.Setenv("GROK_INSPECT", string(body))
	err = preflight(context.Background(), repo, "grok", command)
	if err == nil || !strings.Contains(err.Error(), "grok-stop") {
		t.Fatalf("grok-stop-failure must not satisfy missing grok-stop route: %v", err)
	}
}

func TestPreflightRejectsCompatibleRouteFromNonProjectSource(t *testing.T) {
	repo := configuredGrokRepo(t)
	hooksList := make([]map[string]interface{}, 0, len(hooks.GrokRuntimeEvents()))
	for _, event := range hooks.GrokRuntimeEvents() {
		hooksList = append(hooksList, map[string]interface{}{
			"target": event,
			"source": map[string]string{
				"type": "user",
				"path": filepath.Join(repo, ".grok", "hooks"),
			},
		})
	}
	body, err := json.Marshal(map[string]interface{}{
		"projectTrusted": true,
		"hooks":          hooksList,
	})
	if err != nil {
		t.Fatal(err)
	}
	command := func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "printf '%s' \"$GROK_INSPECT\"")
	}
	t.Setenv("GROK_INSPECT", string(body))
	err = preflight(context.Background(), repo, "grok", command)
	if err == nil || !strings.Contains(err.Error(), "did not load native Reconc routes") {
		t.Fatalf("non-project hook source must fail preflight, got %v", err)
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
	  [ -z "${GROK_PROMPT_LOG:-}" ] || printf '%s\n' "$count" >> "$GROK_PROMPT_LOG"
      printf '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"grok-test","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"first turn"}}}}\n'
      printf '{"jsonrpc":"2.0","id":4,"result":{"stopReason":"end_turn"}}\n'
      ;;
    5)
	  [ -z "${GROK_PROMPT_LOG:-}" ] || printf '%s\n' "$count" >> "$GROK_PROMPT_LOG"
      printf '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"grok-test","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"second turn"}}}}\n'
      printf '{"jsonrpc":"2.0","id":5,"result":{"stopReason":"end_turn"}}\n'
      ;;
    6)
	  [ -z "${GROK_PROMPT_LOG:-}" ] || printf '%s\n' "$count" >> "$GROK_PROMPT_LOG"
	  printf '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"grok-test","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"third turn"}}}}\n'
	  printf '{"jsonrpc":"2.0","id":6,"result":{"stopReason":"end_turn"}}\n'
	  ;;
  esac
done
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
