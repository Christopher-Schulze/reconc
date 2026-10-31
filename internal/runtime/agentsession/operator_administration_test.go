package agentsession

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestOperatorOnlyCommandsAreRefusedForAgentToolCalls(t *testing.T) {
	cases := []struct {
		command string
		refused bool
	}{
		{"reconc hook pause . --for 30m --reason incident", true},
		{"reconc hook resume .", true},
		{"/usr/local/bin/reconc hook evidence-resolve . --token abc --reason review", true},
		{"reconc hook claim . operator:agents-md-edit-authorized", true},
		{"reconc hook claim . operator:x --json", true},
		{"reconc hook claim . operator:x --for 3h", true},
		{"sh -c 'reconc hook claim . operator:x --for 8h --session s1'", true},
		{"reconc-1.2.3-darwin-arm64 hook pause . --for 1h --reason x", true},
		{"sh -c 'reconc hook pause . --for 5m --reason nested'", true},
		{"git status && reconc hook resume .", true},
		{`reconc hook claim . "$CLAIM"`, true},
		{"reconc hook claim . ci-green", false},
		{"reconc hook claim . operator:agents-md-edit-authorized --revoke", false},
		{`reconc hook claim . "$CLAIM" --revoke`, false},
		{"sh -c 'reconc hook claim . operator:x --revoke --json'", false},
		{`reconc hook claim . operator:x "$FLAG"`, true},
		{"reconc hook claim . operator:x --revoked", true},
		{"reconc hook status .", false},
		{"reconc hook pause-status . --json", false},
		{"echo reconc hook pause . --for 1m --reason x", false},
		{"reconc status .", false},
	}
	for _, testCase := range cases {
		reason := operatorOnlyCommandReason(testCase.command)
		if testCase.refused && !strings.Contains(reason, "operator-only") {
			t.Errorf("%q was not refused: %q", testCase.command, reason)
		}
		if !testCase.refused && reason != "" {
			t.Errorf("%q was refused: %q", testCase.command, reason)
		}
	}
}

func TestPreToolUseRefusesOperatorClaimWithoutTouchingState(t *testing.T) {
	repo := setupPolicyRepo(t)
	result := RunPreToolUse(repo, []byte(`{"session_id":"guard","tool_name":"Bash","tool_input":{"command":"reconc hook claim . operator:agents-md-edit-authorized"}}`))
	if result.ExitCode != 2 || !strings.Contains(result.Stderr, "operator-only") {
		t.Fatalf("operator claim reached execution: %+v", result)
	}
	codex := RunPreToolUse(repo, []byte(`{"session_id":"guard","tool_name":"exec_command","tool_input":{"cmd":"reconc hook pause . --for 1h --reason x"}}`))
	if codex.ExitCode != 2 || !strings.Contains(codex.Stderr, "operator-only") {
		t.Fatalf("Codex exec_command pause reached execution: %+v", codex)
	}
}

