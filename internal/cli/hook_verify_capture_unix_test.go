//go:build !windows

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/hooks"
)

func TestLiveHookCaptureRunsRealRuntimeAndPreservesControlResponse(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	t.Setenv(hookVerificationChildEnv, "1")
	t.Setenv(hookVerificationRepoEnv, repo)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := newHookVerificationChildCommand(context.Background(), executable, "hook", "runtime")
	words := make([]string, len(command.Args))
	for index, argument := range command.Args {
		words[index] = task499ShellQuote(argument)
	}
	// The test executable dispatches the actual CLI and policy evaluator. This
	// launcher only supplies Go's test-entry flags; no host outcome is simulated.
	wrapper := filepath.Join(repo, hooks.WrapperPath+"-verify-real")
	if err := os.MkdirAll(filepath.Dir(wrapper), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec "+strings.Join(words, " ")+" \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runID := strings.Repeat("a", 32)
	for _, test := range []struct {
		name, route, payload string
		code                 int
		policy, decision     string
	}{
		{"codex-deny", "codex-pre-tool-use", `{"session_id":"capture-transport-codex","tool_use_id":"call-codex","tool_name":"apply_patch","tool_input":{"command":"*** Begin Patch\n*** Add File: generated/blocked.txt\n+blocked\n*** End Patch"}}`, 2, "block", "deny"},
		{"cursor-deny", "cursor-pre-tool-use", `{"conversation_id":"capture-transport-cursor","hook_event_name":"preToolUse","tool_name":"Write","tool_input":{"file_path":"generated/blocked.txt"}}`, 0, "block", "deny"},
		{"codex-pass", "codex-pre-tool-use", `{"session_id":"capture-transport-pass","tool_name":"Write","tool_input":{"file_path":"allowed.txt"}}`, 0, "pass", "unproven"},
		{"codex-input-error", "codex-pre-tool-use", `{"tool_name":"Write","tool_input":{"file_path":"generated/blocked.txt"}}`, 2, "unproven", "deny"},
		{"codex-read-without-policy", "codex-pre-tool-use", `{"session_id":"capture-transport-read","tool_name":"Read","tool_input":{"file_path":"AGENTS.md"}}`, 0, "unproven", "unproven"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			execution, err := executeLiveHookCapture(ctx, repo, []string{test.route, repo}, []byte(test.payload))
			if err != nil || execution.ExitCode != test.code || execution.PolicyDecision != test.policy {
				t.Fatalf("direct wrapper: exit=%d policy=%s err=%v", execution.ExitCode, execution.PolicyDecision, err)
			}
			var stdout, stderr bytes.Buffer
			err = runHookVerificationCaptureChild([]string{repo, runID, test.route, repo}, strings.NewReader(test.payload), &stdout, &stderr)
			if ExitCode(err) != test.code || !bytes.Equal(stdout.Bytes(), execution.Body) {
				t.Fatalf("capture altered control response: exit=%d stdout=%s expected=%s stderr=%s err=%v", ExitCode(err), &stdout, execution.Body, &stderr, err)
			}
			records, err := readLiveHookProbeRecords(repo)
			if err != nil || len(records) == 0 {
				t.Fatalf("capture records: %v", err)
			}
			record := records[len(records)-1]
			if record.Binding.PolicyDecision != test.policy || record.Binding.Decision != test.decision || record.DurationNanos <= 0 {
				t.Fatalf("policy and transport decision conflated: %+v", record.Binding)
			}
		})
	}
	records, err := readLiveHookProbeRecords(repo)
	if err != nil || len(records) != 5 {
		t.Fatalf("records=%d err=%v", len(records), err)
	}
	lock, err := os.OpenFile(filepath.Join(repo, ".reconc/policy.lock.json"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.WriteString("\ninvalid-json"); err != nil {
		lock.Close()
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"codex", "cursor"} {
		payload := `{"session_id":"invalid-policy","conversation_id":"invalid-policy","hook_event_name":"preToolUse","tool_name":"Write","tool_input":{"file_path":"generated/blocked.txt"}}`
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		execution, err := executeLiveHookCapture(ctx, repo, []string{kind + "-pre-tool-use", repo}, []byte(payload))
		cancel()
		decision, _, _ := classifyLiveHookResponse(kind+"-pre-tool-use", execution.ExitCode, execution.Body)
		if err != nil || decision != "deny" || execution.PolicyDecision != "unproven" {
			t.Fatalf("%s policy error became policy proof: %+v err=%v", kind, execution, err)
		}
	}
}

func TestLiveHookCaptureRejectsSymlinkWithoutWritingTarget(t *testing.T) {
	record, _ := liveHookBoundCaptureFixture(t)
	for _, parent := range []bool{false, true} {
		repo, outside := t.TempDir(), t.TempDir()
		target := filepath.Join(outside, "hook-verify-events.jsonl")
		if err := os.WriteFile(target, []byte("preserve\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if parent {
			if err := os.Symlink(outside, filepath.Join(repo, ".reconc")); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.Mkdir(filepath.Join(repo, ".reconc"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(repo, ".reconc/hook-verify-events.jsonl")); err != nil {
				t.Fatal(err)
			}
		}
		if err := appendLiveHookCapture(repo, record); err == nil {
			t.Fatal("capture followed a symlink")
		}
		body, err := os.ReadFile(target)
		if err != nil || string(body) != "preserve\n" {
			t.Fatalf("external file changed: %q %v", body, err)
		}
	}
}
