package agentsession

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

const protectedClaim = "operator:edit-protected"

// setupOperatorClaimRepo creates a compiled Git repository whose policy blocks
// writes to protected.md unless the session holds protectedClaim.
func setupOperatorClaimRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv(StateRootEnv, t.TempDir())
	t.Setenv("TMPDIR", t.TempDir())
	repo := t.TempDir()
	for path, body := range map[string]string{
		"AGENTS.md":    "# t\n",
		"protected.md": "protected\n",
		"src/a.go":     "package src\n",
		"policies/rules.yml": `rules:
  - id: protected-edit
    kind: require_claim
    when_paths: ['protected.md']
    claims: ['` + protectedClaim + `']
    mode: block
    message: protected.md needs the operator claim
`,
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, path), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatalf("compile: %v", err)
	}
	claimRepoGit(t, repo, "init", "-q")
	claimRepoGit(t, repo, "add", "-A")
	claimRepoGit(t, repo, "commit", "-q", "-m", "fixture")
	return repo
}

func claimRepoGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	command := exec.CommandContext(t.Context(), "git", args...)
	command.Dir = repo
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(repo, ".git-test-config"),
		"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
		"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

// writeProtected changes protected.md on disk and records the write as the
// host's PostToolUse event of sessionID would.
func writeProtected(t *testing.T, repo, sessionID, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, "protected.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"session_id":"` + sessionID + `","tool_name":"Write","tool_input":{"file_path":"protected.md"}}`)
	if result := RunPostToolUse(repo, payload); result.ExitCode != 0 {
		t.Fatalf("post: %+v", result)
	}
}

// stopBlocked runs Stop for sessionID and reports whether it refused to end
// the turn.
func stopBlocked(t *testing.T, repo, sessionID string) bool {
	t.Helper()
	result := RunStop(repo, []byte(`{"session_id":"`+sessionID+`"}`))
	return result.ExitCode != 0 || result.Stdout != ""
}

func activeClaims(t *testing.T, repo string) []string {
	t.Helper()
	claims, err := ActiveClaims(repo)
	if err != nil {
		t.Fatal(err)
	}
	return claims
}

// TestOperatorClaimAuthorizesOnlyUntilHeadMoves proves that an operator claim
// satisfies the protected rule for the change it was recorded for, that the
// committed change no longer blocks Stop once HEAD moved, and that a further
// protected change needs a fresh claim, for Stop and for the active evidence
// that reconc ci reads.
func TestOperatorClaimAuthorizesOnlyUntilHeadMoves(t *testing.T) {
	repo := setupOperatorClaimRepo(t)
	writeProtected(t, repo, "s1", "first change\n")
	if !stopBlocked(t, repo, "s1") {
		t.Fatal("control: an unauthorized protected change did not block Stop")
	}
	if _, err := RecordClaim(repo, protectedClaim, "s1"); err != nil {
		t.Fatal(err)
	}
	if stopBlocked(t, repo, "s1") {
		t.Fatal("the operator claim did not authorize its change")
	}
	if claims := activeClaims(t, repo); !slices.Equal(claims, []string{protectedClaim}) {
		t.Fatalf("active evidence before the commit = %q", claims)
	}
	claimRepoGit(t, repo, "add", "protected.md")
	claimRepoGit(t, repo, "commit", "-q", "-m", "the authorized change")
	if claims := activeClaims(t, repo); len(claims) != 0 {
		t.Fatalf("active evidence still carries the expired claim: %q", claims)
	}
	if stopBlocked(t, repo, "s1") {
		t.Fatal("Stop blocked the committed authorized change")
	}
	writeProtected(t, repo, "s1", "second change\n")
	if !stopBlocked(t, repo, "s1") {
		t.Fatal("the operator claim outlived its commit")
	}
	if _, err := RecordClaim(repo, protectedClaim, "s1"); err != nil {
		t.Fatal(err)
	}
	if stopBlocked(t, repo, "s1") {
		t.Fatal("a fresh operator claim did not authorize the next change")
	}
}

