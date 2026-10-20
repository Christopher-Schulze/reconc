package agentsession

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/lockdiff"
	"reconc.dev/reconc/internal/pathidentity"
)

const (
	maxLinkedWorktrees     = 4096
	maxPolicyGapIdentities = 8
)

// ClaudeWorktreeRoot returns the linked worktree whose own policy governs a
// Claude Code session working in dir, or "" to keep projectRoot. The payload
// cwd follows every `cd`, so it selects a worktree only when that worktree's
// policy keeps every rule of the project policy; anything else stays under
// the project policy that installed the hooks.
func ClaudeWorktreeRoot(projectRoot, dir string) (string, error) {
	worktree, err := LinkedWorktreeRoot(projectRoot, dir)
	if err != nil || worktree == "" {
		return "", err
	}
	if worktreePolicyGap(projectRoot, worktree) != "" {
		return "", nil
	}
	return worktree, nil
}

// HasLinkedWorktrees reports whether root's repository registers a linked
// worktree, so a caller can skip locating one when none exists. An unreadable
// repository reports true and leaves the decision to LinkedWorktreeRoot.
func HasLinkedWorktrees(root string) bool {
	common, err := checkoutCommonDir(root)
	if err != nil || common == "" {
		return err != nil
	}
	info, err := os.Lstat(filepath.Join(common, "worktrees"))
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	return err != nil || info.IsDir()
}

// worktreePolicyGap explains why a linked worktree's policy does not keep
// every rule enforced at root, or returns "" when it does: the worktree is its
// own policy root and its compiled lockfile keeps every rule of root's
// lockfile unchanged and every other enforced field identical. Added rules
// only make the worktree stricter.
func worktreePolicyGap(root, worktree string) string {
	discovery, err := ingest.DiscoverPolicyRepo(worktree)
	if err != nil {
		return fmt.Sprintf("its policy cannot be discovered: %v", err)
	}
	if !discovery.Discovered || filepath.Clean(discovery.RepoRoot) != filepath.Clean(worktree) {
		return "it has no Reconc policy of its own"
	}
	rootLock := filepath.Join(root, ingest.LockfilePath)
	ownLock := filepath.Join(worktree, ingest.LockfilePath)
	own, err := boundedio.ReadRegularFile(ownLock, compiler.MaxLockfileBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return "it has no compiled policy; run `reconc refresh .` inside it"
	}
	if err != nil {
		return fmt.Sprintf("its compiled policy cannot be read: %v", err)
	}
	enforced, err := boundedio.ReadRegularFile(rootLock, compiler.MaxLockfileBytes)
	if err != nil {
		return fmt.Sprintf("the enforced policy cannot be read: %v", err)
	}
	if bytes.Equal(own, enforced) {
		return ""
	}
	report, err := lockdiff.Diff(rootLock, ownLock)
	if err != nil {
		return fmt.Sprintf("its compiled policy cannot be compared: %v", err)
	}
	gaps := []string{}
	if ids := ruleIdentities(report.Removed); len(ids) > 0 {
		gaps = append(gaps, "it drops the rules "+boundedIdentityList(ids))
	}
	changed := make([]string, 0, len(report.Changed))
	for _, rule := range report.Changed {
		changed = append(changed, rule.ID)
	}
	if len(changed) > 0 {
		gaps = append(gaps, "it changes the rules "+boundedIdentityList(changed))
	}
	fields := []string{}
	for _, change := range report.EnvelopeChanges {
		if change.Class == lockdiff.FieldClassSemantic || change.Class == lockdiff.FieldClassUnsupported {
			fields = append(fields, change.Field)
		}
	}
	if len(fields) > 0 {
		gaps = append(gaps, "it changes the policy fields "+boundedIdentityList(fields))
	}
	return strings.Join(gaps, "; ")
}

