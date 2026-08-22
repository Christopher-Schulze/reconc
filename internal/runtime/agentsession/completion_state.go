package agentsession

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reconc.dev/reconc/internal/assurance"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/commandproof"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/tasklifecycle"
)

func CaptureCompletionState(repoRoot string) (CompletionStateSnapshot, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return CompletionStateSnapshot{}, err
	}
	state := SessionState{
		RepoRoot: root, ReadPaths: []string{}, WritePaths: []string{},
		WriteEpochs: map[string]uint64{}, Commands: []string{}, Claims: []string{},
		CommandResults: []CommandResult{},
	}
	sessionID, err := resolveActiveSessionIDResolved(root)
	if err != nil {
		return CompletionStateSnapshot{}, err
	}
	if sessionID != "" {
		state, err = loadSessionStateWithLockResolved(root, sessionID)
		if err != nil {
			return CompletionStateSnapshot{}, fmt.Errorf("load active session %q: %w", sessionID, err)
		}
		state, err = loadCompleteSessionEvidence(root, state)
		if err != nil {
			return CompletionStateSnapshot{}, fmt.Errorf("load active session %q evidence chain: %w", sessionID, err)
		}
	} else if taint, taintErr := loadEvidenceTaint(root); taintErr != nil {
		return CompletionStateSnapshot{}, taintErr
	} else if taint != nil {
		applyEvidenceTaint(&state, *taint)
	}

	gitSnapshot := completionPolicyGitSnapshotFor(root)
	gitAvailable := gitSnapshot.StatusOK || gitMetadataPresent(root)
	gitIndexHash := ""
	if gitAvailable {
		gitIndexHash, err = gitIndexFingerprint(root)
		if err != nil {
			return CompletionStateSnapshot{}, fmt.Errorf("fingerprint Git index: %w", err)
		}
	}
	taskSnapshot, err := captureStopTaskSnapshot(root)
	if err != nil {
		taskSnapshot = stopTaskSnapshot{State: tasklifecycle.RunState{
			Disposition: tasklifecycle.RunInvalid,
			Blocker:     err.Error(),
		}}
	}
	policyDigest, policyCount, err := stopPolicySourceIdentity(root)
	if err != nil {
		policyDigest = "error:" + err.Error()
		policyCount = 0
	}
	scanCache := &stopPolicyScanCache{}
	fingerprintInput := stopPolicyFingerprintInputForSnapshotWithScan(root, state, gitSnapshot, taskSnapshot, stopGenerationCapture{
		PolicySourceDigest: policyDigest,
		PolicySourceCount:  policyCount,
		TaskStateHash:      stopTaskSnapshotHash(taskSnapshot),
	}, scanCache)
	dirtyFiles := fingerprintInput.GitDirtyFiles
	dirtyPaths := []string{}
	worktreeMatchesIndex := false
	if gitSnapshot.StatusOK {
		dirtyPaths = dirtyPathsFromStatus(gitSnapshot.Status)
		worktreeMatchesIndex = gitWorktreeMatchesIndex(gitSnapshot.Status)
	}
	capturedAt := time.Now().UTC()
	inputs, err := completionExecutionInputs(
		root, state, gitAvailable, gitSnapshot.StatusOK, dirtyPaths, worktreeMatchesIndex, capturedAt,
	)
	if err != nil {
		return CompletionStateSnapshot{}, err
	}
	policyScan := scanCache.get(root, inputs.WritePaths)
	completionPolicyInputs := stopPolicyInputIdentities(root, policyScan.Paths)
	worktreeTrusted := completionDirtyFilesTrusted(dirtyFiles) && completionPolicyInputsTrusted(completionPolicyInputs)
	assuranceInputIdentity := ""
	if len(policyScan.Assurance) > 0 {
		successfulCommands := make([]string, 0, len(inputs.CommandResults))
		for _, result := range inputs.CommandResults {
			if result.Outcome == runtime.CommandOutcomeSuccess {
				successfulCommands = append(successfulCommands, result.Command)
			}
		}
		_, assuranceInputIdentity, err = assurance.EvaluateWithInputIdentity(root, policyScan.Assurance, assurance.Inputs{
			ChangedPaths: inputs.WritePaths, SuccessfulCommands: successfulCommands, Now: capturedAt,
		})
		if err != nil {
			return CompletionStateSnapshot{}, fmt.Errorf("capture native assurance inputs: %w", err)
		}
	}
	freshness := completionFreshnessStates(root, policyScan.FreshFiles, capturedAt)
	worktreeBody, err := json.Marshal(struct {
		Status     string         `json:"status"`
		DirtyFiles []gitDirtyFile `json:"dirty_files"`
	}{Status: gitSnapshot.Status, DirtyFiles: dirtyFiles})
	if err != nil {
		return CompletionStateSnapshot{}, fmt.Errorf("marshal completion worktree identity: %w", err)
	}

	reportHash := ""
	reportTrusted := true
	if sessionID != "" {
		if _, statErr := os.Stat(state.ReportPath); statErr == nil {
			_, reportHash, err = readLatestReport(root, sessionID)
			if err != nil {
				return CompletionStateSnapshot{}, fmt.Errorf("read active session report: %w", err)
			}
			reportTrusted = state.StopPolicyReportHash != "" && state.StopPolicyReportHash == reportHash
		} else if errors.Is(statErr, os.ErrNotExist) {
			reportTrusted = state.StopPolicyReportHash == ""
		} else {
			return CompletionStateSnapshot{}, fmt.Errorf("inspect active session report: %w", statErr)
		}
	}
	completionInput := completionStateFingerprintInput{
		Version:                   "completion-state-v4",
		StopPolicyFingerprint:     fingerprintInput,
		CompletionInputs:          inputs,
		CompletionPolicyInputs:    completionPolicyInputs,
		CompletionFreshness:       freshness,
		AssuranceInputIdentity:    assuranceInputIdentity,
		SessionReportHash:         reportHash,
		ExpectedSessionReportHash: state.StopPolicyReportHash,
		GitIndexHash:              gitIndexHash,
	}
	completionBody, err := json.Marshal(completionInput)
	if err != nil {
		return CompletionStateSnapshot{}, fmt.Errorf("marshal completion state identity: %w", err)
	}
	return CompletionStateSnapshot{
		FormatVersion: "4", RepoRoot: root, Fingerprint: hashBytes(completionBody),
		PolicyLockHash: fingerprintInput.PolicyLockHash,
		SessionID:      sessionID, SessionEvidenceHash: stopPolicyEvidenceHash(state),
		SessionReportHash: reportHash, SessionReportTrusted: reportTrusted,
		EvidenceEpoch:    state.EvidenceEpoch,
		EvidenceOverflow: state.EvidenceOverflow, EvidenceOverflowReason: state.EvidenceOverflowReason,
		EvidenceOverflowLimit: state.EvidenceOverflowLimit,
		GitAvailable:          gitAvailable,
		GitHead:               gitSnapshot.Head, GitIndexHash: gitIndexHash, GitStatusMode: gitSnapshot.StatusMode,
		GitStatusOK: gitSnapshot.StatusOK, GitStatus: gitSnapshot.Status,
		WorktreeHash: hashBytes(worktreeBody), WorktreeTrusted: worktreeTrusted,
		WorktreeMatchesIndex: worktreeMatchesIndex,
		DirtyPaths:           dirtyPaths, Inputs: inputs,
	}, nil
}

