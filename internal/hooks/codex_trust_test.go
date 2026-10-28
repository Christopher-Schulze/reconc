package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// codexWrapperCommand is the Codex hook command the generator writes for one
// route; the reference hashes below were written by Codex itself when the
// operator approved these exact definitions under /hooks.
func codexWrapperCommand(route string) string {
	return `repo="."; hook="$repo/tools/reconc/bin/hook"; if [ -x "$hook" ]; then exec "$hook" ` + route +
		` "$repo"; fi; repo="$(git -C "$repo" rev-parse --show-toplevel 2>/dev/null || printf "%s" "$repo")"; RECONC_HOOK_REPO_RESOLVED=1 exec "$repo/tools/reconc/bin/hook" ` +
		route + ` "$repo"`
}

func codexCommandHandler(t *testing.T, command string, timeout int) map[string]json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{"type": "command", "command": command, "timeout": timeout})
	if err != nil {
		t.Fatal(err)
	}
	var handler map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &handler); err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestCodexHookDefinitionHashMatchesCodexApprovals(t *testing.T) {
	for _, tc := range []struct {
		event, route, want string
		timeout            int
	}{
		{"Stop", "codex-stop", "sha256:661fa8c61839fe3e04eb9e1ecfc4f9bf28efc6044d9744971d98542b6ae9b407", 30},
		{"UserPromptSubmit", "codex-user-prompt-submit", "sha256:cbf24dcf3c3633c694c0ecd077176d2b40140b04c84109d58bb6b10259470e15", 5},
		{"PreCompact", "codex-pre-compaction", "sha256:8e781f50cfea12fa03d766ef19926940842dfd7fdc062a6c8a4b83032917cfe6", 5},
		{"PostCompact", "codex-post-compaction", "sha256:4ebd722fbb805167c0a809ff469d1d2648448c8db05666075d7ded8c1de82e57", 5},
		{"SubagentStart", "codex-subagent-start", "sha256:670f3793841b04d4d04a31821bd0ccc88bceba8518ff251259dcf175424f1e08", 5},
	} {
		got, ok, err := codexHookDefinitionHash(tc.event, nil, codexCommandHandler(t, codexWrapperCommand(tc.route), tc.timeout))
		if err != nil || !ok || got != tc.want {
			t.Fatalf("%s hash = %q, %v, %v; want the Codex approval %q", tc.event, got, ok, err, tc.want)
		}
	}
	matcher := "startup|resume"
	withMatcher, _, _ := codexHookDefinitionHash("SessionStart", &matcher, codexCommandHandler(t, "true", 5))
	withoutMatcher, _, _ := codexHookDefinitionHash("SessionStart", nil, codexCommandHandler(t, "true", 5))
	ignored, _, _ := codexHookDefinitionHash("Stop", &matcher, codexCommandHandler(t, "true", 5))
	plainStop, _, _ := codexHookDefinitionHash("Stop", nil, codexCommandHandler(t, "true", 5))
	if withMatcher == withoutMatcher || ignored != plainStop {
		t.Fatal("the matcher must count for SessionStart and be ignored for Stop, as in Codex")
	}
	if _, ok, _ := codexHookDefinitionHash("Stop", nil, map[string]json.RawMessage{"type": json.RawMessage(`"prompt"`)}); ok {
		t.Fatal("a non-command handler was hashed")
	}
}