// TestOperatorClaimEndsWithSessionSwitchAndCleanup proves that switching the
// active session ends an operator claim even when the first session becomes
// active again, and that cleanup forgets the session's grant records.
func TestOperatorClaimEndsWithSessionSwitchAndCleanup(t *testing.T) {
	repo := setupOperatorClaimRepo(t)
	writeProtected(t, repo, "s1", "change\n")
	if _, err := RecordClaim(repo, protectedClaim, "s1"); err != nil {
		t.Fatal(err)
	}
	if stopBlocked(t, repo, "s1") {
		t.Fatal("the operator claim did not authorize")
	}
	if result := RunPostToolUse(repo, []byte(`{"session_id":"s2","tool_name":"Read","tool_input":{"file_path":"src/a.go"}}`)); result.ExitCode != 0 {
		t.Fatalf("read in s2: %+v", result)
	}
	writeProtected(t, repo, "s1", "change again\n")
	if !stopBlocked(t, repo, "s1") {
		t.Fatal("the operator claim survived a session switch")
	}
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	if claims, err := effectiveClaims(root, "s1", []string{protectedClaim}); err != nil || len(claims) != 0 {
		t.Fatalf("the switched-away grant is still effective: %q %v", claims, err)
	}
	if _, err := RecordClaim(repo, protectedClaim, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordClaim(repo, "ci-green", "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := RevokeClaim(repo, "ci-green", "s1"); err != nil {
		t.Fatal(err)
	}
	if err := CleanupSessionState(repo, "s1"); err != nil {
		t.Fatal(err)
	}
	file, err := loadClaimGrantFile(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.OperatorGrants) != 0 || len(file.Revocations) != 0 {
		t.Fatalf("cleanup kept the session's claim records: %+v", file)
	}
}

// TestOperatorClaimWithoutGrantIsNotEffective covers sessions recorded by an
// older release: their operator claims have no grant and authorize nothing,
// while agent claims keep working.
func TestOperatorClaimWithoutGrantIsNotEffective(t *testing.T) {
	repo := setupOperatorClaimRepo(t)
	writeProtected(t, repo, "legacy", "change\n")
	if _, err := MutateSessionState(repo, "legacy", func(state SessionState) SessionState {
		return AppendClaim(AppendClaim(state, protectedClaim), "ci-green")
	}); err != nil {
		t.Fatal(err)
	}
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := effectiveClaims(root, "legacy", []string{protectedClaim, "ci-green"})
	if err != nil || !slices.Equal(claims, []string{"ci-green"}) {
		t.Fatalf("effective legacy claims = %q, %v", claims, err)
	}
	if !stopBlocked(t, repo, "legacy") {
		t.Fatal("a legacy operator claim authorized a protected change")
	}
}

// TestRevokedClaimStaysRevokedAcrossSealingAndReload proves that a revoked
// claim does not return from sealed evidence segments or a state reload, and
// that recording it again restores it.
func TestRevokedClaimStaysRevokedAcrossSealingAndReload(t *testing.T) {
	repo := setupOperatorClaimRepo(t)
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeActiveSession(root, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordClaim(repo, "ci-green", "s1"); err != nil {
		t.Fatal(err)
	}
	if err := withSessionLock(root, "s1", func() error {
		current, err := loadSessionStateResolved(root, "s1")
		if err != nil {
			return err
		}
		rotated, err := rotateSessionEvidenceLocked(root, current)
		if err != nil {
			return err
		}
		return saveSessionStateLocked(rotated)
	}); err != nil {
		t.Fatal(err)
	}
	if claims := activeClaims(t, repo); !slices.Equal(claims, []string{"ci-green"}) {
		t.Fatalf("sealed claim missing before revocation: %q", claims)
	}
	report, err := RevokeClaim(repo, "ci-green", "")
	if err != nil || !report.Revoked || report.SessionID != "s1" || report.ClaimCount != 0 {
		t.Fatalf("revoke = %+v, %v", report, err)
	}
	if claims := activeClaims(t, repo); len(claims) != 0 {
		t.Fatalf("a revoked claim returned from sealed evidence: %q", claims)
	}
	state, err := LoadSessionState(repo, "s1")
	if err != nil {
		t.Fatal(err)
	}
	complete, err := loadCompleteSessionEvidence(root, state)
	if err != nil || !slices.Contains(complete.Claims, "ci-green") {
		t.Fatalf("the evidence chain must keep recording the claim: %q, %v", complete.Claims, err)
	}
	if _, err := RecordClaim(repo, "ci-green", "s1"); err != nil {
		t.Fatal(err)
	}
	if claims := activeClaims(t, repo); !slices.Equal(claims, []string{"ci-green"}) {
		t.Fatalf("recording again did not restore the claim: %q", claims)
	}
}

// TestClaimGrantFileFailsClosed proves that a damaged grant file stops
// claim-dependent decisions instead of dropping revocations.
func TestClaimGrantFileFailsClosed(t *testing.T) {
	repo := setupOperatorClaimRepo(t)
	writeProtected(t, repo, "s1", "change\n")
	if _, err := RecordClaim(repo, protectedClaim, "s1"); err != nil {
		t.Fatal(err)
	}
	if stopBlocked(t, repo, "s1") {
		t.Fatal("control: the intact grant did not authorize the change")
	}
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"malformed":        "{",
		"unknown field":    `{"format_version":"claim-grants-v1","repo_root":"` + root + `","operator_grants":[],"revocations":[],"extra":1}`,
		"foreign root":     `{"format_version":"claim-grants-v1","repo_root":"/elsewhere","operator_grants":[],"revocations":[]}`,
		"agent as grant":   `{"format_version":"claim-grants-v1","repo_root":"` + root + `","operator_grants":[{"session_id":"s1","claim":"ci-green","head":""}],"revocations":[]}`,
		"empty revocation": `{"format_version":"claim-grants-v1","repo_root":"` + root + `","operator_grants":[],"revocations":[{"session_id":"s1","claim":" "}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(claimGrantPath(root), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ActiveClaims(repo); err == nil {
				t.Fatal("a damaged grant file was accepted")
			}
			if !stopBlocked(t, repo, "s1") {
				t.Fatal("a damaged grant file let Stop pass")
			}
		})
	}
}
