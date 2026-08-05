package cli

import (
	"fmt"
	"time"

	"reconc.dev/reconc/internal/commandproof"
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
	if err := addStagedProofs(discovery.RepoRoot, staged, &result.inputs); err != nil {
		return result, 1, err
	}
	result.candidate, err = agentsession.CaptureCompletionState(discovery.RepoRoot)
	if err != nil {
		return result, 1, fmt.Errorf("capture candidate: %w", err)
	}
	if result.candidate.EvidenceOverflow {
		return result, 2, evidenceOverflowError(result.candidate)
	}
	active, err := agentsession.ActiveEvidence(discovery.RepoRoot)
	if err != nil {
		return result, 1, fmt.Errorf("active evidence: %w", err)
	}
	mergeCIActiveEvidence(staged, active, &result.inputs)
	result.git, err = addCIGitEvidence(discovery.RepoRoot, staged, base, head, active, &result.inputs)
	return result, 1, err
}

func addStagedProofs(repoRoot string, staged bool, inputs *runtime.ExecutionInputs) error {
	if !staged {
		return nil
	}
	proofs, err := commandproof.LoadCurrentSuccesses(repoRoot, time.Now())
	if err != nil {
		return fmt.Errorf("load staged command proofs: %w", err)
	}
	for _, proof := range proofs {
		inputs.Commands = append(inputs.Commands, proof.Command)
		inputs.CommandResults = append(inputs.CommandResults, runtime.CommandResult{
			Command: proof.Command, Outcome: runtime.CommandOutcomeSuccess,
			EvidenceEpoch: runtime.ExplicitEvidenceEpoch,
		})
	}
	return nil
}

func evidenceOverflowError(candidate agentsession.CompletionStateSnapshot) error {
	detail := candidate.EvidenceOverflowReason
	if candidate.EvidenceOverflowLimit != "" {
		detail += "/" + candidate.EvidenceOverflowLimit
	}
	return fmt.Errorf("persisted evidence is uncertified at %s", detail)
}

func mergeCIActiveEvidence(staged bool, active agentsession.ActiveEvidenceSnapshot, inputs *runtime.ExecutionInputs) {
	inputs.ReadPaths = append(inputs.ReadPaths, active.ReadPaths...)
	inputs.Commands = append(inputs.Commands, active.Commands...)
	if !staged {
		for _, result := range active.CommandResults {
			inputs.CommandResults = append(inputs.CommandResults, runtime.CommandResult{
				Command: result.Command, Outcome: result.Outcome, EvidenceEpoch: result.EvidenceEpoch,
			})
		}
	}
	inputs.Claims = append(inputs.Claims, active.Claims...)
}

func addCIGitEvidence(repoRoot string, staged bool, base, head string, active agentsession.ActiveEvidenceSnapshot, inputs *runtime.ExecutionInputs) (runtime.GitDiffMetadata, error) {
	paths, metadata, err := runtime.CollectGitWritePaths(repoRoot, staged, base, head)
	if err != nil {
		return runtime.GitDiffMetadata{}, err
	}
	inputs.WritePaths = append(inputs.WritePaths, paths...)
	if inputs.WriteEpochs == nil {
		inputs.WriteEpochs = runtime.RelativizeEpochKeys(repoRoot, active.WriteEpochs)
	}
	if inputs.WriteEpochs == nil {
		inputs.WriteEpochs = map[string]uint64{}
	}
	epoch := active.EvidenceEpoch
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
