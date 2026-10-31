package agentsession

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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

// TestOperatorClaimStaysExpiredWhenHeadReturns proves that a grant recorded at
// one commit does not come back when HEAD moves on and returns to that commit
// within the same session.
func TestOperatorClaimStaysExpiredWhenHeadReturns(t *testing.T) {
	repo := setupOperatorClaimRepo(t)
	writeProtected(t, repo, "s1", "first change\n")
	if _, err := RecordClaim(repo, protectedClaim, "s1"); err != nil {
		t.Fatal(err)
	}
	if stopBlocked(t, repo, "s1") {
		t.Fatal("control: the operator claim did not authorize its change")
	}
	claimRepoGit(t, repo, "commit", "-q", "--allow-empty", "-m", "unrelated commit")
	claimRepoGit(t, repo, "reset", "-q", "--soft", "HEAD~1")
	if claims := activeClaims(t, repo); len(claims) != 0 {
		t.Fatalf("the grant came back with its commit: %q", claims)
	}
	if !stopBlocked(t, repo, "s1") {
		t.Fatal("the expired claim authorized the protected change again")
	}
}

// TestOperatorClaimFromUnbornHeadStaysExpiredWhenGitFails proves that a grant
// recorded before the first commit stays expired after that commit even when
// Git later cannot resolve HEAD.
func TestOperatorClaimFromUnbornHeadStaysExpiredWhenGitFails(t *testing.T) {
	repo := setupOperatorClaimRepo(t)
	if err := os.RemoveAll(filepath.Join(repo, ".git")); err != nil {
		t.Fatal(err)
	}
	claimRepoGit(t, repo, "init", "-q")
	writeProtected(t, repo, "s1", "change\n")
	if _, err := RecordClaim(repo, protectedClaim, "s1"); err != nil {
		t.Fatal(err)
	}
	if claims := activeClaims(t, repo); !slices.Equal(claims, []string{protectedClaim}) {
		t.Fatalf("control: the claim at the unborn HEAD is not effective: %q", claims)
	}
	claimRepoGit(t, repo, "add", "-A")
	claimRepoGit(t, repo, "commit", "-q", "-m", "first commit")
	if claims := activeClaims(t, repo); len(claims) != 0 {
		t.Fatalf("the first commit did not expire the claim: %q", claims)
	}
	head, err := os.ReadFile(filepath.Join(repo, ".git", "HEAD"))
	if err != nil {
		t.Fatal(err)
	}
	branch := strings.TrimSpace(strings.TrimPrefix(string(head), "ref: "))
	if err := os.WriteFile(filepath.Join(repo, ".git", filepath.FromSlash(branch)), []byte("not-a-commit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if claims := activeClaims(t, repo); len(claims) != 0 {
		t.Fatalf("an unresolvable HEAD revived the claim from the unborn HEAD: %q", claims)
	}
}

// TestOperatorClaimNeedsHeadReflog proves that recording an operator claim
// fails when HEAD names a commit but Git keeps no reflog for it, because a
// return to the same commit could not be detected.
func TestOperatorClaimNeedsHeadReflog(t *testing.T) {
	repo := setupOperatorClaimRepo(t)
	claimRepoGit(t, repo, "config", "core.logAllRefUpdates", "false")
	if err := os.RemoveAll(filepath.Join(repo, ".git", "logs")); err != nil {
		t.Fatal(err)
	}
	writeProtected(t, repo, "s1", "change\n")
	if _, err := RecordClaim(repo, protectedClaim, "s1"); err == nil || !strings.Contains(err.Error(), "reflog") {
		t.Fatalf("recording without a HEAD reflog = %v, want a reflog error", err)
	}
	if !stopBlocked(t, repo, "s1") {
		t.Fatal("an operator claim without a HEAD reflog authorized the change")
	}
}

// TestLegacyOperatorGrantsAuthorizeNothing proves that grants stored without
// a reflog generation no longer authorize a change while revocations stay in force.
func TestLegacyOperatorGrantsAuthorizeNothing(t *testing.T) {
	repo := setupOperatorClaimRepo(t)
	writeProtected(t, repo, "s1", "change\n")
	if _, err := RecordClaim(repo, protectedClaim, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordClaim(repo, "ci-green", "s1"); err != nil {
		t.Fatal(err)
	}
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(claimRepoGitOutput(t, repo, "rev-parse", "HEAD"))
	binding, err := currentHeadBinding(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"claim-grants-v1", "claim-grants-v2"} {
		t.Run(format, func(t *testing.T) {
			legacy := fmt.Sprintf(`{"format_version":%q,"repo_root":%q,"operator_grants":[{"session_id":"s1","claim":%q,"head":%q,"head_reflog_bytes":%d}],"revocations":[{"session_id":"s1","claim":"ci-green"}]}`, format, root, protectedClaim, head, binding.reflogBytes)
			if err := os.WriteFile(claimGrantPath(root), []byte(legacy), 0o600); err != nil {
				t.Fatal(err)
			}
			if claims := activeClaims(t, repo); len(claims) != 0 {
				t.Fatalf("a legacy operator grant or a revoked claim is effective: %q", claims)
			}
		})
	}
}

func claimRepoGitOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	command := exec.CommandContext(t.Context(), "git", args...)
	command.Dir = repo
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(output)
}

// corruptHeadBranch replaces the branch HEAD names with body.
func corruptHeadBranch(t *testing.T, repo, body string) {
	t.Helper()
	head, err := os.ReadFile(filepath.Join(repo, ".git", "HEAD"))
	if err != nil {
		t.Fatal(err)
	}
	branch := strings.TrimSpace(strings.TrimPrefix(string(head), "ref: "))
	if err := os.WriteFile(filepath.Join(repo, ".git", filepath.FromSlash(branch)), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestOperatorClaimRefusesHeadWithoutCommit proves that recording an operator
// claim fails, instead of binding an unborn HEAD, when the branch HEAD names
// is broken or points to a missing commit object.
func TestOperatorClaimRefusesHeadWithoutCommit(t *testing.T) {
	for name, body := range map[string]string{
		"broken reference": "not-a-commit\n",
		"missing object":   "1111111111111111111111111111111111111111\n",
	} {
		t.Run(name, func(t *testing.T) {
			repo := setupOperatorClaimRepo(t)
			writeProtected(t, repo, "s1", "change\n")
			corruptHeadBranch(t, repo, body)
			if _, err := RecordClaim(repo, protectedClaim, "s1"); err == nil {
				t.Fatal("an operator claim was bound to a HEAD without a commit")
			}
			if claims := activeClaims(t, repo); len(claims) != 0 {
				t.Fatalf("a claim on a HEAD without a commit is effective: %q", claims)
			}
		})
	}
}

// TestOperatorClaimOnOrphanBranchBindsUnbornHead proves that an orphan branch
// in a repository with other commits still counts as an unborn HEAD.
func TestOperatorClaimOnOrphanBranchBindsUnbornHead(t *testing.T) {
	repo := setupOperatorClaimRepo(t)
	claimRepoGit(t, repo, "checkout", "-q", "--orphan", "fresh")
	writeProtected(t, repo, "s1", "change\n")
	if _, err := RecordClaim(repo, protectedClaim, "s1"); err != nil {
		t.Fatalf("recording on an orphan branch: %v", err)
	}
	if claims := activeClaims(t, repo); !slices.Equal(claims, []string{protectedClaim}) {
		t.Fatalf("the claim on the orphan branch is not effective: %q", claims)
	}
}

func TestOperatorClaimStaysExpiredAfterReflogCleanup(t *testing.T) {
	for _, observed := range []bool{false, true} {
		t.Run(fmt.Sprintf("expiry_observed=%t", observed), func(t *testing.T) {
			repo := setupOperatorClaimRepo(t)
			writeProtected(t, repo, "s1", "change\n")
			if _, err := RecordClaim(repo, protectedClaim, "s1"); err != nil {
				t.Fatal(err)
			}
			if stopBlocked(t, repo, "s1") {
				t.Fatal("control: the fresh grant did not authorize the change")
			}
			claimRepoGit(t, repo, "commit", "-q", "--allow-empty", "-m", "temporary commit")
			claimRepoGit(t, repo, "reset", "-q", "--soft", "HEAD~1")
			if observed && len(activeClaims(t, repo)) != 0 {
				t.Fatal("the grant did not expire before cleanup")
			}
			claimRepoGit(t, repo, "reflog", "expire", "--expire-unreachable=now", "--all")
			if !stopBlocked(t, repo, "s1") {
				t.Fatal("Stop reused the authorization after reflog cleanup")
			}
			if claims := activeClaims(t, repo); len(claims) != 0 {
				t.Fatalf("reflog cleanup revived the expired grant: %q", claims)
			}
			root, err := ResolveRepoRoot(repo)
			if err != nil {
				t.Fatal(err)
			}
			file, err := loadClaimGrantFile(root)
			if err != nil || len(file.OperatorGrants) != 0 {
				t.Fatalf("the expired grant was not removed: %+v, %v", file.OperatorGrants, err)
			}
			if _, err := RecordClaim(repo, protectedClaim, "s1"); err != nil {
				t.Fatal(err)
			}
			if stopBlocked(t, repo, "s1") {
				t.Fatal("a fresh grant after cleanup did not authorize the change")
			}
		})
	}
}

func TestOperatorClaimExpiresAfterSameContentReflogRewrite(t *testing.T) {
	repo := setupOperatorClaimRepo(t)
	writeProtected(t, repo, "s1", "change\n")
	if _, err := RecordClaim(repo, protectedClaim, "s1"); err != nil {
		t.Fatal(err)
	}
	if stopBlocked(t, repo, "s1") {
		t.Fatal("control: the fresh grant did not authorize the change")
	}
	reflog := strings.TrimSpace(claimRepoGitOutput(t, repo, "rev-parse", "--git-path", "logs/HEAD"))
	if !filepath.IsAbs(reflog) {
		reflog = filepath.Join(repo, reflog)
	}
	before, err := os.ReadFile(reflog)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(reflog)
	if err != nil {
		t.Fatal(err)
	}
	claimRepoGit(t, repo, "reflog", "expire", "--expire=never", "--expire-unreachable=never", "--all")
	if err := os.Chtimes(reflog, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(reflog)
	if err != nil || string(before) != string(after) {
		t.Fatalf("control: rewrite changed the reflog content: %v", err)
	}
	if !stopBlocked(t, repo, "s1") {
		t.Fatal("the rewritten reflog kept the authorization")
	}
	if claims := activeClaims(t, repo); len(claims) != 0 {
		t.Fatalf("same-content rewrite with restored mtime kept the grant: %q", claims)
	}
}

func TestOperatorClaimSurvivesUnverifiedGitObservation(t *testing.T) {
	repo := setupOperatorClaimRepo(t)
	writeProtected(t, repo, "s1", "change\n")
	for _, claim := range []string{protectedClaim, "ci-green"} {
		if _, err := RecordClaim(repo, claim, "s1"); err != nil {
			t.Fatal(err)
		}
	}
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	recorded := activeClaims(t, repo)
	path := os.Getenv("PATH")
	t.Setenv("PATH", t.TempDir())
	claims, err := effectiveClaims(root, "s1", recorded)
	if err != nil || !slices.Equal(claims, []string{"ci-green"}) {
		t.Fatalf("unverified Git state must withhold only operator authority: %q, %v", claims, err)
	}
	file, err := loadClaimGrantFile(root)
	if err != nil || len(file.OperatorGrants) != 1 {
		t.Fatalf("an unverified observation removed the grant: %+v, %v", file.OperatorGrants, err)
	}
	t.Setenv("PATH", path)
	if claims := activeClaims(t, repo); !slices.Equal(claims, recorded) {
		t.Fatalf("recovered Git did not restore the unexpired claims: %q", claims)
	}
}

// TestTimedOperatorClaimSurvivesCommitsUntilExpiry proves that a timed
// operator claim authorizes protected changes across commits and that an
// expired timed claim authorizes nothing.
func TestTimedOperatorClaimSurvivesCommitsUntilExpiry(t *testing.T) {
	repo := setupOperatorClaimRepo(t)
	writeProtected(t, repo, "s1", "first change\n")
	report, err := RecordOperatorClaimFor(repo, protectedClaim, "s1", time.Hour, time.Now())
	if err != nil || report.ExpiresAt == "" {
		t.Fatalf("timed claim report = %+v, %v", report, err)
	}
	if stopBlocked(t, repo, "s1") {
		t.Fatal("the timed operator claim did not authorize its change")
	}
	claimRepoGit(t, repo, "add", "protected.md")
	claimRepoGit(t, repo, "commit", "-q", "-m", "first authorized change")
	writeProtected(t, repo, "s1", "second change\n")
	if stopBlocked(t, repo, "s1") {
		t.Fatal("the timed operator claim ended with the commit")
	}
	if claims := activeClaims(t, repo); !slices.Equal(claims, []string{protectedClaim}) {
		t.Fatalf("active evidence after the commit = %q", claims)
	}
	if _, err := RecordOperatorClaimFor(repo, protectedClaim, "s1", time.Hour, time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !stopBlocked(t, repo, "s1") {
		t.Fatal("an expired timed operator claim still authorized a change")
	}
	if claims := activeClaims(t, repo); len(claims) != 0 {
		t.Fatalf("active evidence carries the expired timed claim: %q", claims)
	}
}

// TestTimedOperatorClaimEndsWithRevocationAndSessionSwitch proves that
// revocation and another active session end a timed operator claim before it
// expires.
func TestTimedOperatorClaimEndsWithRevocationAndSessionSwitch(t *testing.T) {
	repo := setupOperatorClaimRepo(t)
	writeProtected(t, repo, "s1", "change\n")
	if _, err := RecordOperatorClaimFor(repo, protectedClaim, "s1", time.Hour, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := RevokeClaim(repo, protectedClaim, "s1"); err != nil {
		t.Fatal(err)
	}
	if claims := activeClaims(t, repo); len(claims) != 0 {
		t.Fatalf("a revoked timed operator claim is still effective: %q", claims)
	}
	if _, err := RecordOperatorClaimFor(repo, protectedClaim, "s1", time.Hour, time.Now()); err != nil {
		t.Fatal(err)
	}
	if result := RunPostToolUse(repo, []byte(`{"session_id":"s2","tool_name":"Read","tool_input":{"file_path":"src/a.go"}}`)); result.ExitCode != 0 {
		t.Fatalf("read in s2: %+v", result)
	}
	writeProtected(t, repo, "s1", "change again\n")
	if !stopBlocked(t, repo, "s1") {
		t.Fatal("the timed operator claim survived a session switch")
	}
}

// TestTimedOperatorGrantBeyondMaximumIsNotEffective proves that a stored
// expiry later than any recordable duration authorizes nothing.
func TestTimedOperatorGrantBeyondMaximumIsNotEffective(t *testing.T) {
	repo := setupOperatorClaimRepo(t)
	writeProtected(t, repo, "s1", "change\n")
	if _, err := RecordOperatorClaimFor(repo, protectedClaim, "s1", time.Hour, time.Now()); err != nil {
		t.Fatal(err)
	}
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	file, err := loadClaimGrantFile(root)
	if err != nil || len(file.OperatorGrants) != 1 {
		t.Fatalf("grant file = %+v, %v", file, err)
	}
	file.OperatorGrants[0].ExpiresAt = time.Now().Add(MaxTimedOperatorClaim + time.Hour).UTC().Format(time.RFC3339Nano)
	if err := writeClaimGrantFile(root, file); err != nil {
		t.Fatal(err)
	}
	if claims := activeClaims(t, repo); len(claims) != 0 {
		t.Fatalf("an over-long timed grant is effective: %q", claims)
	}
}

func TestTimedOperatorClaimRequiresOperatorClaimAndBoundedDuration(t *testing.T) {
	repo := setupOperatorClaimRepo(t)
	for _, tc := range []struct {
		name     string
		claim    string
		duration time.Duration
	}{
		{name: "too short", claim: protectedClaim, duration: 30 * time.Second},
		{name: "too long", claim: protectedClaim, duration: MaxTimedOperatorClaim + time.Minute},
		{name: "agent claim", claim: "ci-green", duration: time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := RecordOperatorClaimFor(repo, tc.claim, "s1", tc.duration, time.Now()); err == nil {
				t.Fatal("an invalid timed claim was recorded")
			}
		})
	}
}

// TestGrantFileUsesTimedFormatOnlyForTimedGrants proves that a grant file
// without timed grants keeps the previous head-bound format, so an earlier
// release still reads it, and that a timed grant switches the file to the
// current format until it is gone.
func TestGrantFileUsesTimedFormatOnlyForTimedGrants(t *testing.T) {
	repo := setupOperatorClaimRepo(t)
	writeProtected(t, repo, "s1", "change\n")
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	format := func() string {
		t.Helper()
		body, err := os.ReadFile(claimGrantPath(root))
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case strings.Contains(string(body), `"`+claimGrantHeadFormat+`"`) && !strings.Contains(string(body), "expires_at"):
			return claimGrantHeadFormat
		case strings.Contains(string(body), `"`+claimGrantFormatVersion+`"`):
			return claimGrantFormatVersion
		}
		t.Fatalf("unexpected grant file: %s", body)
		return ""
	}
	if _, err := RecordClaim(repo, protectedClaim, "s1"); err != nil {
		t.Fatal(err)
	}
	if got := format(); got != claimGrantHeadFormat {
		t.Fatalf("head-bound grant file format = %s", got)
	}
	if stopBlocked(t, repo, "s1") {
		t.Fatal("a head-bound grant in the previous format did not authorize")
	}
	if _, err := RecordOperatorClaimFor(repo, protectedClaim, "s1", time.Hour, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := format(); got != claimGrantFormatVersion {
		t.Fatalf("timed grant file format = %s", got)
	}
	if _, err := RevokeClaim(repo, protectedClaim, "s1"); err != nil {
		t.Fatal(err)
	}
	if got := format(); got != claimGrantHeadFormat {
		t.Fatalf("grant file without timed grants format = %s", got)
	}
}
