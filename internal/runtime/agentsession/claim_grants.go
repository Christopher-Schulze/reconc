package agentsession

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/boundedio"
)

// An operator claim authorizes the changes up to the next commit. Its grant
// records the session, Git HEAD, and the size and filesystem generation of
// HEAD's reflog. Reflog writes and replacements expire it, including cleanup
// that restores the original HEAD and reflog bytes between evaluations.
// Observed expiry removes the grant under the active-session lock; a session
// switch or cleanup also drops it. Revocations remove any claim from a session's
// effective claims. Grants and revocations live in one private per-project
// file outside the sealed evidence chain, so reloading sealed evidence cannot
// bring an expired or revoked claim back; the evidence keeps recording that the
// claim was made.
const (
	claimGrantFormatVersion = "claim-grants-v3"
	// claimGrantLegacyFormat bound grants to the commit alone; its grants
	// authorize nothing, while its revocations stay in force.
	claimGrantLegacyFormat = "claim-grants-v1"
	// claimGrantSizeOnlyFormat also bound the reflog size, which cleanup can
	// restore; neither earlier format's grants authorize a change.
	claimGrantSizeOnlyFormat = "claim-grants-v2"
	claimGrantFileName       = "claim-grants.json"
	maxClaimGrantBytes       = 256 * 1024
	maxOperatorClaimGrants   = 256
	maxClaimRevocations      = 1024
)

type operatorClaimGrant struct {
	SessionID        string `json:"session_id"`
	Claim            string `json:"claim"`
	Head             string `json:"head"`
	ReflogBytes      int64  `json:"head_reflog_bytes"`
	ReflogGeneration string `json:"head_reflog_generation"`
}

// headBinding is the Git HEAD commit, empty while HEAD is unborn, and the
// size and filesystem generation of HEAD's reflog. The generation includes
// file identity and change time, so replacing or rewriting the log expires
// a binding even when its bytes and modification time return unchanged.
type headBinding struct {
	head             string
	reflogBytes      int64
	reflogGeneration string
}

type claimRevocation struct {
	SessionID string `json:"session_id"`
	Claim     string `json:"claim"`
}

type claimGrantFile struct {
	FormatVersion  string               `json:"format_version"`
	RepoRoot       string               `json:"repo_root"`
	OperatorGrants []operatorClaimGrant `json:"operator_grants"`
	Revocations    []claimRevocation    `json:"revocations"`
}

func claimGrantPath(root string) string {
	return filepath.Join(projectDir(root), claimGrantFileName)
}

func isOperatorClaim(claim string) bool {
	return strings.HasPrefix(strings.TrimSpace(claim), OperatorClaimPrefix)
}

// effectiveClaims returns the claims of sessionID that may authorize policy
// decisions now: every recorded claim that is not revoked, where an operator
// claim additionally needs a grant of this session at the current HEAD. An
// operator claim recorded without a grant, as by an older release, is never
// effective. An unreadable grant file fails closed with an error.
func effectiveClaims(root, sessionID string, claims []string) ([]string, error) {
	if len(claims) == 0 {
		return claims, nil
	}
	file, err := loadClaimGrantFile(root)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(file.OperatorGrants, func(grant operatorClaimGrant) bool {
		return grant.SessionID == sessionID
	}) {
		return effectiveClaimsFromGrantFile(root, sessionID, claims, &file), nil
	}
	var effective []string
	err = withActiveSessionLock(root, func() error {
		return mutateClaimGrantFileLocked(root, func(current *claimGrantFile) error {
			effective = effectiveClaimsFromGrantFile(root, sessionID, claims, current)
			return nil
		})
	})
	return effective, err
}

// effectiveClaimsFromGrantFile filters claims and removes confirmed-expired
// grants of this session. A caller that can remove a grant holds the active-
// session lock and publishes the mutation before returning the claims.
func effectiveClaimsFromGrantFile(root, sessionID string, claims []string, file *claimGrantFile) []string {
	revoked := map[string]bool{}
	for _, revocation := range file.Revocations {
		if revocation.SessionID == sessionID {
			revoked[revocation.Claim] = true
		}
	}
	grants := currentSessionOperatorGrants(root, sessionID, file)
	out := make([]string, 0, len(claims))
	for _, claim := range claims {
		normalized := strings.TrimSpace(claim)
		if revoked[normalized] {
			continue
		}
		if isOperatorClaim(normalized) && !grants[normalized] {
			continue
		}
		out = append(out, claim)
	}
	return out
}