// writeCodexApprovals installs the Codex artifact in repo and writes a Codex
// config under a private CODEX_HOME that trusts the project and approves every
// generated hook, letting edit change the approvals first.
func writeCodexApprovals(t *testing.T, repo, trustLevel string, edit func(map[string]map[string]any)) {
	t.Helper()
	root, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(CodexHooksPath)))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Hooks map[string][]codexHookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(root, filepath.FromSlash(CodexHooksPath))
	state := map[string]map[string]any{}
	for event, groups := range document.Hooks {
		for groupIndex, group := range groups {
			for handlerIndex, handler := range group.Hooks {
				hash, ok, err := codexHookDefinitionHash(event, group.Matcher, handler)
				if err != nil {
					t.Fatal(err)
				}
				if ok {
					key := hooksPath + ":" + codexHookEventLabels[event] + ":" + strconv.Itoa(groupIndex) + ":" + strconv.Itoa(handlerIndex)
					state[key] = map[string]any{"trusted_hash": hash}
				}
			}
		}
	}
	if edit != nil {
		edit(state)
	}
	var config strings.Builder
	if trustLevel != "" {
		config.WriteString("[projects." + quoteTOML(root) + "]\ntrust_level = " + quoteTOML(trustLevel) + "\n\n")
	}
	for key, entry := range state {
		config.WriteString("[hooks.state." + quoteTOML(key) + "]\n")
		if hash, ok := entry["trusted_hash"].(string); ok {
			config.WriteString("trusted_hash = " + quoteTOML(hash) + "\n")
		}
		if enabled, ok := entry["enabled"].(bool); ok && !enabled {
			config.WriteString("enabled = false\n")
		}
		config.WriteString("\n")
	}
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func quoteTOML(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func installedCodexRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	writeExecutableWrapper(t, repo)
	if _, err := Install(KindCodex, repo, false); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestCodexStatusReportsHooksCodexWillNotRun(t *testing.T) {
	stale := func(state map[string]map[string]any) {
		for key, entry := range state {
			if strings.HasSuffix(key, ":pre_tool_use:0:0") {
				entry["trusted_hash"] = "sha256:0000"
			}
		}
	}
	missing := func(state map[string]map[string]any) {
		for key := range state {
			if strings.HasSuffix(key, ":stop:0:0") {
				delete(state, key)
			}
		}
	}
	disabled := func(state map[string]map[string]any) {
		for key, entry := range state {
			if strings.HasSuffix(key, ":post_tool_use:0:0") {
				entry["enabled"] = false
			}
		}
	}
	for _, tc := range []struct {
		name, trust, want string
		state             ActivationState
		edit              func(map[string]map[string]any)
	}{
		// A regenerated definition always starts unapproved, so per-hook
		// findings must keep the repository configured: install and repository
		// sync verification require that state.
		{"stale approval", "trusted", "pre_tool_use:0:0 (approval is stale)", StateConfigured, stale},
		{"missing approval", "trusted", "stop:0:0 (not approved)", StateConfigured, missing},
		{"disabled hook", "trusted", "post_tool_use:0:0 (disabled)", StateConfigured, disabled},
		{"untrusted project", "untrusted", "Codex does not trust this project", StateInstalled, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := installedCodexRepo(t)
			writeCodexApprovals(t, repo, tc.trust, tc.edit)
			status := statusForKind(t, repo, KindCodex)
			if status.State != tc.state || status.Configured != (tc.state == StateConfigured) || !strings.Contains(status.Detail, tc.want) {
				t.Fatalf("status = %s %q, want %s naming %q", status.State, status.Detail, tc.state, tc.want)
			}
			if !strings.Contains(status.Remediation, "/hooks") {
				t.Fatalf("remediation %q does not name /hooks", status.Remediation)
			}
		})
	}
}

func TestCodexStatusConfirmsApprovedHooksAndStaysUnverifiedWithoutEvidence(t *testing.T) {
	repo := installedCodexRepo(t)
	writeCodexApprovals(t, repo, "trusted", nil)
	if status := statusForKind(t, repo, KindCodex); status.State != StateConfigured || !strings.Contains(status.Detail, "every Reconc hook definition is approved") {
		t.Fatalf("approved status = %s %q", status.State, status.Detail)
	}
	unknown := installedCodexRepo(t)
	writeCodexApprovals(t, unknown, "", nil)
	if status := statusForKind(t, unknown, KindCodex); status.State != StateConfigured || !strings.Contains(status.Detail, "hook-definition trust") {
		t.Fatalf("status without a project entry = %s %q", status.State, status.Detail)
	}
}

func TestCodexStatusStaysConfiguredWhenApprovalsCannotBeRead(t *testing.T) {
	repo := installedCodexRepo(t)
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[projects\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A broken user configuration must not fail install or repository sync
	// verification, which require the configured state; it stays visible.
	status := statusForKind(t, repo, KindCodex)
	if status.State != StateConfigured || !strings.Contains(status.Detail, "Codex hook approvals cannot be inspected") || !strings.Contains(status.Remediation, "config.toml") {
		t.Fatalf("status with a broken Codex configuration = %s %q, remediation %q", status.State, status.Detail, status.Remediation)
	}
}