func completionExecutionInputs(
	root string,
	state SessionState,
	gitAvailable, gitStatusOK bool,
	dirtyPaths []string,
	worktreeMatchesIndex bool,
	now time.Time,
) (runtime.ExecutionInputs, error) {
	inputs := executionInputs(
		filterRepoScopedReadPaths(root, state.ReadPaths),
		append([]string{}, state.WritePaths...), cloneWriteEpochs(state.WriteEpochs),
		append([]string{}, state.Commands...), append([]CommandResult{}, state.CommandResults...),
		append([]string{}, state.Claims...),
	)
	if !gitAvailable || !gitStatusOK {
		return inputs, nil
	}
	inputs.WritePaths = append([]string{}, dirtyPaths...)
	inputs.WriteEpochs = runtime.RelativizeEpochKeys(root, state.WriteEpochs)
	if inputs.WriteEpochs == nil {
		inputs.WriteEpochs = map[string]uint64{}
	}
	nextEpoch := state.EvidenceEpoch
	if nextEpoch < runtime.ExplicitEvidenceEpoch-1 {
		nextEpoch++
	}
	for _, path := range inputs.WritePaths {
		if inputs.WriteEpochs[path] == 0 {
			inputs.WriteEpochs[path] = nextEpoch
		}
	}
	if len(inputs.WritePaths) == 0 || !worktreeMatchesIndex {
		return inputs, nil
	}
	proofs, err := commandproof.LoadCurrentSuccesses(root, now)
	if err != nil {
		return runtime.ExecutionInputs{}, fmt.Errorf("load current command proofs: %w", err)
	}
	for _, proof := range proofs {
		inputs.Commands = append(inputs.Commands, proof.Command)
		inputs.CommandResults = append(inputs.CommandResults, runtime.CommandResult{
			Command: proof.Command, Outcome: runtime.CommandOutcomeSuccess, EvidenceEpoch: runtime.ExplicitEvidenceEpoch,
		})
	}
	return inputs, nil
}

