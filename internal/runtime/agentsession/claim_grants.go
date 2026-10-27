package agentsession

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/boundedio"
)

// An operator claim authorizes the changes up to the next commit. Its grant records the
// session that recorded it and the Git HEAD at that moment; the claim is
// effective only while HEAD is unchanged and the recording session stayed the
// active session. A commit, amend, reset, checkout or rebase moves HEAD and
// expires it, and a switch of the active session or the session's cleanup
// drops it. Revocations remove any claim, operator or agent, from a session's
// effective claims. Grants and revocations live in one private per-project
// file outside the sealed evidence chain, so reloading sealed evidence cannot
// bring an expired or revoked claim back; the evidence keeps recording that the
// claim was made.
const (
	claimGrantFormatVersion = "claim-grants-v1"
	claimGrantFileName      = "claim-grants.json"
	maxClaimGrantBytes      = 256 * 1024
	maxOperatorClaimGrants  = 256
	maxClaimRevocations     = 1024
)

type operatorClaimGrant struct {
	SessionID string `json:"session_id"`
	Claim     string `json:"claim"`
	Head      string `json:"head"`
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
	revoked := map[string]bool{}
	for _, revocation := range file.Revocations {
		if revocation.SessionID == sessionID {
			revoked[revocation.Claim] = true
		}
	}
	grants := map[string]string{}
	for _, grant := range file.OperatorGrants {
		if grant.SessionID == sessionID {
			grants[grant.Claim] = grant.Head
		}
	}
	head, headRead := "", false
	out := make([]string, 0, len(claims))
	for _, claim := range claims {
		normalized := strings.TrimSpace(claim)
		if revoked[normalized] {
			continue
		}
		if isOperatorClaim(normalized) {
			grantHead, granted := grants[normalized]
			if !granted {
				continue
			}
			if !headRead {
				head, headRead = currentGitHead(root), true
			}
			if grantHead != head {
				continue
			}
		}
		out = append(out, claim)
	}
	return out, nil
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

// currentGitHead returns the commit HEAD names, or "" when the repository has
// no commit yet or Git cannot answer. A grant and its later check read HEAD the
// same way, so an unreadable HEAD can only expire a grant, never extend it.
func currentGitHead(root string) string {
	head, err := gitCommandOutput(root, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(head)
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
			file.OperatorGrants = withoutGrant(file.OperatorGrants, sessionID, claim)
			if len(file.OperatorGrants) >= maxOperatorClaimGrants {
				return fmt.Errorf("claim grants exceed %d operator grants", maxOperatorClaimGrants)
			}
			file.OperatorGrants = append(file.OperatorGrants, operatorClaimGrant{SessionID: sessionID, Claim: claim, Head: currentGitHead(root)})
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
	if file.FormatVersion != claimGrantFormatVersion || file.RepoRoot != root ||
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
	if file.OperatorGrants == nil {
		file.OperatorGrants = []operatorClaimGrant{}
	}
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
