package agentsession

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/runtime"
)

func stopPayload(t *testing.T, sessionID string, fields map[string]interface{}) []byte {
	t.Helper()
	body := map[string]interface{}{"session_id": sessionID, "hook_event_name": "Stop"}
	for key, value := range fields {
		body[key] = value
	}
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func unstableStopRepo(t *testing.T, sessionID string) string {
	t.Helper()
	if goruntime.GOOS == "windows" {
		t.Skip("POSIX shell fixture")
	}
	repo := setupStopBenchmarkRepo(t)
	script := filepath.Join(repo, ".reconc", "scripts", "mutate.sh")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf x >> mutation.txt\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	policy := "rules:\n" +
		"  - id: unstable-script\n" +
		"    kind: require_script\n" +
		"    when_paths: ['src/**']\n" +
		"    script: .reconc/scripts/mutate.sh\n" +
		"    cache_inputs: [mutation.txt]\n" +
		"    mode: block\n" +
		"    message: unstable\n"
	if err := os.WriteFile(filepath.Join(repo, "policies", "rules.yml"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatal(err)
	}
	stopGenerationGit(t, repo, "add", "-A")
	stopGenerationGit(t, repo, "commit", "-m", "unstable policy fixture", "--quiet")
	if err := os.WriteFile(filepath.Join(repo, "src", "a.go"), []byte("package fixture\n// dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := InitializeSessionState(repo, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveSessionState(RecordWriteEvent(state, []string{"src/a.go"})); err != nil {
		t.Fatal(err)
	}
	return repo
}

func invalidPolicyRepo(t *testing.T, sessionID string) string {
	t.Helper()
	repo := setupStopBenchmarkRepo(t)
	if _, err := InitializeSessionState(repo, sessionID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte("Claim route: AGENTS.md: broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func uncertified(t *testing.T, repo, sessionID string) bool {
	t.Helper()
	state, err := LoadSessionState(repo, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return state.UncertifiedTermination
}

func TestStopWithMovingInputsHoldsOnceThenEndsUncertified(t *testing.T) {
	repo := unstableStopRepo(t, "moving")

	first := RunStop(repo, stopPayload(t, "moving", nil))
	if first.ExitCode != 2 || !strings.Contains(first.Stderr, "stop inputs changed") {
		t.Fatalf("first Stop must hold and name the moving inputs: %+v", first)
	}
	if uncertified(t, repo, "moving") {
		t.Fatal("holding the Stop must not mark the session uncertified")
	}

	second := RunStop(repo, stopPayload(t, "moving", map[string]interface{}{"stop_hook_active": true}))
	if second.ExitCode != 0 || !strings.Contains(second.Stderr, "uncertified") {
		t.Fatalf("a Stop the agent cannot settle must end instead of looping: %+v", second)
	}
	if !uncertified(t, repo, "moving") {
		t.Fatal("releasing an unverified Stop must record that it is uncertified")
	}
}

func TestStopWithMovingInputsReleasesHostsWithoutStopHookActiveOnRepeat(t *testing.T) {
	repo := unstableStopRepo(t, "repeat")
	if first := RunStop(repo, stopPayload(t, "repeat", nil)); first.ExitCode != 2 {
		t.Fatalf("first Stop must hold: %+v", first)
	}
	if second := RunStop(repo, stopPayload(t, "repeat", nil)); second.ExitCode != 0 {
		t.Fatalf("the repeated Stop must release a host that never sets stop_hook_active: %+v", second)
	}
}

func TestStopWithMovingInputsWaitsForPendingBackgroundWork(t *testing.T) {
	repo := unstableStopRepo(t, "background")
	result := RunStop(repo, stopPayload(t, "background", map[string]interface{}{
		"background_tasks": []interface{}{map[string]interface{}{"id": "agent-1"}},
	}))
	if result.ExitCode != 0 || !strings.Contains(result.Stderr, "1 background") {
		t.Fatalf("a paused session must not loop while its own background work runs: %+v", result)
	}
	if uncertified(t, repo, "background") {
		t.Fatal("a paused session verifies at the Stop after its work settles and is not uncertified")
	}
}

func TestStrictContinuationNeverReleasesAnUnverifiableStop(t *testing.T) {
	repo := invalidPolicyRepo(t, "strict")
	payload, err := ParsePayload(stopPayload(t, "strict", nil))
	if err != nil {
		t.Fatal(err)
	}
	payload.StrictContinuation = true
	for attempt := 0; attempt < 2; attempt++ {
		result := unverifiableStopResult(repo, payload, "test", "held")
		if result.ExitCode != 2 {
			t.Fatalf("attempt %d: strict continuation released: %+v", attempt, result)
		}
	}
	if uncertified(t, repo, "strict") {
		t.Fatal("a held strict Stop must not be marked uncertified")
	}
}

func TestStopWithInvalidPolicyNamesTheRepairAndDoesNotLoop(t *testing.T) {
	repo := invalidPolicyRepo(t, "invalid")

	first := RunStop(repo, stopPayload(t, "invalid", nil))
	if first.ExitCode != 2 {
		t.Fatalf("an unenforceable policy must hold the first Stop: %+v", first)
	}
	for _, want := range []string{".reconc.yml", "operator", "reconc refresh ."} {
		if !strings.Contains(first.Stderr, want) {
			t.Fatalf("message lacks %q: %s", want, first.Stderr)
		}
	}

	second := RunStop(repo, stopPayload(t, "invalid", map[string]interface{}{"stop_hook_active": true}))
	if second.ExitCode != 0 || !strings.Contains(second.Stderr, "uncertified") {
		t.Fatalf("an agent that cannot repair the policy must be able to stop: %+v", second)
	}
	if !uncertified(t, repo, "invalid") {
		t.Fatal("the released Stop must be recorded as uncertified")
	}
}

func TestPreToolUseWithInvalidPolicyStaysClosedAndNamesTheRepair(t *testing.T) {
	repo := invalidPolicyRepo(t, "pre-invalid")
	for name, payload := range map[string][]byte{
		"write":   preToolUsePayload(t, "Write", map[string]interface{}{"file_path": ".reconc.yml", "content": "rules: []\n"}),
		"command": preToolUsePayload(t, "Bash", map[string]interface{}{"command": "git status"}),
	} {
		result := RunPreToolUse(repo, payload)
		if result.ExitCode != 2 {
			t.Fatalf("%s: an unreadable policy must never authorize work: %+v", name, result)
		}
		if !strings.Contains(result.Stderr, "operator") || !strings.Contains(result.Stderr, "reconc refresh .") {
			t.Fatalf("%s: message names no reachable repair: %s", name, result.Stderr)
		}
	}
}

func TestStopFeedbackStaysBoundedWhateverTheViolationsSay(t *testing.T) {
	long := strings.Repeat("output line ", 4000)
	var violations []runtime.Violation
	for index := 0; index < 40; index++ {
		violations = append(violations, runtime.Violation{
			RuleID:            fmt.Sprintf("rule-%02d", index),
			RecommendedAction: "first line of the action " + long + "\nsecond line that must not reach the agent",
		})
	}
	for name, repeated := range map[string]bool{"first": false, "repeated": true} {
		reason := stopReasonForViolations(violations, "/tmp/report.json", "RB-123456789abc", repeated, "Repository run: enabled")
		if len(reason) > stopFeedbackMaxBytes {
			t.Fatalf("%s: feedback has %d bytes, limit %d", name, len(reason), stopFeedbackMaxBytes)
		}
		if strings.Contains(reason, "second line") {
			t.Fatalf("%s: feedback keeps more than the first line of an action", name)
		}
		for _, want := range []string{"Report: /tmp/report.json", "Feedback: RB-123456789abc", "Repository run: enabled"} {
			if !strings.Contains(reason, want) {
				t.Fatalf("%s: bounding lost %q: %s", name, want, reason)
			}
		}
	}
	first := stopReasonForViolations(violations, "", "", false, "")
	if !strings.Contains(first, "37 more violation(s)") {
		t.Fatalf("feedback must count what it omits: %s", first)
	}
	repeated := stopReasonForViolations(violations, "", "", true, "")
	if !strings.Contains(repeated, "rule-00") || !strings.Contains(repeated, "+37 more") || strings.Contains(repeated, "rule-39") {
		t.Fatalf("repeated feedback must name a bounded rule list: %s", repeated)
	}
}

func TestStopFeedbackKeepsShortActionsIntact(t *testing.T) {
	reason := stopReasonForViolations([]runtime.Violation{{RuleID: "gate-one", RecommendedAction: "run the exact gate"}}, "", "", false, "")
	if !strings.Contains(reason, "[gate-one] run the exact gate") {
		t.Fatalf("a short action must reach the agent unchanged: %s", reason)
	}
}

func TestCleanStopClearsAnEarlierUncertifiedRelease(t *testing.T) {
	repo := setupStopBenchmarkRepo(t)
	policyPath := filepath.Join(repo, ".reconc.yml")
	original, readErr := os.ReadFile(policyPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	if _, err := InitializeSessionState(repo, "recovers"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policyPath, []byte("Claim route: AGENTS.md: broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if released := RunStop(repo, stopPayload(t, "recovers", map[string]interface{}{"stop_hook_active": true})); released.ExitCode != 0 {
		t.Fatalf("the unverifiable Stop must release: %+v", released)
	}
	if !uncertified(t, repo, "recovers") {
		t.Fatal("the release must be recorded as uncertified")
	}
	if readErr == nil {
		if err := os.WriteFile(policyPath, original, 0o644); err != nil {
			t.Fatal(err)
		}
	} else if err := os.Remove(policyPath); err != nil {
		t.Fatal(err)
	}
	clean := RunStop(repo, stopPayload(t, "recovers", nil))
	if clean.ExitCode != 0 || clean.Stdout != "" {
		t.Fatalf("the repaired session must stop cleanly: %+v", clean)
	}
	if uncertified(t, repo, "recovers") {
		t.Fatal("a clean Stop must clear the earlier uncertified mark")
	}
}

func TestCodexSubagentWithMovingInputsEndsUncertifiedOnItsRepeatedTurn(t *testing.T) {
	repo := unstableStopRepo(t, "child")
	root, err := ResolveRepoRootRef(repo)
	if err != nil {
		t.Fatal(err)
	}
	run := func(stopHookActive bool) Result {
		body, err := NormalizeCodexPayload("codex-subagent-stop", []byte(fmt.Sprintf(
			`{"session_id":"child","hook_event_name":"SubagentStop","agent_id":"child","stop_hook_active":%t}`, stopHookActive)))
		if err != nil {
			t.Fatal(err)
		}
		return RunHookRequest(root, HookHandlerCodexSubagentStop, "codex-subagent-stop", body)
	}
	if first := run(false); first.ExitCode != 2 {
		t.Fatalf("the first child turn must hold: %+v", first)
	}
	second := run(true)
	if second.ExitCode != 0 {
		t.Fatalf("the repeated child turn must not loop on moving inputs: %+v", second)
	}
}
