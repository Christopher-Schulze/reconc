package agentsession

import (
	"errors"
	"fmt"
	"strings"
)

// ClaimReport is the outcome of appending one claim to the active
// session. Returned by RecordClaim so the CLI can render a JSON or
// text confirmation.
type ClaimReport struct {
	RepoRoot   string `json:"repo_root"`
	SessionID  string `json:"session_id"`
	Claim      string `json:"claim"`
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
// AppendClaim).
func RecordClaim(repoRoot, claim, sessionID string) (*ClaimReport, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	claim = strings.TrimSpace(claim)
	if claim == "" {
		return nil, errors.New("claim must be a non-empty string")
	}

	if sessionID == "" {
		active, err := ResolveActiveSessionID(root)
		if err != nil {
			return nil, err
		}
		if active == "" {
			return nil, errors.New("no active reconc session for this repo; pass --session to target one explicitly")
		}
		sessionID = active
	}

	updated, err := MutateSessionState(root, sessionID, func(state SessionState) SessionState {
		return AppendClaim(state, claim)
	})
	if err != nil {
		return nil, err
	}
	// Re-run check so the saved report reflects the new claim set.
	// Ignore check errors here -- the claim record is the primary
	// artefact; the report refresh is a courtesy for later inspection.
	_, _ = runCheckAndSave(root, sessionID, updated.ReadPaths,
		updated.WritePaths, updated.Commands, updated.CommandResults, updated.Claims)

	return &ClaimReport{
		RepoRoot:   root,
		SessionID:  sessionID,
		Claim:      claim,
		ClaimCount: len(updated.Claims),
		StatePath:  sessionStatePath(root, sessionID),
		ReportPath: updated.ReportPath,
	}, nil
}

// ActiveEvidence returns command and claim evidence recorded on the
// currently active agent session. Missing session state is not an
// error; callers use this to let non-interactive gates such as git
// pre-commit inherit in-session authorizations and successful checks
// without requiring PATH-dependent wrappers.
func ActiveEvidence(repoRoot string) ([]string, []CommandResult, []string, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return nil, nil, nil, err
	}
	sessionID, err := ResolveActiveSessionID(root)
	if err != nil {
		return nil, nil, nil, err
	}
	if sessionID == "" {
		return nil, nil, nil, nil
	}
	state, err := LoadSessionState(root, sessionID)
	if err != nil {
		return nil, nil, nil, nil
	}
	commands := append([]string{}, state.Commands...)
	results := append([]CommandResult{}, state.CommandResults...)
	claims := append([]string{}, state.Claims...)
	return commands, results, claims, nil
}

// ActiveClaims returns claims recorded on the currently active agent
// session. Kept as a narrow helper for callers that need only claims.
func ActiveClaims(repoRoot string) ([]string, error) {
	_, _, claims, err := ActiveEvidence(repoRoot)
	if err != nil {
		return nil, err
	}
	return claims, nil
}

// DescribeClaimReport returns a short human-readable rendering of
// the ClaimReport. Used by the CLI when --json is not set.
func DescribeClaimReport(r *ClaimReport) string {
	return fmt.Sprintf("claim '%s' recorded for session %s (total claims: %d)\n  state:  %s\n  report: %s",
		r.Claim, r.SessionID, r.ClaimCount, r.StatePath, r.ReportPath)
}