func currentSessionOperatorGrants(root, sessionID string, file *claimGrantFile) map[string]bool {
	grants := map[string]bool{}
	var current headBinding
	var currentErr error
	headRead := false
	kept := file.OperatorGrants[:0]
	for _, grant := range file.OperatorGrants {
		if grant.SessionID == sessionID {
			if !headRead {
				current, currentErr = currentHeadBinding(root)
				headRead = true
			}
			binding := headBinding{head: grant.Head, reflogBytes: grant.ReflogBytes, reflogGeneration: grant.ReflogGeneration}
			if currentErr == nil && binding != current {
				continue
			}
			grants[grant.Claim] = currentErr == nil
		}
		kept = append(kept, grant)
	}
	file.OperatorGrants = kept
	return grants
}

// claimsForEvaluation is the claim set every policy decision of a loaded
// session uses.
func claimsForEvaluation(root string, state SessionState) ([]string, error) {
	claims, err := effectiveClaims(root, state.SessionID, state.Claims)
	if err != nil {
		return nil, fmt.Errorf("resolve effective claims: %w", err)
	}
	return claims, nil
}

// currentHeadBinding reads HEAD and the size and generation of its reflog. Git failing to
// answer is an error, never an unborn HEAD, and so is a HEAD commit without a
// reflog, where a return to the same commit would go unnoticed; recording then
// fails and an existing grant is not effective. Git answers a broken branch
// reference or a missing commit exactly like an unborn branch, so an unborn
// HEAD must be confirmed separately.
func currentHeadBinding(root string) (headBinding, error) {
	// HEAD^{commit} also requires the commit object, which plain HEAD does not.
	output, err := gitCommandOutput(root, "rev-parse", "--git-path", "logs/HEAD", "--verify", "-q", "HEAD^{commit}")
	lines := strings.Split(strings.TrimSpace(output), "\n")
	var binding headBinding
	switch {
	case err == nil && len(lines) == 2:
		binding.head = strings.TrimSpace(lines[1])
	case err != nil && len(lines) == 1 && lines[0] != "":
		// --verify -q exits 1 without output while HEAD has no commit.
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return headBinding{}, fmt.Errorf("read git HEAD: %w", err)
		}
		if err := confirmUnbornHead(root); err != nil {
			return headBinding{}, err
		}
	case err != nil:
		return headBinding{}, fmt.Errorf("read git HEAD: %w", err)
	default:
		return headBinding{}, fmt.Errorf("read git HEAD: unexpected rev-parse output %q", output)
	}
	reflog := strings.TrimSpace(lines[0])
	if !filepath.IsAbs(reflog) {
		reflog = filepath.Join(root, reflog)
	}
	info, err := os.Lstat(reflog)
	switch {
	case err == nil && info.Mode().IsRegular():
		generation, reliable := platformFileGeneration(reflog, info)
		if !reliable {
			return headBinding{}, fmt.Errorf("cannot bind git HEAD reflog %s to a reliable filesystem generation", reflog)
		}
		binding.reflogBytes = info.Size()
		binding.reflogGeneration = generation
	case err == nil:
		return headBinding{}, fmt.Errorf("git HEAD reflog %s is not a regular file", reflog)
	case errors.Is(err, fs.ErrNotExist) && binding.head == "":
	case errors.Is(err, fs.ErrNotExist):
		return headBinding{}, fmt.Errorf("no git HEAD reflog in %s, so a return to the granted commit cannot be detected; enable core.logAllRefUpdates", root)
	default:
		return headBinding{}, fmt.Errorf("inspect git HEAD reflog: %w", err)
	}
	return binding, nil
}

// confirmUnbornHead accepts HEAD without a commit only when it is a symbolic
// reference to a branch that does not exist; any diagnostic, such as Git's
// warning about a broken reference, fails the check.
func confirmUnbornHead(root string) error {
	target, err := gitCommandOutput(root, "symbolic-ref", "-q", "HEAD")
	if err != nil {
		return fmt.Errorf("read git HEAD: HEAD names no commit and is not an unborn branch: %w", err)
	}
	target = strings.TrimSpace(target)
	listed, err := gitCommandOutput(root, "for-each-ref", "--format=%(refname)", target)
	if err != nil {
		return fmt.Errorf("read git HEAD: inspect %s: %w", target, err)
	}
	for _, name := range strings.Split(strings.TrimSpace(listed), "\n") {
		if strings.TrimSpace(name) == target {
			return fmt.Errorf("read git HEAD: %s exists but names no readable commit", target)
		}
	}
	return nil
}

