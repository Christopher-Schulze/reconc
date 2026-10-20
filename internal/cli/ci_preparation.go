package cli

import (
	"fmt"

	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

type ciPreparation struct {
	candidate agentsession.CompletionStateSnapshot
	inputs    runtime.ExecutionInputs
	git       runtime.GitDiffMetadata
}

func prepareCIEvaluation(repo string, staged bool, base, head string, inputs runtime.ExecutionInputs) (ciPreparation, int, error) {
	result := ciPreparation{inputs: inputs}
	discovery, err := ingest.DiscoverPolicyRepo(repo)
	if err != nil {
		return result, 1, err
	}
	if !discovery.Discovered {
		return result, 1, fmt.Errorf("no policy markers found")
	}
	result.candidate, err = agentsession.CaptureCompletionState(discovery.RepoRoot)
	if err != nil {
		return result, 1, fmt.Errorf("capture candidate: %w", err)
	}
	if result.candidate.EvidenceOverflow {
		return result, 2, evidenceOverflowError(result.candidate)
	}
	if staged && (!result.candidate.GitAvailable || !result.candidate.GitStatusOK || !result.candidate.WorktreeMatchesIndex) {
		return result, 2, fmt.Errorf("staged candidate requires readable Git status and a worktree matching the index; prepare a staged-only worktree with no unstaged or untracked changes, then rerun reconc ci --staged")
	}
	// The candidate already contains its session evidence and current staged
	// proofs. Loading either separately can pair an earlier proof with a newer
	// index while both completion barriers observe only the newer candidate.
	mergeCIActiveEvidence(staged, result.candidate.Inputs, &result.inputs)
	result.git, err = addCIGitEvidence(result.candidate, staged, base, head, &result.inputs)
	return result, 1, err
}

func evidenceOverflowError(candidate agentsession.CompletionStateSnapshot) error {
	detail := candidate.EvidenceOverflowReason
	if candidate.EvidenceOverflowLimit != "" {
		detail += "/" + candidate.EvidenceOverflowLimit
	}
	return fmt.Errorf("persisted evidence is uncertified at %s", detail)
}

func mergeCIActiveEvidence(staged bool, active runtime.ExecutionInputs, inputs *runtime.ExecutionInputs) {
	inputs.ReadPaths = append(inputs.ReadPaths, active.ReadPaths...)
	inputs.Commands = append(inputs.Commands, active.Commands...)
	for _, result := range active.CommandResults {
		// Only successes admitted by the candidate's index-bound proof loader
		// use ExplicitEvidenceEpoch. Ordinary tool history cannot certify the index.
		if !staged || result.EvidenceEpoch == runtime.ExplicitEvidenceEpoch && result.Outcome == runtime.CommandOutcomeSuccess {
			inputs.CommandResults = append(inputs.CommandResults, result)
		}
	}
	inputs.Claims = append(inputs.Claims, active.Claims...)
}

// stagedCandidateGitCommand names the snapshot staged write paths come from.
const stagedCandidateGitCommand = "git status --porcelain=v1 -z --ignore-submodules=none --untracked-files=all (fingerprinted candidate)"

func addCIGitEvidence(candidate agentsession.CompletionStateSnapshot, staged bool, base, head string, inputs *runtime.ExecutionInputs) (runtime.GitDiffMetadata, error) {
	var paths []string
	var metadata runtime.GitDiffMetadata
	if staged && (base != "" || head != "") {
		return runtime.GitDiffMetadata{}, &rerrors.GitError{Message: "specify either --staged OR --base/--head, not both"}
	}
	if staged {
		// The candidate's worktree matches its index, so its dirty paths are
		// exactly the staged changes, read from the same status snapshot the
		// fingerprint binds. A second Git call could observe another index.
		paths = append([]string{}, candidate.DirtyPaths...)
		metadata = runtime.GitDiffMetadata{Mode: runtime.GitModeStaged, GitCommand: stagedCandidateGitCommand, WritePathCount: len(paths)}
	} else {
		var err error
		paths, metadata, err = runtime.CollectGitWritePaths(candidate.RepoRoot, staged, base, head)
		if err != nil {
			return runtime.GitDiffMetadata{}, err
		}
	}
	inputs.WritePaths = append(inputs.WritePaths, paths...)
	if inputs.WriteEpochs == nil {
		inputs.WriteEpochs = runtime.RelativizeEpochKeys(candidate.RepoRoot, candidate.Inputs.WriteEpochs)
	}
	if inputs.WriteEpochs == nil {
		inputs.WriteEpochs = map[string]uint64{}
	}
	epoch := candidate.EvidenceEpoch
	if epoch < runtime.ExplicitEvidenceEpoch-1 {
		epoch++
	}
	for _, path := range paths {
		if inputs.WriteEpochs[path] == 0 {
			inputs.WriteEpochs[path] = epoch
		}
	}
	return metadata, nil
}
