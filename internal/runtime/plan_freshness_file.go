package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/boundedio"
)

const (
	planFreshnessRecordVersion  = 1
	maxPlanFreshnessRecordBytes = 64 << 10
)

// planFreshnessRecord persists, across one-shot processes, that the lockfile
// with LockSHA256 was fully validated against policy sources whose content
// freshness under IncludePatterns was FreshnessSHA256. A cold evaluator that
// observes the same lockfile and the same freshness compiles the plan without
// re-reading and re-digesting the sources, exactly as a warm evaluator reuses
// its cached plan. The freshness observation still hashes every source file,
// so any content change falls back to the full validation.
type planFreshnessRecord struct {
	FormatVersion   int      `json:"format_version"`
	Root            string   `json:"root"`
	LockSHA256      string   `json:"lock_sha256"`
	FreshnessSHA256 string   `json:"freshness_sha256"`
	IncludePatterns []string `json:"include_patterns"`
}

// NewEvaluatorWithPlanFreshnessFile returns an evaluator that persists
// validated plan freshness at the private path locate returns for a root. It
// serves one-shot hook processes, whose in-memory plan cache never survives.
func NewEvaluatorWithPlanFreshnessFile(locate func(root string) string) *Evaluator {
	evaluator := NewEvaluator()
	evaluator.planFreshnessFile = locate
	return evaluator
}

// planFromPersistedFreshness compiles the plan for a lockfile whose
// validation is persisted and whose sources still have the recorded content.
// Every mismatch, unreadable record, or compile failure is a miss that leaves
// the full validation to report it; only caller cancellation is returned.
func (e *Evaluator) planFromPersistedFreshness(ctx context.Context, root string, data []byte, lockHash [sha256.Size]byte) (*runtimePlan, [sha256.Size]byte, bool, error) {
	var none [sha256.Size]byte
	record, ok := e.readPlanFreshness(root, lockHash)
	if !ok {
		return nil, none, false, nil
	}
	lock, err := decodeLockfile(data)
	if err != nil {
		return nil, none, false, nil
	}
	lock.byteHash = lockHash
	plan, err := compileRuntimePlanFromLockForRoot(lock, root)
	if err != nil {
		return nil, none, false, nil
	}
	plan.sourceFreshness, err = newSourceFreshnessRecipe(root, record.IncludePatterns)
	if err != nil {
		return nil, none, false, nil
	}
	freshness, err := observeRuntimeSourceFreshnessWithStatsContext(ctx, root, plan, e.loadStats)
	if runtimePlanCallerAbort(err) {
		return nil, none, false, err
	}
	if err != nil || hex.EncodeToString(freshness[:]) != record.FreshnessSHA256 {
		return nil, none, false, nil
	}
	return plan, freshness, true, nil
}

func (e *Evaluator) readPlanFreshness(root string, lockHash [sha256.Size]byte) (planFreshnessRecord, bool) {
	path := e.planFreshnessPath(root)
	if path == "" {
		return planFreshnessRecord{}, false
	}
	body, err := boundedio.ReadRegularFile(path, maxPlanFreshnessRecordBytes)
	if err != nil {
		return planFreshnessRecord{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var record planFreshnessRecord
	if err := decoder.Decode(&record); err != nil || record.FormatVersion != planFreshnessRecordVersion ||
		record.Root != root || record.LockSHA256 != hex.EncodeToString(lockHash[:]) ||
		len(record.FreshnessSHA256) != 2*sha256.Size || len(record.IncludePatterns) == 0 {
		return planFreshnessRecord{}, false
	}
	return record, true
}

// storePlanFreshness records a full validation. The record is a disposable
// cache: a lost or torn write only costs the next process a full validation.
// It is written only into an existing directory, which the private state
// owner created with its platform security; this package creates none.
func (e *Evaluator) storePlanFreshness(root string, lockHash, freshness [sha256.Size]byte, patterns []string) {
	path := e.planFreshnessPath(root)
	if path == "" {
		return
	}
	if parent, err := os.Lstat(filepath.Dir(path)); err != nil || !parent.IsDir() {
		return
	}
	body, err := json.MarshalIndent(planFreshnessRecord{
		FormatVersion: planFreshnessRecordVersion, Root: root,
		LockSHA256:      hex.EncodeToString(lockHash[:]),
		FreshnessSHA256: hex.EncodeToString(freshness[:]),
		IncludePatterns: patterns,
	}, "", "  ")
	if err != nil || len(body) >= maxPlanFreshnessRecordBytes {
		return
	}
	// A failed write only costs the next process a full validation.
	_, _ = atomicfile.WritePrivateCacheIfChanged(path, append(body, '\n'), 0o600)
}

func (e *Evaluator) planFreshnessPath(root string) string {
	if e == nil || e.planFreshnessFile == nil {
		return ""
	}
	return e.planFreshnessFile(root)
}
