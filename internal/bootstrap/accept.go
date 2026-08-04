package bootstrap

import (
	"bytes"
	"fmt"
	"sort"
)

// ManagedAcceptanceReport is the bounded result of explicitly promoting pure
// marker-only candidate changes. Non-managed conflicts remain untouched.
type ManagedAcceptanceReport struct {
	Updated           []string `json:"updated"`
	RemovedCandidates []string `json:"removed_candidates"`
	Remaining         []string `json:"remaining"`
}

// HasManagedCandidates reports whether a plan contains at least one conflict
// whose component owns a marker-delimited block.
func HasManagedCandidates(plan *Plan) bool {
	if plan == nil {
		return false
	}
	for _, action := range plan.Actions {
		start, _ := managedMarkersForAction(action)
		if action.State == ActionConflict && start != "" {
			return true
		}
	}
	return false
}

// AcceptManagedCandidates promotes only candidates that are byte-for-byte the
// current user file plus one recognized Reconc managed block. It revalidates
// target and candidate identity before an atomic, rollback-capable update.
//
// The mutation runs under the repository transaction lock so concurrent
// bootstrap operations serialize. Callers that already hold the lock (such as
// initializeLocked) must call acceptManagedCandidatesLocked instead, because
// the lock is not reentrant.
func AcceptManagedCandidates(plan *Plan, report *Report) (*ManagedAcceptanceReport, error) {
	result := &ManagedAcceptanceReport{Updated: []string{}, RemovedCandidates: []string{}, Remaining: []string{}}
	if err := ValidatePlan(plan); err != nil {
		return result, err
	}
	var acceptErr error
	err := withRepositoryTransactionLock(plan.RepoRoot, func() error {
		result, acceptErr = acceptManagedCandidatesLocked(plan, report)
		return acceptErr
	})
	if err != nil {
		return result, err
	}
	return result, acceptErr
}

// acceptManagedCandidatesLocked is the lock-free core; the caller must hold
// the repository transaction lock.
func acceptManagedCandidatesLocked(plan *Plan, report *Report) (*ManagedAcceptanceReport, error) {
	result := &ManagedAcceptanceReport{Updated: []string{}, RemovedCandidates: []string{}, Remaining: []string{}}
	if err := ValidatePlan(plan); err != nil {
		return result, err
	}
	if report == nil || report.PlanDigest != plan.PlanDigest || report.RepoRoot != plan.RepoRoot || report.Status != ApplyDrift {
		return result, fmt.Errorf("managed candidate acceptance requires the matching drift report")
	}
	reported := map[string]bool{}
	for _, candidate := range report.Candidates {
		reported[candidate] = true
	}
	mutations := []removalMutation{}
	accepted := 0
	for _, action := range plan.Actions {
		if action.State != ActionConflict {
			continue
		}
		start, end := managedMarkersForAction(action)
		if start == "" || !reported[action.CandidatePath] {
			result.Remaining = append(result.Remaining, action.Path)
			continue
		}
		target, err := safeBootstrapTarget(plan.RepoRoot, action.Path)
		if err != nil {
			return result, err
		}
		candidate, err := safeBootstrapTarget(plan.RepoRoot, action.CandidatePath)
		if err != nil {
			return result, err
		}
		current, currentMode, err := readRemovalFile(target, maxBinaryBytes)
		if err != nil {
			return result, fmt.Errorf("read managed acceptance target %s: %w", action.Path, err)
		}
		if bytesSHA256(current) != action.ExistingSHA256 || !modeSatisfies(currentMode, action.Mode) {
			return result, fmt.Errorf("managed acceptance target drifted since planning: %s", action.Path)
		}
		desired, desiredMode, err := readRemovalFile(candidate, maxBinaryBytes)
		if err != nil {
			return result, fmt.Errorf("read managed acceptance candidate %s: %w", action.CandidatePath, err)
		}
		if bytesSHA256(desired) != action.DesiredSHA256 || !modeSatisfies(desiredMode, action.Mode) {
			return result, fmt.Errorf("managed acceptance candidate is not plan-exact: %s", action.CandidatePath)
		}
		stripped, found, err := stripReceiptManagedBlock(string(desired), start, end)
		if err != nil {
			return result, fmt.Errorf("inspect managed candidate %s: %w", action.CandidatePath, err)
		}
		pureBase := string(current)
		pureAppendBase := pureBase + managedAppendSeparator(pureBase)
		if !found || (stripped != pureBase && stripped != pureAppendBase) {
			return result, fmt.Errorf("candidate %s changes content outside its Reconc managed block", action.CandidatePath)
		}
		mutations = append(mutations,
			removalMutation{relative: action.Path, path: target, before: current, after: desired, mode: currentMode},
			removalMutation{relative: action.CandidatePath, path: candidate, before: desired, mode: desiredMode, remove: true},
		)
		accepted++
	}
	if accepted == 0 {
		return result, fmt.Errorf("drift report has no pure marker-only managed candidate")
	}
	removed, updated, rolledBack, err := applyRemovalTransaction(plan.RepoRoot, mutations)
	if err != nil {
		return result, fmt.Errorf("accept managed bootstrap candidates (rolled back %v): %w", rolledBack, err)
	}
	result.Updated = updated
	result.RemovedCandidates = removed
	sort.Strings(result.Updated)
	sort.Strings(result.RemovedCandidates)
	sort.Strings(result.Remaining)
	return result, nil
}

func managedAppendSeparator(existing string) string {
	if existing == "" {
		return ""
	}
	if bytes.HasSuffix([]byte(existing), []byte("\n")) {
		return "\n"
	}
	return "\n\n"
}
