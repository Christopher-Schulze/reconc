package hooks

import (
	"encoding/json"
	stderrors "errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	rerrors "reconc.dev/reconc/internal/errors"
)

func TestGenerateGitPreCommitContainsReconcCI(t *testing.T) {
	a, err := Generate(KindGitPreCommit)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if a.Kind != KindGitPreCommit {
		t.Errorf("kind wrong: %s", a.Kind)
	}
	if !a.Executable {
		t.Error("git pre-commit should be marked executable")
	}
	if !strings.Contains(a.Content, "reconc ci") {
		t.Errorf("content should reference `reconc ci`, got: %s", a.Content)
	}
	if !strings.HasPrefix(a.Content, "#!/bin/sh") {
		t.Errorf("content should start with shebang, got: %s", a.Content[:50])
	}
}

func TestGenerateClaudeCodeIsValidJSON(t *testing.T) {
	a, err := Generate(KindClaudeCode)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(a.Content), &payload); err != nil {
		t.Fatalf("content is not valid JSON: %v\n%s", err, a.Content)
	}
	if _, ok := payload["hooks"]; !ok {
		t.Errorf("expected 'hooks' key in Claude Code template")
	}
	if !strings.Contains(a.Content, "reconc hook runtime claude-pre-tool-use") {
		t.Errorf("expected reconc routes in template")
	}
}

func TestGenerateCodexIsValidJSON(t *testing.T) {
	a, err := Generate(KindCodex)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(a.Content), &payload); err != nil {
		t.Fatalf("content is not valid JSON: %v\n%s", err, a.Content)
	}
	if !strings.Contains(a.Content, "codex-pre-tool-use") {
		t.Errorf("expected codex routes in template")
	}
}

func TestGenerateUnknownKind(t *testing.T) {
	_, err := Generate("not-a-kind")
	if err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func TestInstallGitPreCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	c := exec.Command("git", "init", "--quiet")
	c.Dir = repo
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	report, err := Install(KindGitPreCommit, repo, false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if report.Action != "created" {
		t.Errorf("expected 'created', got %s", report.Action)
	}
	if report.TargetPath != GitPreCommitPath {
		t.Errorf("target path wrong: %s", report.TargetPath)
	}
	target := filepath.Join(repo, ".git", "hooks", "pre-commit")
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("hook missing: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Error("hook should be executable")
	}
}

// gitInitRepo is a small helper that runs `git init` in the given dir
// (and only in that dir, not the test process cwd).
func gitInitRepo(t *testing.T, dir string) {
	t.Helper()
	c := exec.Command("git", "init", "--quiet")
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git init in %s: %v\n%s", dir, err, out)
	}
}

func TestInstallGitPreCommitRefusesOverwriteWithoutForce(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	gitInitRepo(t, repo)

	if _, err := Install(KindGitPreCommit, repo, false); err != nil {
		t.Fatalf("first install: %v", err)
	}
	_, err := Install(KindGitPreCommit, repo, false)
	if err == nil {
		t.Fatal("expected error on second install without force")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected 'already exists', got: %v", err)
	}
}

func TestInstallGitPreCommitForceOverwrites(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	gitInitRepo(t, repo)

	if _, err := Install(KindGitPreCommit, repo, false); err != nil {
		t.Fatalf("first install: %v", err)
	}
	report, err := Install(KindGitPreCommit, repo, true)
	if err != nil {
		t.Fatalf("force install: %v", err)
	}
	if report.Action != "updated" {
		t.Errorf("expected 'updated', got %s", report.Action)
	}
}

func TestInstallGitPreCommitNonGitDirReturnsError(t *testing.T) {
	repo := t.TempDir()
	_, err := Install(KindGitPreCommit, repo, false)
	if err == nil {
		t.Fatal("expected error for non-git dir")
	}
	if !strings.Contains(err.Error(), "no .git") {
		t.Errorf("expected 'no .git' in error, got: %v", err)
	}
}

func TestInstallClaudeCodeCreatesFreshFile(t *testing.T) {
	repo := t.TempDir()
	report, err := Install(KindClaudeCode, repo, false)
	if err != nil {
		t.Fatalf("install claude-code: %v", err)
	}
	if report.Action != "created" {
		t.Errorf("expected action=created, got %s", report.Action)
	}
	data, err := os.ReadFile(filepath.Join(repo, ClaudeCodeSettingsPath))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	if !strings.Contains(string(data), `"reconc hook runtime claude-session-start`) {
		t.Errorf("expected reconc session-start entry in settings.json, got:\n%s", string(data))
	}
}

func TestInstallClaudeCodeMergesExistingConfig(t *testing.T) {
	repo := t.TempDir()
	// Pre-existing settings.json with a user setting + a hand-written
	// non-reconc hook entry that reconc must NOT remove.
	pre := `{
  "editor": "vscode",
  "hooks": {
    "SessionStart": [
      { "hooks": [ { "type": "command", "command": "echo user-custom" } ] }
    ]
  }
}`
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ClaudeCodeSettingsPath), []byte(pre), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := Install(KindClaudeCode, repo, false)
	if err != nil {
		t.Fatalf("install claude-code: %v", err)
	}
	if report.Action != "updated" {
		t.Errorf("expected action=updated, got %s", report.Action)
	}
	data, _ := os.ReadFile(filepath.Join(repo, ClaudeCodeSettingsPath))
	got := string(data)
	// User's non-reconc settings preserved.
	if !strings.Contains(got, `"editor": "vscode"`) {
		t.Errorf("user's editor setting lost:\n%s", got)
	}
	if !strings.Contains(got, `"echo user-custom"`) {
		t.Errorf("user's hand-written hook lost:\n%s", got)
	}
	// reconc's hooks present.
	if !strings.Contains(got, "reconc hook runtime claude-session-start") {
		t.Errorf("reconc hook not merged in:\n%s", got)
	}
}

