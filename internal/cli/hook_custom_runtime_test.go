package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/customruntime"
	"reconc.dev/reconc/internal/hooks"
)

func TestCustomRuntimeFixturesBridgePolicyEndToEnd(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	for _, fixture := range []struct {
		name, startEvent, toolEvent, startPayload, toolPayload string
	}{
		{name: "local-agent", startEvent: "session-start", toolEvent: "before-tool", startPayload: `{"context":{"session":"local-session"}}`, toolPayload: `{"context":{"session":"local-session"},"tool":{"name":"Write","input":{"file_path":"gen/out.txt","content":"x"}}}`},
		{name: "ci-bot", startEvent: "run.started", toolEvent: "job.requested", startPayload: `{"run":{"id":"ci-session"}}`, toolPayload: `{"run":{"id":"ci-session"},"job":{"name":"Write","input":{"file_path":"gen/out.txt","content":"x"}}}`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			repo := makeCustomRuntimeBridgeRepo(t, fixture.name)
			var stdout, stderr bytes.Buffer
			if err := runHookBridge([]string{fixture.name, fixture.startEvent, repo}, strings.NewReader(fixture.startPayload), &stdout, &stderr); err != nil {
				t.Fatalf("session start: %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
			}
			stdout.Reset()
			stderr.Reset()
			err := runHookBridge([]string{fixture.name, fixture.toolEvent, repo}, strings.NewReader(fixture.toolPayload), &stdout, &stderr)
			var cliErr *CLIError
			if !errors.As(err, &cliErr) || cliErr.ExitCode != 2 {
				t.Fatalf("pre-tool error = %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
			}
			var response customruntime.NeutralResponse
			if decodeErr := json.Unmarshal(stdout.Bytes(), &response); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if response.Decision != customruntime.DecisionBlock || response.Runtime != "custom:"+fixture.name {
				t.Fatalf("unexpected response: %+v", response)
			}
			var statusOut bytes.Buffer
			if err := runHookStatus([]string{repo, "--json"}, &statusOut); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(statusOut.String(), `"kind": "custom:`+fixture.name+`"`) || !strings.Contains(statusOut.String(), `"live": true`) {
				t.Fatalf("custom status missing liveness: %s", statusOut.String())
			}
		})
	}
}

func TestHookConformPublishedFixtures(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"local-agent", "ci-bot"} {
		var output bytes.Buffer
		base := filepath.Join("..", "customruntime", "testdata")
		if err := runHookConform([]string{filepath.Join(base, name+".json"), filepath.Join(base, name+"-conformance.json"), "--json"}, &output); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(output.String(), `"passed": true`) || !strings.Contains(output.String(), `"case_count": 3`) {
			t.Fatalf("unexpected %s report: %s", name, output.String())
		}
	}
}

func TestCustomRuntimeBridgeReportsDegradedGuaranteesWithoutExecution(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := makeCustomRuntimeBridgeRepo(t, "local-agent")
	manifestPath := filepath.Join(repo, ".reconc", "runtimes", "local-agent.json")
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	body = bytes.Replace(body, []byte(`"pre_execution": true`), []byte(`"pre_execution": false`), 1)
	if err := os.WriteFile(manifestPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runHookBridge([]string{"local-agent", "before-tool", repo}, strings.NewReader(`{"not":"read"}`), &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"decision":"unsupported"`) {
		t.Fatalf("unexpected degraded response: %s", output.String())
	}
}

func TestCustomRuntimeStatusKeepsManifestFreshnessIndependent(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := makeCustomRuntimeBridgeRepo(t, "local-agent")
	directory := filepath.Join(repo, ".reconc", "runtimes")
	ciBody, err := os.ReadFile(filepath.Join("..", "customruntime", "testdata", "ci-bot.json"))
	if err != nil {
		t.Fatal(err)
	}
	ciPath := filepath.Join(directory, "ci-bot.json")
	if err := os.WriteFile(ciPath, ciBody, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(repo, ".reconc", "policy.lock.json")
	lockBody, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	var lock map[string]interface{}
	if err := json.Unmarshal(lockBody, &lock); err != nil {
		t.Fatal(err)
	}
	summaries, ok := lock["custom_runtimes"].([]interface{})
	if !ok {
		t.Fatalf("custom_runtimes = %T", lock["custom_runtimes"])
	}
	for _, value := range summaries {
		summary, ok := value.(map[string]interface{})
		if ok && summary["runtime"] == "custom:ci-bot" {
			summary["manifest_digest"] = "sha256:" + strings.Repeat("a", 64)
		}
	}
	delete(lock, "lock_digest")
	lockDigest, err := compiler.ComputeLockDigest(lock)
	if err != nil {
		t.Fatal(err)
	}
	lock["lock_digest"] = lockDigest
	lockBody, err = json.MarshalIndent(lock, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, append(lockBody, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	statuses, err := inspectCustomRuntimeStatuses(repo)
	if err != nil {
		t.Fatal(err)
	}
	byKind := make(map[string]hooks.PlatformStatus, len(statuses))
	for _, status := range statuses {
		byKind[status.Kind] = status
	}
	if status := byKind["custom:ci-bot"]; status.State != hooks.StateDegraded || status.Configured {
		t.Fatalf("drifted CI runtime status = %+v", status)
	}
	if status := byKind["custom:local-agent"]; status.State != hooks.StateConfigured || !status.Configured {
		t.Fatalf("fresh local runtime inherited CI drift = %+v", status)
	}
}

func makeCustomRuntimeBridgeRepo(t *testing.T, fixture string) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "policies"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte("default_mode: warn\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := "rules:\n  - id: deny-generated\n    kind: deny_write\n    paths: [gen/**]\n    mode: block\n    message: generated output is protected\n"
	if err := os.WriteFile(filepath.Join(repo, "policies", "rules.yml"), []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(repo, ".reconc", "runtimes")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join("..", "customruntime", "testdata", fixture+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, fixture+".json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatal(err)
	}
	return repo
}