func completionPolicyInputsTrusted(inputs []policyInputIdentity) bool {
	for _, input := range inputs {
		if !input.Trusted {
			return false
		}
	}
	return true
}

func completionFreshnessStates(root string, files []stopPolicyFreshFile, now time.Time) []completionFreshnessState {
	states := make([]completionFreshnessState, 0, len(files))
	for _, fresh := range files {
		state := completionFreshnessState{Path: fresh.Path, MaxAgeHours: fresh.MaxAgeHours}
		resolved, err := resolveStopPolicyInputPath(root, fresh.Path)
		if err == nil {
			if info, statErr := os.Stat(resolved); statErr == nil && info.Mode().IsRegular() {
				state.Expired = now.Sub(info.ModTime()) > time.Duration(fresh.MaxAgeHours)*time.Hour
			}
		}
		states = append(states, state)
	}
	return states
}

func gitMetadataPresent(repoRoot string) bool {
	for dir := filepath.Clean(repoRoot); ; dir = filepath.Dir(dir) {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil || !errors.Is(err, os.ErrNotExist) {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
	}
}

func gitIndexFingerprint(repoRoot string) (string, error) {
	entries, err := gitCommandOutput(repoRoot, "ls-files", "-s", "-z")
	if err != nil {
		return "", fmt.Errorf("git ls-files --stage: %w", err)
	}
	return hashBytes([]byte(entries)), nil
}

func gitWorktreeMatchesIndex(status string) bool {
	records := strings.Split(status, "\x00")
	for index := 0; index < len(records); index++ {
		record := records[index]
		if record == "" {
			continue
		}
		if len(record) < 4 || record[2] != ' ' {
			return false
		}
		if record[0] == '?' || record[1] == '?' || record[1] != ' ' {
			return false
		}
		if isRenameOrCopyStatus(record[0], record[1]) && index+1 < len(records) {
			index++
		}
	}
	return true
}

func readLatestReport(repoRoot, sessionID string) (*runtime.CheckReport, string, error) {
	path := sessionReportPath(repoRoot, sessionID)
	body, err := boundedio.ReadRegularFile(path, maxStopReportBytes)
	if err != nil {
		return nil, "", err
	}
	var report runtime.CheckReport
	if err := json.Unmarshal(body, &report); err != nil {
		return nil, "", fmt.Errorf("unmarshal report %s: %w", path, err)
	}
	return &report, hashCheckReport(&report), nil
}

func hashCheckReport(report *runtime.CheckReport) string {
	if report == nil {
		return ""
	}
	body, err := json.Marshal(report)
	if err != nil {
		return ""
	}
	return hashBytes(body)
}

func hashBlockingViolations(violations []runtime.Violation) string {
	body, err := json.Marshal(violations)
	if err != nil {
		return ""
	}
	return hashBytes(body)
}

func hashBytes(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func fileContentHash(path string) string {
	hash, err := hashFileContent(path)
	if err != nil {
		return "error:" + err.Error()
	}
	return hash
}
