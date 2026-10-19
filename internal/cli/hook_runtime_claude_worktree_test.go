package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

const claudeWorktreePublishRule = `rules:
  - id: no-publish
    kind: forbid_command
    commands: ['npm publish']
    mode: block
    message: publication is reserved for releases
`

// claudeWorktreeRepo commits an empty root and the governed tree on top of
// it, so a linked worktree can be checked out with or without the policy.
func claudeWorktreeRepo(t *testing.T) (string, func(args ...string)) {
	t.Helper()
	repo := bootstrapE2ERepo(t)
	if err := os.WriteFile(filepath.Join(repo, "policies", "commands.yml"), []byte(claudeWorktreePublishRule), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "e2e"); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("commit", "-q", "--allow-empty", "-m", "empty root")
	git("tag", "ungoverned")
	git("add", "-A", "-f", ".")
	git("commit", "-q", "-m", "governed")
	return repo, git
}

func claudeWorktreeWrite(t *testing.T, repo, session, cwd, target string) (string, int) {
	t.Helper()
	payload := fmt.Sprintf(`{"session_id":%q,"transcript_path":"/tmp/t.jsonl","cwd":%q,"hook_event_name":"PreToolUse","tool_name":"Write","tool_use_id":"w1","tool_input":{"file_path":%q,"content":"x"}}`, session, cwd, target)
	_, stderr, code := runWithStdin(t, payload, "hook", "runtime", "claude-pre-tool-use", repo)
	return stderr, code
}

func claudeWorktreeCommand(t *testing.T, repo, session, cwd, command string) (string, int) {
	t.Helper()
	payload := fmt.Sprintf(`{"session_id":%q,"transcript_path":"/tmp/t.jsonl","cwd":%q,"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"b1","tool_input":{"command":%q}}`, session, cwd, command)
	_, stderr, code := runWithStdin(t, payload, "hook", "runtime", "claude-pre-tool-use", repo)
	return stderr, code
}