// grantRecordedClaim updates the grant file after a claim was recorded: the
// claim is no longer revoked, and an operator claim receives a fresh grant at
// the current HEAD. The caller holds no active-session lock.
func grantRecordedClaim(root, sessionID, claim string) error {
	claim = strings.TrimSpace(claim)
	return withActiveSessionLock(root, func() error {
		return mutateClaimGrantFileLocked(root, func(file *claimGrantFile) error {
			file.Revocations = withoutRevocation(file.Revocations, sessionID, claim)
			if !isOperatorClaim(claim) {
				return nil
			}
			binding, err := currentHeadBinding(root)
			if err != nil {
				return fmt.Errorf("bind operator claim: %w", err)
			}
			file.OperatorGrants = withoutGrant(file.OperatorGrants, sessionID, claim)
			if len(file.OperatorGrants) >= maxOperatorClaimGrants {
				return fmt.Errorf("claim grants exceed %d operator grants", maxOperatorClaimGrants)
			}
			file.OperatorGrants = append(file.OperatorGrants, operatorClaimGrant{
				SessionID: sessionID, Claim: claim, Head: binding.head,
				ReflogBytes: binding.reflogBytes, ReflogGeneration: binding.reflogGeneration,
			})
			return nil
		})
	})
}

// RevokeClaim removes claim from the effective claims of sessionID (the
// active session when empty). Revoking only lowers authority, so it needs no
// operator confirmation. Recording the claim again restores it.
func RevokeClaim(repoRoot, claim, sessionID string) (*ClaimReport, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	claim = strings.TrimSpace(claim)
	if claim == "" {
		return nil, errors.New("claim must be a non-empty string")
	}
	if sessionID == "" {
		active, err := resolveActiveSessionIDResolved(root)
		if err != nil {
			return nil, err
		}
		if active == "" {
			return nil, errors.New("no active reconc session for this repo; pass --session to target one explicitly")
		}
		sessionID = active
	}
	if err := validateSessionID(sessionID); err != nil {
		return nil, err
	}
	err = withActiveSessionLock(root, func() error {
		return mutateClaimGrantFileLocked(root, func(file *claimGrantFile) error {
			file.OperatorGrants = withoutGrant(file.OperatorGrants, sessionID, claim)
			file.Revocations = withoutRevocation(file.Revocations, sessionID, claim)
			if len(file.Revocations) >= maxClaimRevocations {
				return fmt.Errorf("claim grants exceed %d revocations", maxClaimRevocations)
			}
			file.Revocations = append(file.Revocations, claimRevocation{SessionID: sessionID, Claim: claim})
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	state, err := loadSessionStateWithLockResolved(root, sessionID)
	if err != nil {
		return nil, err
	}
	state, err = loadCompleteSessionEvidence(root, state)
	if err != nil {
		return nil, fmt.Errorf("load complete evidence after revoking claim: %w", err)
	}
	effective, err := claimsForEvaluation(root, state)
	if err != nil {
		return nil, err
	}
	return &ClaimReport{
		RepoRoot:   root,
		SessionID:  sessionID,
		Claim:      claim,
		Revoked:    true,
		ClaimCount: len(effective),
		StatePath:  sessionStatePath(root, sessionID),
		ReportPath: state.ReportPath,
	}, nil
}

// dropForeignOperatorGrantsLocked expires every operator grant of a session
// other than the newly active one. The caller holds the active-session lock.
func dropForeignOperatorGrantsLocked(root, activeSessionID string) error {
	return mutateClaimGrantFileLocked(root, func(file *claimGrantFile) error {
		kept := file.OperatorGrants[:0]
		for _, grant := range file.OperatorGrants {
			if grant.SessionID == activeSessionID {
				kept = append(kept, grant)
			}
		}
		file.OperatorGrants = kept
		return nil
	})
}

// dropSessionClaimGrantsLocked forgets the grants and revocations of a session
// whose state is being removed. The caller holds the active-session lock.
func dropSessionClaimGrantsLocked(root, sessionID string) error {
	return mutateClaimGrantFileLocked(root, func(file *claimGrantFile) error {
		grants := file.OperatorGrants[:0]
		for _, grant := range file.OperatorGrants {
			if grant.SessionID != sessionID {
				grants = append(grants, grant)
			}
		}
		file.OperatorGrants = grants
		revocations := file.Revocations[:0]
		for _, revocation := range file.Revocations {
			if revocation.SessionID != sessionID {
				revocations = append(revocations, revocation)
			}
		}
		file.Revocations = revocations
		return nil
	})
}

func withoutGrant(grants []operatorClaimGrant, sessionID, claim string) []operatorClaimGrant {
	out := make([]operatorClaimGrant, 0, len(grants))
	for _, grant := range grants {
		if grant.SessionID != sessionID || grant.Claim != claim {
			out = append(out, grant)
		}
	}
	return out
}

func withoutRevocation(revocations []claimRevocation, sessionID, claim string) []claimRevocation {
	out := make([]claimRevocation, 0, len(revocations))
	for _, revocation := range revocations {
		if revocation.SessionID != sessionID || revocation.Claim != claim {
			out = append(out, revocation)
		}
	}
	return out
}

// mutateClaimGrantFileLocked applies fn to the grant file and writes it only
// when it changed. A missing file starts empty; an unreadable one fails closed.
func mutateClaimGrantFileLocked(root string, fn func(*claimGrantFile) error) error {
	file, err := loadClaimGrantFile(root)
	if err != nil {
		return err
	}
	before, err := json.Marshal(file)
	if err != nil {
		return fmt.Errorf("marshal claim grants: %w", err)
	}
	if err := fn(&file); err != nil {
		return err
	}
	after, err := json.Marshal(file)
	if err != nil {
		return fmt.Errorf("marshal claim grants: %w", err)
	}
	if string(before) == string(after) {
		return nil
	}
	return writeClaimGrantFile(root, file)
}

func loadClaimGrantFile(root string) (claimGrantFile, error) {
	empty := claimGrantFile{FormatVersion: claimGrantFormatVersion, RepoRoot: root, OperatorGrants: []operatorClaimGrant{}, Revocations: []claimRevocation{}}
	body, err := boundedio.ReadRegularFile(claimGrantPath(root), maxClaimGrantBytes)
	if errors.Is(err, os.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return claimGrantFile{}, fmt.Errorf("read claim grants: %w", err)
	}
	var file claimGrantFile
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return claimGrantFile{}, fmt.Errorf("claim grants are not valid JSON (%s): %w", claimGrantPath(root), err)
	}
	if (file.FormatVersion != claimGrantFormatVersion && file.FormatVersion != claimGrantLegacyFormat && file.FormatVersion != claimGrantSizeOnlyFormat) || file.RepoRoot != root ||
		len(file.OperatorGrants) > maxOperatorClaimGrants || len(file.Revocations) > maxClaimRevocations {
		return claimGrantFile{}, fmt.Errorf("claim grants identity is invalid (%s)", claimGrantPath(root))
	}
	for _, grant := range file.OperatorGrants {
		if validateSessionID(grant.SessionID) != nil || !isOperatorClaim(grant.Claim) {
			return claimGrantFile{}, fmt.Errorf("claim grants contain an invalid operator grant (%s)", claimGrantPath(root))
		}
	}
	for _, revocation := range file.Revocations {
		if validateSessionID(revocation.SessionID) != nil || strings.TrimSpace(revocation.Claim) == "" {
			return claimGrantFile{}, fmt.Errorf("claim grants contain an invalid revocation (%s)", claimGrantPath(root))
		}
	}
	if file.OperatorGrants == nil || file.FormatVersion != claimGrantFormatVersion {
		file.OperatorGrants = []operatorClaimGrant{}
	}
	file.FormatVersion = claimGrantFormatVersion
	if file.Revocations == nil {
		file.Revocations = []claimRevocation{}
	}
	return file, nil
}

func writeClaimGrantFile(root string, file claimGrantFile) error {
	file.FormatVersion = claimGrantFormatVersion
	file.RepoRoot = root
	body, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal claim grants: %w", err)
	}
	body = append(body, '\n')
	if len(body) > maxClaimGrantBytes {
		return fmt.Errorf("claim grants exceed %d bytes", maxClaimGrantBytes)
	}
	path := claimGrantPath(root)
	if err := ensurePrivateStateDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("mkdir claim grants dir: %w", err)
	}
	if _, err := atomicfile.WritePrivateIfChanged(path, body, 0o600); err != nil {
		return fmt.Errorf("write claim grants: %w", err)
	}
	return nil
}
