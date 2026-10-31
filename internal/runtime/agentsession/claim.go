package agentsession

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ClaimReport is the outcome of appending one claim to the active
// session. Returned by RecordClaim so the CLI can render a JSON or
// text confirmation.
type ClaimReport struct {
	RepoRoot   string `json:"repo_root"`
	SessionID  string `json:"session_id"`
	Claim      string `json:"claim"`
	Revoked    bool   `json:"revoked,omitempty"`
	ExpiresAt  string `json:"expires_at,omitempty"`
	ClaimCount int    `json:"claim_count"`
	StatePath  string `json:"state_path"`
	ReportPath string `json:"report_path"`
}

// RecordClaim appends one explicit claim to the session state. If
// sessionID is empty it defaults to the active session. Errors when
// no active session exists, when the claim is empty, or when the
// underlying state file can't be written.
//
// Re-running RecordClaim for the same claim is idempotent (dedup in
// AppendClaim). An operator claim recorded here is bound to the current HEAD.
func RecordClaim(repoRoot, claim, sessionID string) (*ClaimReport, error) {
	return recordClaim(repoRoot, claim, sessionID, time.Time{})
}

// RecordOperatorClaimFor records an operator claim that stays effective for
// duration after now, across commits, until it expires, is revoked, another
// session becomes active, or the session is cleaned up.
func RecordOperatorClaimFor(repoRoot, claim, sessionID string, duration time.Duration, now time.Time) (*ClaimReport, error) {
	if err := ValidateTimedOperatorClaim(claim, duration); err != nil {
		return nil, err
	}
	return recordClaim(repoRoot, claim, sessionID, now.Add(duration))
}

// ValidateTimedOperatorClaim reports whether claim and duration form a valid
// timed operator claim, so callers can refuse before asking for confirmation.
func ValidateTimedOperatorClaim(claim string, duration time.Duration) error {
	if !isOperatorClaim(claim) {
		return fmt.Errorf("a claim duration applies only to %s* claims", OperatorClaimPrefix)
	}
	if duration < minTimedOperatorClaim || duration > MaxTimedOperatorClaim {
		return fmt.Errorf("claim duration must be between %s and %s", minTimedOperatorClaim, MaxTimedOperatorClaim)
	}
	return nil
}

func recordClaim(repoRoot, claim, sessionID string, expiresAt time.Time) (*ClaimReport, error) {
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
	current, err := loadSessionStateWithLockResolved(root, sessionID)
	if err != nil {
		return nil, err
	}
	if current.EvidenceOverflow {
		return nil, errors.New(evidenceOverflowMessage(current))
	}

	updated, err := mutateSessionStateResolved(root, sessionID, func(state SessionState) SessionState {
		if state.EvidenceOverflow {
			return state
		}
		return AppendClaim(state, claim)
	})
	if err != nil {
		return nil, err
	}
	if updated.EvidenceOverflow {
		return nil, errors.New(evidenceOverflowMessage(updated))
	}
	if err := grantRecordedClaim(root, sessionID, claim, expiresAt); err != nil {
		return nil, fmt.Errorf("record claim grant: %w", err)
	}
	updated, err = loadCompleteSessionEvidence(root, updated)
	if err != nil {
		return nil, fmt.Errorf("load complete evidence after recording claim: %w", err)
	}
	// Recording a claim only persists it. Every gate that consults claims
	// (PreToolUse, Stop, completion, CI) evaluates the current claim set
	// itself, so re-running the full policy here would only repeat that work
	// and execute every applicable script on the claim path.

	report := &ClaimReport{
		RepoRoot:   root,
		SessionID:  sessionID,
		Claim:      claim,
		ClaimCount: len(updated.Claims),
		StatePath:  sessionStatePath(root, sessionID),
		ReportPath: updated.ReportPath,
	}
	if !expiresAt.IsZero() {
		report.ExpiresAt = expiresAt.UTC().Format(time.RFC3339)
	}
	return report, nil
}