func TestInstallClaudeCodeIsIdempotent(t *testing.T) {
	repo := t.TempDir()
	if _, err := Install(KindClaudeCode, repo, false); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if _, err := Install(KindClaudeCode, repo, false); err != nil {
		t.Fatalf("second install: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(repo, ClaudeCodeSettingsPath))
	// Count how many SessionStart hook entries are reconc-owned.
	// Should be exactly one even after running install twice.
	count := strings.Count(string(data), "reconc hook runtime claude-session-start")
	if count != 1 {
		t.Errorf("expected exactly 1 reconc session-start entry after double install, got %d\nfile:\n%s",
			count, string(data))
	}
}

func TestInstallCodexCreatesFreshFile(t *testing.T) {
	repo := t.TempDir()
	report, err := Install(KindCodex, repo, false)
	if err != nil {
		t.Fatalf("install codex: %v", err)
	}
	if report.Action != "created" {
		t.Errorf("expected action=created, got %s", report.Action)
	}
	data, _ := os.ReadFile(filepath.Join(repo, CodexHooksPath))
	if !strings.Contains(string(data), "reconc hook runtime") {
		t.Errorf("expected reconc command in codex hooks.json, got:\n%s", string(data))
	}
}

func TestInstallClaudeCodeMalformedJSONRejected(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ClaudeCodeSettingsPath), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Install(KindClaudeCode, repo, false)
	if err == nil {
		t.Fatal("expected error on malformed JSON without --force")
	}
	// With --force it should overwrite.
	if _, err := Install(KindClaudeCode, repo, true); err != nil {
		t.Errorf("--force should overwrite malformed JSON; got: %v", err)
	}
}

func TestInstallUnknownKind(t *testing.T) {
	_, err := Install("not-a-kind", t.TempDir(), false)
	if err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func TestPolicySourceErrorTypeUsed(t *testing.T) {
	_, err := Generate("not-a-kind")
	var pse *rerrors.PolicySourceError
	if !stderrors.As(err, &pse) {
		t.Errorf("expected *PolicySourceError, got %T", err)
	}
}

// --- classifier + user-edit surfacing -------------------------------

func TestClassifyHookEntryCanonicalVsModified(t *testing.T) {
	canonical := `reconc hook runtime claude-session-start "$CLAUDE_PROJECT_DIR"`
	mkEntry := func(cmd string) map[string]interface{} {
		return map[string]interface{}{
			"hooks": []interface{}{
				map[string]interface{}{"type": "command", "command": cmd},
			},
		}
	}
	cases := []struct {
		name string
		cmd  string
		want HookEntryClass
	}{
		{"canonical", canonical, CanonicalReconc},
		{"user modified with --debug", canonical + " --debug", ModifiedReconc},
		{"user wrapped in sh -c", `sh -c 'reconc hook runtime claude-session-start .'`, NonReconc}, // not a prefix
		{"user custom echo", `echo user-custom`, NonReconc},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifyHookEntry(mkEntry(c.cmd), canonical)
			if got != c.want {
				t.Errorf("classifyHookEntry(%q) = %d, want %d", c.cmd, got, c.want)
			}
		})
	}
}

func TestInstallReportsDroppedUserEdits(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-existing settings with a user-modified reconc entry (adds
	// --debug to the canonical command).
	preExisting := `{
  "hooks": {
    "SessionStart": [
      { "hooks": [ { "type": "command", "command": "reconc hook runtime claude-session-start --debug \"$CLAUDE_PROJECT_DIR\"" } ] }
    ]
  }
}`
	if err := os.WriteFile(filepath.Join(repo, ClaudeCodeSettingsPath), []byte(preExisting), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := Install(KindClaudeCode, repo, false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(report.DroppedUserEdits) == 0 {
		t.Fatal("expected DroppedUserEdits to report the --debug entry")
	}
	found := false
	for _, e := range report.DroppedUserEdits {
		if strings.Contains(e, "--debug") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected --debug entry in DroppedUserEdits, got %v", report.DroppedUserEdits)
	}
}

// --- merge validates array type -------------------------------------

func TestInstallSurfacesNonArrayHooksEvent(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	// User accidentally made hooks.SessionStart an OBJECT not an array.
	pre := `{
  "hooks": {
    "SessionStart": { "note": "I did this wrong" }
  }
}`
	if err := os.WriteFile(filepath.Join(repo, ClaudeCodeSettingsPath), []byte(pre), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := Install(KindClaudeCode, repo, false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	// The user's malformed shape is overwritten, but it must be
	// reported via DroppedUserEdits so the CLI can warn.
	found := false
	for _, e := range report.DroppedUserEdits {
		if strings.Contains(e, "SessionStart") && strings.Contains(e, "object") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected non-array SessionStart to be reported, got: %v", report.DroppedUserEdits)
	}
}
