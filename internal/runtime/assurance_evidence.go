package runtime

import "reconc.dev/reconc/internal/assurance"

// SuccessfulAssuranceCommandEvidence shares normalized causal evidence between
// runtime evaluation and completion input capture.
func SuccessfulAssuranceCommandEvidence(results []CommandResult, repoRoot string) []assurance.CommandEvidence {
	return successfulAssuranceEvidence(newCommandEvidenceIndex(ExecutionInputs{CommandResults: results}, repoRoot))
}

func successfulAssuranceEvidence(index *commandEvidenceIndex) []assurance.CommandEvidence {
	evidence := make([]assurance.CommandEvidence, 0, len(index.results))
	for _, result := range index.results {
		if result.outcome != CommandOutcomeSuccess {
			continue
		}
		evidence = append(evidence, assurance.CommandEvidence{Command: result.raw, EvidenceEpoch: result.epoch})
		if result.normalized != result.raw {
			evidence = append(evidence, assurance.CommandEvidence{Command: result.normalized, EvidenceEpoch: result.epoch})
		}
	}
	return evidence
}