// ActiveEvidence returns read, write, command, and claim evidence recorded on the
// currently active agent session. Missing session state is not an error;
// callers use this to let non-interactive gates such as git pre-commit inherit
// in-session context, authorizations, and successful checks.
type ActiveEvidenceSnapshot struct {
	ReadPaths              []string
	WritePaths             []string
	WriteEpochs            map[string]uint64
	EvidenceEpoch          uint64
	Commands               []string
	CommandResults         []CommandResult
	Claims                 []string
	EvidenceOverflow       bool
	EvidenceOverflowReason string
	EvidenceOverflowLimit  string
}

func ActiveEvidence(repoRoot string) (ActiveEvidenceSnapshot, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return ActiveEvidenceSnapshot{}, err
	}
	sessionID, err := resolveActiveSessionIDResolved(root)
	if err != nil {
		return ActiveEvidenceSnapshot{}, err
	}
	if sessionID == "" {
		taint, taintErr := loadEvidenceTaint(root)
		if taintErr != nil {
			return ActiveEvidenceSnapshot{}, taintErr
		}
		if taint == nil {
			return ActiveEvidenceSnapshot{}, nil
		}
		state := emptyState(root, taint.SessionID)
		applyEvidenceTaint(&state, *taint)
		return ActiveEvidenceSnapshot{}, errors.New(evidenceOverflowMessage(state))
	}
	state, err := loadSessionStateWithLockResolved(root, sessionID)
	if err != nil {
		return ActiveEvidenceSnapshot{}, fmt.Errorf("load active session %q: %w", sessionID, err)
	}
	if state.EvidenceOverflow {
		return ActiveEvidenceSnapshot{}, errors.New(evidenceOverflowMessage(state))
	}
	state, err = loadCompleteSessionEvidence(root, state)
	if err != nil {
		return ActiveEvidenceSnapshot{}, fmt.Errorf("load active session %q evidence chain: %w", sessionID, err)
	}
	claims, err := claimsForEvaluation(root, state)
	if err != nil {
		return ActiveEvidenceSnapshot{}, fmt.Errorf("active session %q: %w", sessionID, err)
	}
	return ActiveEvidenceSnapshot{
		ReadPaths:              append([]string{}, state.ReadPaths...),
		WritePaths:             append([]string{}, state.WritePaths...),
		WriteEpochs:            cloneWriteEpochs(state.WriteEpochs),
		EvidenceEpoch:          state.EvidenceEpoch,
		Commands:               append([]string{}, state.Commands...),
		CommandResults:         append([]CommandResult{}, state.CommandResults...),
		Claims:                 append([]string{}, claims...),
		EvidenceOverflow:       state.EvidenceOverflow,
		EvidenceOverflowReason: state.EvidenceOverflowReason,
		EvidenceOverflowLimit:  state.EvidenceOverflowLimit,
	}, nil
}

func cloneWriteEpochs(values map[string]uint64) map[string]uint64 {
	out := make(map[string]uint64, len(values))
	for path, epoch := range values {
		out[path] = epoch
	}
	return out
}

// ActiveClaims returns claims recorded on the currently active agent
// session. Kept as a narrow helper for callers that need only claims.
func ActiveClaims(repoRoot string) ([]string, error) {
	evidence, err := ActiveEvidence(repoRoot)
	if err != nil {
		return nil, err
	}
	return evidence.Claims, nil
}

// DescribeClaimReport returns a short human-readable rendering of
// the ClaimReport. Used by the CLI when --json is not set.
func DescribeClaimReport(r *ClaimReport) string {
	if r.Revoked {
		return fmt.Sprintf("claim '%s' revoked for session %s (effective claims: %d)\n  state:  %s\n  report: %s",
			r.Claim, r.SessionID, r.ClaimCount, r.StatePath, r.ReportPath)
	}
	if r.ExpiresAt != "" {
		return fmt.Sprintf("claim '%s' recorded for session %s until %s (total claims: %d)\n  state:  %s\n  report: %s",
			r.Claim, r.SessionID, r.ExpiresAt, r.ClaimCount, r.StatePath, r.ReportPath)
	}
	return fmt.Sprintf("claim '%s' recorded for session %s (total claims: %d)\n  state:  %s\n  report: %s",
		r.Claim, r.SessionID, r.ClaimCount, r.StatePath, r.ReportPath)
}