func TestOperatorPauseLetsGatesPassUntilResume(t *testing.T) {
	repo := setupPolicyRepo(t)
	root, err := ResolveRepoRootRef(repo)
	if err != nil {
		t.Fatal(err)
	}
	write := []byte(`{"session_id":"pause","tool_name":"Write","tool_input":{"file_path":"generated/out.txt"}}`)
	if result := RunHookRequest(root, HookHandlerPreToolUse, "claude-code", write); result.ExitCode != 2 {
		t.Fatalf("denied write was not blocked before the pause: %+v", result)
	}
	if _, err := PauseHooks(repo, 30*time.Minute, "incident triage", time.Now()); err != nil {
		t.Fatal(err)
	}
	paused := RunHookRequest(root, HookHandlerPreToolUse, "claude-code", write)
	if paused.ExitCode != 0 || !strings.Contains(paused.Stderr, "paused by the operator") {
		t.Fatalf("pause did not override the cached block: %+v", paused)
	}
	resume := RunHookRequest(root, HookHandlerPreToolUse, "claude-code",
		[]byte(`{"session_id":"pause","tool_name":"Bash","tool_input":{"command":"reconc hook resume ."}}`))
	if resume.ExitCode != 2 || !strings.Contains(resume.Stderr, "operator-only") {
		t.Fatalf("operator guard did not hold during the pause: %+v", resume)
	}
	if stop := RunStop(repo, []byte(`{"session_id":"pause"}`)); stop.ExitCode != 0 {
		t.Fatalf("stop gate blocked during the pause: %+v", stop)
	}
	status, err := ReadHookPauseStatus(repo, time.Now())
	if err != nil || !status.Active || status.Reason != "incident triage" {
		t.Fatalf("pause status = %+v err=%v", status, err)
	}
	if _, err := ResumeHooks(repo, time.Now()); err != nil {
		t.Fatal(err)
	}
	if result := RunHookRequest(root, HookHandlerPreToolUse, "claude-code", write); result.ExitCode != 2 {
		t.Fatalf("enforcement did not return after resume: %+v", result)
	}
	if _, err := ResumeHooks(repo, time.Now()); err == nil {
		t.Fatal("resume without an active pause succeeded")
	}
}

func TestOperatorPauseOverridesCachedPolicyBlockButKeepsDestructiveGuard(t *testing.T) {
	fixture := declaredCommandFixture(t, `rules:
  - id: no-publish
    kind: forbid_command
    commands: ['npm publish']
    mode: block
    message: never publish from an agent session
`)
	root, err := ResolveRepoRootRef(fixture.repo)
	if err != nil {
		t.Fatal(err)
	}
	publish := nativeCommandPayload(t, fixture, "publish-call", "npm publish", nil)
	if result := RunHookRequest(root, HookHandlerPreToolUse, "kimi-pre-tool-use", publish); result.ExitCode != 2 {
		t.Fatalf("forbidden command was not blocked: %+v", result)
	}
	if _, err := os.Stat(preDecisionCachePath(root.Path(), publish)); err != nil {
		t.Fatalf("block decision was not cached, so the test cannot prove pause precedence: %v", err)
	}
	if _, err := PauseHooks(fixture.repo, time.Hour, "release hotfix", time.Now()); err != nil {
		t.Fatal(err)
	}
	if result := RunHookRequest(root, HookHandlerPreToolUse, "kimi-pre-tool-use", publish); result.ExitCode != 0 {
		t.Fatalf("cached block outlived the pause: %+v", result)
	}
	reset := nativeCommandPayload(t, fixture, "reset-call", "git reset --hard HEAD", nil)
	if result := RunHookRequest(root, HookHandlerPreToolUse, "kimi-pre-tool-use", reset); result.ExitCode != 2 {
		t.Fatalf("destructive git guard was lifted by the pause: %+v", result)
	}
	if _, err := ResumeHooks(fixture.repo, time.Now()); err != nil {
		t.Fatal(err)
	}
	if result := RunHookRequest(root, HookHandlerPreToolUse, "kimi-pre-tool-use", publish); result.ExitCode != 2 {
		t.Fatalf("policy block did not return after resume: %+v", result)
	}
}

func TestOperatorPauseIsBoundedAndExpires(t *testing.T) {
	repo := setupPolicyRepo(t)
	if _, err := PauseHooks(repo, MaxHookPause+time.Minute, "too long", time.Now()); err == nil {
		t.Fatal("pause above the maximum was accepted")
	}
	if _, err := PauseHooks(repo, 30*time.Second, "too short", time.Now()); err == nil {
		t.Fatal("pause below the minimum was accepted")
	}
	if _, err := PauseHooks(repo, time.Hour, " ", time.Now()); err == nil {
		t.Fatal("pause without a reason was accepted")
	}
	started := time.Now().Add(-2 * time.Hour)
	if _, err := PauseHooks(repo, time.Hour, "expired window", started); err != nil {
		t.Fatal(err)
	}
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, active := activeHookPause(root, time.Now()); active {
		t.Fatal("expired pause is still active")
	}
	result := RunPreToolUse(repo, []byte(`{"session_id":"expired","tool_name":"Write","tool_input":{"file_path":"generated/out.txt"}}`))
	if result.ExitCode != 2 {
		t.Fatalf("expired pause let a denied write pass: %+v", result)
	}
}

