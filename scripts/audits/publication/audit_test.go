package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuditTextRejectsPrivateAndSecretMaterial(t *testing.T) {
	privateName := "omni" + "mus"
	privatePath := "/" + "Users/developer/project"
	sessionURL := "https://claude.ai/code/" + "session_example"
	sessionTrailer := "Claude" + "-Session: local-reference"
	credentialURL := "https://developer:" + "password@example.com/repository"
	accessToken := "sk-" + strings.Repeat("a", 24)
	keyAssignment := "OPENAI_API_" + "KEY=" + strings.Repeat("b", 24)
	privateKey := "-----BEGIN " + "PRIVATE KEY-----"
	body := strings.Join([]string{
		privateName,
		privatePath,
		sessionURL,
		sessionTrailer,
		credentialURL,
		accessToken,
		keyAssignment,
		privateKey,
	}, "\n")
	findings := auditText("fixture.txt", body)
	for _, rule := range []string{
		"content/private-name",
		"content/private-path",
		"content/session-url",
		"content/session-trailer",
		"content/credential-url",
		"content/access-token",
		"content/key-assignment",
		"content/private-key",
	} {
		if !hasFinding(findings, rule) {
			t.Fatalf("missing %s finding: %#v", rule, findings)
		}
	}
}

func TestAuditPathNameRejectsSensitiveArtifacts(t *testing.T) {
	for _, path := range []string{"scripts/.gitkeep", ".env", "keys/id_ed25519", "evidence/transcript.md", "config/secrets.yaml"} {
		if findings := auditPathName(path); len(findings) == 0 {
			t.Fatalf("sensitive path was accepted: %s", path)
		}
	}
	for _, path := range []string{".env.example", ".claude/settings.json", "internal/runtime/agentsession/state.go"} {
		if findings := auditPathName(path); len(findings) != 0 {
			t.Fatalf("public-safe path was rejected: %s: %#v", path, findings)
		}
	}
}

func TestAuditRepositoryScansWorkingTreeAndPostBoundaryHistory(t *testing.T) {
	repo := newAuditRepo(t)
	writeAuditFixture(t, repo, "README.md", "public fixture\n")
	gitAudit(t, repo, "add", "README.md")
	gitAudit(t, repo, "commit", "-m", "baseline", "--quiet")
	boundary := strings.TrimSpace(gitAudit(t, repo, "rev-parse", "HEAD"))
	options := auditOptions{Root: repo, HistoryBoundary: boundary, MaxFileBytes: 1 << 20}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	report, err := auditRepository(ctx, options)
	if err != nil || len(report.Findings) != 0 {
		t.Fatalf("clean fixture failed: report=%#v err=%v", report, err)
	}

	writeAuditFixture(t, repo, "README.md", "private project: "+"gole"+"m\n")
	report, err = auditRepository(ctx, options)
	if err != nil || !hasFinding(report.Findings, "content/private-name") {
		t.Fatalf("dirty tracked leak was missed: report=%#v err=%v", report, err)
	}
	writeAuditFixture(t, repo, "README.md", "public fixture\n")

	writeAuditFixture(t, repo, "evidence/transcript.md", "private project: "+"gole"+"m\n")
	gitAudit(t, repo, "add", "evidence/transcript.md")
	gitAudit(t, repo, "commit", "-m", "temporary unsafe artifact", "--quiet")
	gitAudit(t, repo, "rm", "evidence/transcript.md", "--quiet")
	gitAudit(t, repo, "commit", "-m", "remove temporary artifact", "--quiet")
	report, err = auditRepository(ctx, options)
	if err != nil || !hasFinding(report.Findings, "path/session-material") || !hasFinding(report.Findings, "content/private-name") {
		t.Fatalf("removed post-boundary leak was missed: report=%#v err=%v", report, err)
	}

	trailer := "Claude" + "-Session: https://claude.ai/code/" + "session_new"
	gitAudit(t, repo, "commit", "--allow-empty", "-m", "unsafe history", "-m", trailer, "--quiet")
	report, err = auditRepository(ctx, options)
	if err != nil || !hasFinding(report.Findings, "content/session-trailer") || !hasFinding(report.Findings, "content/session-url") {
		t.Fatalf("post-boundary session evidence was missed: report=%#v err=%v", report, err)
	}
}