func claudeWorktreeRecordWrite(t *testing.T, repo, session, cwd, target string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("package src\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	payload := fmt.Sprintf(`{"session_id":%q,"transcript_path":"/tmp/t.jsonl","cwd":%q,"hook_event_name":"PostToolUse","tool_name":"Write","tool_use_id":"p1","tool_input":{"file_path":%q,"content":"x"},"tool_response":{"success":true}}`, session, cwd, target)
	if _, stderr, code := runWithStdin(t, payload, "hook", "runtime", "claude-post-tool-use", repo); code != 0 {
		t.Fatalf("record write %s: code=%d stderr=%q", target, code, stderr)
	}
}

func claudeWorktreeStop(t *testing.T, repo, session, cwd string) string {
	t.Helper()
	payload := fmt.Sprintf(`{"session_id":%q,"transcript_path":"/tmp/t.jsonl","cwd":%q,"hook_event_name":"Stop","stop_hook_active":false}`, session, cwd)
	stdout, _, _ := runWithStdin(t, payload, "hook", "runtime", "claude-stop", repo)
	return stdout
}

func TestHookRuntimeClaudeWorktreeWithoutPolicyKeepsTheProjectPolicy(t *testing.T) {
	repo, git := claudeWorktreeRepo(t)
	git("worktree", "add", "-q", ".claude/worktrees/bare", "ungoverned")
	worktree := filepath.Join(repo, ".claude", "worktrees", "bare")

	for _, target := range []string{"generated/x.go", "src/ok.go"} {
		stderr, code := claudeWorktreeWrite(t, repo, "bare", worktree, filepath.Join(worktree, target))
		if code != 2 || !strings.Contains(stderr, "no Reconc policy of its own") {
			t.Fatalf("a write to %s in a worktree without policy was not refused: code=%d stderr=%q", target, code, stderr)
		}
	}
	if stderr, code := claudeWorktreeCommand(t, repo, "bare", worktree, "npm publish"); code != 2 || !strings.Contains(stderr, "no-publish") {
		t.Fatalf("a command in a worktree without policy escaped the project policy: code=%d stderr=%q", code, stderr)
	}
}

func TestHookRuntimeClaudeWorktreeMustKeepEveryProjectRule(t *testing.T) {
	repo, git := claudeWorktreeRepo(t)
	git("worktree", "add", "-q", ".claude/worktrees/weaker", "-b", "weaker")
	git("worktree", "add", "-q", ".claude/worktrees/stricter", "-b", "stricter")
	weaker := filepath.Join(repo, ".claude", "worktrees", "weaker")
	stricter := filepath.Join(repo, ".claude", "worktrees", "stricter")

	rules, err := os.ReadFile(filepath.Join(weaker, "policies", "rules.yml"))
	if err != nil {
		t.Fatal(err)
	}
	withoutDeny := strings.Replace(string(rules), "deny_write", "require_claim\n    claims: ['unused']\n    when_paths: ['never/**']", 1)
	withoutDeny = strings.Replace(withoutDeny, "    paths: ['generated/**']\n", "", 1)
	if err := os.WriteFile(filepath.Join(weaker, "policies", "rules.yml"), []byte(withoutDeny), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(weaker, "e2e"); err != nil {
		t.Fatal(err)
	}
	stderr, code := claudeWorktreeWrite(t, repo, "weaker", weaker, filepath.Join(weaker, "generated", "x.go"))
	if code != 2 || !strings.Contains(stderr, "deny-gen") {
		t.Fatalf("a worktree whose policy changed a project rule was not held to it: code=%d stderr=%q", code, stderr)
	}

	extra := "rules:\n  - id: deny-docs\n    kind: deny_write\n    paths: ['docs/**']\n    mode: block\n    message: docs are frozen here\n"
	if err := os.WriteFile(filepath.Join(stricter, "policies", "extra.yml"), []byte(extra), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(stricter, "e2e"); err != nil {
		t.Fatal(err)
	}
	if stderr, code := claudeWorktreeWrite(t, repo, "stricter", stricter, filepath.Join(stricter, "docs", "a.md")); code != 2 || !strings.Contains(stderr, "deny-docs") {
		t.Fatalf("a worktree that adds rules was not evaluated under its own policy: code=%d stderr=%q", code, stderr)
	}
	if stderr, code := claudeWorktreeWrite(t, repo, "stricter", stricter, filepath.Join(stricter, "generated", "x.go")); code != 2 || !strings.Contains(stderr, "deny-gen") {
		t.Fatalf("a worktree that adds rules lost a project rule: code=%d stderr=%q", code, stderr)
	}
	if stderr, code := claudeWorktreeWrite(t, repo, "stricter", stricter, filepath.Join(stricter, "src", "ok.go")); code != 0 {
		t.Fatalf("an allowed write in a stricter worktree was denied: code=%d stderr=%q", code, stderr)
	}
}

func TestHookRuntimeClaudeStopCoversEveryCheckoutTheSessionChanged(t *testing.T) {
	repo, git := claudeWorktreeRepo(t)
	git("worktree", "add", "-q", ".claude/worktrees/w1", "-b", "w1")
	worktree := filepath.Join(repo, ".claude", "worktrees", "w1")

	// A change in the main checkout keeps its Stop gate after a cd into a
	// linked worktree.
	claudeWorktreeRecordWrite(t, repo, "main-first", repo, filepath.Join(repo, "src", "app.go"))
	if stdout := claudeWorktreeStop(t, repo, "main-first", worktree); !strings.Contains(stdout, `"decision":"block"`) || !strings.Contains(stdout, "need-ci") {
		t.Fatalf("Stop from the worktree dropped the main checkout's gate: %q", stdout)
	}
	// A change in the worktree keeps its Stop gate after a cd back.
	claudeWorktreeRecordWrite(t, repo, "worktree-first", worktree, filepath.Join(worktree, "src", "app.go"))
	if stdout := claudeWorktreeStop(t, repo, "worktree-first", repo); !strings.Contains(stdout, `"decision":"block"`) || !strings.Contains(stdout, "need-ci") {
		t.Fatalf("Stop from the main checkout dropped the worktree's gate: %q", stdout)
	}
	// A session that changed nothing passes from either checkout.
	for _, cwd := range []string{repo, worktree} {
		if stdout := claudeWorktreeStop(t, repo, "idle", cwd); stdout != "" {
			t.Fatalf("an idle session was blocked from %s: %q", cwd, stdout)
		}
	}
	// A worktree the session changed and whose policy then falls behind the
	// project policy holds the Stop until the policies agree again.
	claudeWorktreeRecordWrite(t, repo, "drifted", worktree, filepath.Join(worktree, "docs", "notes.md"))
	extra := "rules:\n  - id: deny-tmp\n    kind: deny_write\n    paths: ['tmp/**']\n    mode: block\n    message: no scratch files\n"
	if err := os.WriteFile(filepath.Join(repo, "policies", "extra.yml"), []byte(extra), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "e2e"); err != nil {
		t.Fatal(err)
	}
	if stdout := claudeWorktreeStop(t, repo, "drifted", worktree); !strings.Contains(stdout, `"decision":"block"`) || !strings.Contains(stdout, "deny-tmp") {
		t.Fatalf("Stop passed for a changed worktree whose policy fell behind: %q", stdout)
	}
}