func ruleIdentities(rules []lockdiff.RuleInfo) []string {
	ids := make([]string, 0, len(rules))
	for _, rule := range rules {
		ids = append(ids, rule.ID)
	}
	return ids
}

func boundedIdentityList(values []string) string {
	if len(values) <= maxPolicyGapIdentities {
		return strings.Join(values, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(values[:maxPolicyGapIdentities], ", "), len(values)-maxPolicyGapIdentities)
}

// SessionCheckout is another checkout of the project in which a session
// recorded evidence. PolicyGap is set when that checkout is a linked worktree
// whose policy no longer keeps every project rule.
type SessionCheckout struct {
	Root      string
	PolicyGap string
}

// OtherSessionCheckouts returns the checkouts of the project at projectRoot,
// other than evaluatedRoot, in which the payload's session recorded evidence.
// A Claude Code session moves between the project and its linked worktrees by
// changing directory, and each checkout keeps its own session state, so a
// Stop evaluated in one checkout must also cover every other one it changed.
func OtherSessionCheckouts(projectRoot, evaluatedRoot string, payloadBytes []byte) ([]SessionCheckout, error) {
	payload, err := ParsePayload(payloadBytes)
	if err != nil || validateSessionID(payload.SessionID) != nil {
		// The evaluated Stop already rejected an unusable session identity.
		return nil, nil
	}
	worktrees, err := linkedWorktreeCheckouts(projectRoot)
	if err != nil {
		return nil, err
	}
	checkouts := []SessionCheckout{}
	for _, root := range append([]string{projectRoot}, worktrees...) {
		if root == evaluatedRoot || !sessionRecordedEvidence(root, payload.SessionID) {
			continue
		}
		checkout := SessionCheckout{Root: root}
		if root != projectRoot {
			checkout.PolicyGap = worktreePolicyGap(projectRoot, root)
		}
		checkouts = append(checkouts, checkout)
	}
	return checkouts, nil
}

// sessionRecordedEvidence reports whether the session recorded a write, a
// command, a command result or sealed evidence in the checkout at root. A
// state file that cannot be read counts as evidence, so the Stop evaluated
// there reports the failure.
func sessionRecordedEvidence(root, sessionID string) bool {
	state, err := readSessionStateResolved(root, sessionID)
	if err != nil {
		return true
	}
	return len(state.WritePaths) > 0 || len(state.Commands) > 0 || len(state.CommandResults) > 0 ||
		state.EvidenceSegmentCount > 0 || state.EvidenceOverflow
}

// linkedWorktreeCheckouts lists the existing linked worktrees registered in
// the common Git directory of the checkout at root, sorted by path.
func linkedWorktreeCheckouts(root string) ([]string, error) {
	common, err := checkoutCommonDir(root)
	if err != nil || common == "" {
		return nil, err
	}
	registry := filepath.Join(common, "worktrees")
	entries, err := boundedio.ReadDirNoSymlink(registry, maxLinkedWorktrees)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list linked worktrees: %w", err)
	}
	worktrees := []string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		link, err := boundedio.ReadFile(filepath.Join(registry, entry.Name(), "gitdir"), maxGitLinkBytes)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read linked worktree %s: %w", entry.Name(), err)
		}
		gitLink := strings.TrimSpace(string(link))
		if !filepath.IsAbs(gitLink) {
			gitLink = filepath.Join(registry, entry.Name(), gitLink)
		}
		checkout := filepath.Dir(filepath.Clean(gitLink))
		if _, err := os.Stat(checkout); err != nil {
			// Git keeps a registration until `git worktree prune`.
			continue
		}
		worktree, err := LinkedWorktreeRoot(root, checkout)
		if err != nil {
			return nil, err
		}
		if canonical, resolveErr := pathidentity.ResolveExisting(checkout); worktree != "" && resolveErr == nil && worktree == canonical {
			worktrees = append(worktrees, worktree)
		}
	}
	sort.Strings(worktrees)
	return worktrees, nil
}