func TestDamagedPauseRecordKeepsEnforcementOn(t *testing.T) {
	repo := setupPolicyRepo(t)
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateStateDir(filepath.Dir(hookPausePath(root))); err != nil {
		t.Fatal(err)
	}
	forged, err := json.Marshal(hookPauseFile{FormatVersion: hookPauseFormatVersion, RepoRoot: "/elsewhere", History: []hookPauseRecord{{
		Reason: "forged", PausedAt: time.Now().UTC().Format(time.RFC3339Nano), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range [][]byte{[]byte("{not json"), forged} {
		if err := os.WriteFile(hookPausePath(root), body, 0o600); err != nil {
			t.Fatal(err)
		}
		result := RunPreToolUse(repo, []byte(`{"session_id":"damaged","tool_name":"Write","tool_input":{"file_path":"generated/out.txt"}}`))
		if result.ExitCode != 2 {
			t.Fatalf("damaged pause record lifted enforcement: %+v", result)
		}
	}
	if _, err := PauseHooks(repo, time.Hour, "operator replaces the damaged record", time.Now()); err != nil {
		t.Fatalf("a damaged record kept the operator valve shut: %v", err)
	}
	if status, err := ReadHookPauseStatus(repo, time.Now()); err != nil || !status.Active {
		t.Fatalf("replacement pause is not active: %+v err=%v", status, err)
	}
}

func TestSelfAdministrationDiagnosticsRunUnderTaint(t *testing.T) {
	repo := setupPolicyRepo(t)
	if _, err := InitializeSessionState(repo, "sealed"); err != nil {
		t.Fatal(err)
	}
	if _, err := MutateSessionState(repo, "sealed", func(state SessionState) SessionState {
		markEvidenceOverflowWithLimit(&state, "claims", "byte_budget")
		return state
	}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		command  string
		admitted bool
	}{
		{"reconc hook evidence-status .", true},
		{"reconc status . --json", true},
		{"reconc doctor . --deep", true},
		{"reconc next . --write src/a.go --json", true},
		{"reconc agent-intro --section claims", true},
		{"reconc hook pause-status .", true},
		{"reconc hook evidence-status . --json 2>&1", true},
		{"reconc status . --output report.txt", false},
		{"reconc hook evidence-status . > status.txt", false},
		{"reconc doctor . && rm -rf build", false},
		{"reconc run log . --follow", false},
		{"reconc refresh . && touch x", false},
		{"reconc hook evidence-resolve . --token t --reason r", false},
		{"git add -A", false},
	}
	for _, testCase := range cases {
		body, err := json.Marshal(map[string]interface{}{
			"session_id": "sealed", "tool_name": "Bash", "tool_input": map[string]string{"command": testCase.command},
		})
		if err != nil {
			t.Fatal(err)
		}
		result := RunPreToolUse(repo, body)
		if testCase.admitted && result.ExitCode != 0 {
			t.Errorf("%q was blocked under taint: %+v", testCase.command, result)
		}
		if !testCase.admitted && result.ExitCode == 0 {
			t.Errorf("%q passed the taint gate", testCase.command)
		}
	}
}

func TestEvidenceTaintResolvesInsideTheActiveSession(t *testing.T) {
	repo := setupPolicyRepo(t)
	if result := RunSessionStart(repo, []byte(`{"session_id":"live"}`)); result.ExitCode != 0 {
		t.Fatalf("session start: %+v", result)
	}
	if _, err := MutateSessionState(repo, "live", func(state SessionState) SessionState {
		markEvidenceOverflowWithLimit(&state, "claims", "item_bytes")
		return state
	}); err != nil {
		t.Fatal(err)
	}
	blocked := RunPreToolUse(repo, []byte(`{"session_id":"live","tool_name":"Bash","tool_input":{"command":"ls"}}`))
	if blocked.ExitCode != 2 || !strings.Contains(blocked.Stderr, "evidence-resolve") {
		t.Fatalf("taint did not block with a reachable recovery: %+v", blocked)
	}
	status, err := ReadEvidenceTaintStatus(repo)
	if err != nil || !status.Present {
		t.Fatalf("taint status = %+v err=%v", status, err)
	}
	if _, err := ResolveEvidenceTaint(repo, strings.Repeat("0", 64), "review"); err == nil {
		t.Fatal("wrong token resolved the taint")
	}
	if _, err := ResolveEvidenceTaint(repo, status.Token, "operator reviewed the dropped claim"); err != nil {
		t.Fatalf("active-session resolution failed: %v", err)
	}
	state, err := LoadSessionState(repo, "live")
	if err != nil || state.EvidenceOverflow {
		t.Fatalf("active session stayed tainted: overflow=%v err=%v", state.EvidenceOverflow, err)
	}
	if current, err := ReadEvidenceTaintStatus(repo); err != nil || current.Present {
		t.Fatalf("taint remained after resolution: %+v err=%v", current, err)
	}
	if result := RunPreToolUse(repo, []byte(`{"session_id":"live","tool_name":"Bash","tool_input":{"command":"ls"}}`)); result.ExitCode != 0 {
		t.Fatalf("session stayed sealed after resolution: %+v", result)
	}
}

func TestLockfileRepairAdmissionRejectsRedirection(t *testing.T) {
	repo := t.TempDir()
	for _, command := range []string{"reconc refresh .", "reconc refresh . 2>&1", "reconc refresh . >/dev/null"} {
		if !isLockfileRepairCommand(repo, command) {
			t.Errorf("repair %q without a file write is no longer admitted", command)
		}
	}
	for _, command := range []string{"reconc refresh . > AGENTS.md", "reconc refresh . 2>notes.txt", "reconc refresh . >> x"} {
		if isLockfileRepairCommand(repo, command) {
			t.Errorf("redirected repair %q was admitted", command)
		}
	}
}

func TestClaudeScratchpadWritesAreNotRepositoryWrites(t *testing.T) {
	repo := setupPolicyRepo(t)
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	owner := "claude-" + strconv.Itoa(os.Getuid())
	scratchpad := filepath.Join(temp, owner, claudeProjectKey(root), "session-1", "scratchpad")
	foreign := filepath.Join(temp, owner, "-some-other-project", "session-1", "scratchpad")
	for _, dir := range []string{scratchpad, foreign, filepath.Join(root, "generated")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "generated"), filepath.Join(scratchpad, "escape")); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path    string
		allowed bool
	}{
		{filepath.Join(scratchpad, "notes.md"), true},
		{filepath.Join(scratchpad, "nested", "plan.json"), true},
		{filepath.Join(scratchpad, "escape", "out.txt"), false},
		{filepath.Join(foreign, "notes.md"), false},
		{filepath.Join(temp, "notes.md"), false},
	}
	for _, testCase := range cases {
		body, err := json.Marshal(map[string]interface{}{
			"session_id": "scratch", "tool_name": "Write", "tool_input": map[string]string{"file_path": testCase.path},
		})
		if err != nil {
			t.Fatal(err)
		}
		result := RunPreToolUse(repo, body)
		if testCase.allowed && result.ExitCode != 0 {
			t.Errorf("scratchpad write %s was blocked: %+v", testCase.path, result)
		}
		if !testCase.allowed && result.ExitCode == 0 {
			t.Errorf("write %s passed as scratchpad", testCase.path)
		}
	}
}