func TestTrackedPathsRejectsUnmergedIndex(t *testing.T) {
	repo := newAuditRepo(t)
	writeAuditFixture(t, repo, "conflict.txt", "baseline\n")
	gitAudit(t, repo, "add", "conflict.txt")
	gitAudit(t, repo, "commit", "-m", "baseline", "--quiet")
	baseBranch := strings.TrimSpace(gitAudit(t, repo, "branch", "--show-current"))

	gitAudit(t, repo, "checkout", "-b", "conflict-side", "--quiet")
	writeAuditFixture(t, repo, "conflict.txt", "side\n")
	gitAudit(t, repo, "commit", "-am", "side", "--quiet")
	gitAudit(t, repo, "checkout", baseBranch, "--quiet")
	writeAuditFixture(t, repo, "conflict.txt", "base\n")
	gitAudit(t, repo, "commit", "-am", "base", "--quiet")

	command := exec.Command("git", "-C", repo, "merge", "conflict-side")
	if body, err := command.CombinedOutput(); err == nil {
		t.Fatalf("fixture merge unexpectedly succeeded: %s", body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := trackedPaths(ctx, repo)
	if err == nil || !strings.Contains(err.Error(), "unmerged Git index") {
		t.Fatalf("unmerged index was accepted: %v", err)
	}
}

func TestLegacyHistoryExceptionIsBoundedAndOwned(t *testing.T) {
	if legacyHistoryException.ThroughCommit != defaultHistoryBoundary {
		t.Fatalf("history exception boundary drifted: %#v", legacyHistoryException)
	}
	if strings.TrimSpace(legacyHistoryException.Owner) == "" || strings.TrimSpace(legacyHistoryException.Rationale) == "" {
		t.Fatalf("history exception is missing ownership metadata: %#v", legacyHistoryException)
	}
}

func TestAuditRepositoryRequiresBoundaryAncestry(t *testing.T) {
	repo := newAuditRepo(t)
	writeAuditFixture(t, repo, "README.md", "public fixture\n")
	gitAudit(t, repo, "add", "README.md")
	gitAudit(t, repo, "commit", "-m", "baseline", "--quiet")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := auditRepository(ctx, auditOptions{Root: repo, HistoryBoundary: strings.Repeat("a", 40), MaxFileBytes: 1 << 20})
	if err == nil || !strings.Contains(err.Error(), "boundary is unavailable") {
		t.Fatalf("missing history boundary was accepted: %v", err)
	}
}

func TestAuditRepositoryDoesNotTreatDeletionAsNewPathLeak(t *testing.T) {
	repo := newAuditRepo(t)
	writeAuditFixture(t, repo, "scripts/.gitkeep", "")
	gitAudit(t, repo, "add", "scripts/.gitkeep")
	gitAudit(t, repo, "commit", "-m", "legacy placeholder", "--quiet")
	boundary := strings.TrimSpace(gitAudit(t, repo, "rev-parse", "HEAD"))
	gitAudit(t, repo, "rm", "scripts/.gitkeep", "--quiet")
	gitAudit(t, repo, "commit", "-m", "remove legacy placeholder", "--quiet")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	report, err := auditRepository(ctx, auditOptions{Root: repo, HistoryBoundary: boundary, MaxFileBytes: 1 << 20})
	if err != nil || len(report.Findings) != 0 {
		t.Fatalf("safe deletion was reported as a new leak: report=%#v err=%v", report, err)
	}
}

func newAuditRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitAudit(t, repo, "init", "--quiet")
	gitAudit(t, repo, "config", "user.name", "reconc-test")
	gitAudit(t, repo, "config", "user.email", "reconc-test@example.com")
	return repo
}

func writeAuditFixture(t *testing.T, root, relative, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitAudit(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	body, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, body)
	}
	return string(body)
}

func hasFinding(findings []auditFinding, rule string) bool {
	for _, finding := range findings {
		if finding.Rule == rule {
			return true
		}
	}
	return false
}
