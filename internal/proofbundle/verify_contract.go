package proofbundle

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"reconc.dev/reconc/internal/completiongate"
)

func verifyBundleIdentity(bundle *Bundle) error {
	if !validBuildIdentity(bundle.Build) {
		return invalidProof("build identity is incomplete")
	}
	if bundle.Build.SourceDigest != "" && bundle.Build.SourceDigest != "unavailable" && !validDigest(bundle.Build.SourceDigest) {
		return invalidProof("build source digest is invalid")
	}
	if !validDigest(bundle.Candidate.Fingerprint) || !validDigest(bundle.Candidate.PolicyLockHash) || !validDigest(bundle.CompletionDigest) || !validDigest(bundle.Digest) {
		return invalidProof("digest identity is incomplete")
	}
	if bundle.Candidate.PolicyReportHash != "" && !validDigest(bundle.Candidate.PolicyReportHash) {
		return invalidProof("policy report digest is invalid")
	}
	return verifyCandidateIdentity(bundle.Candidate)
}

func validBuildIdentity(build Build) bool {
	return strings.TrimSpace(build.Version) != "" &&
		boundedProofText(build.Version) && boundedProofText(build.ProvenanceFormat) &&
		boundedProofText(build.SourceDigest) && boundedProofText(build.GOOS) &&
		boundedProofText(build.GOARCH)
}

func verifyCandidateIdentity(candidate Candidate) error {
	if !boundedProofText(candidate.GitHead) {
		return invalidProof("candidate Git HEAD exceeds its text limit")
	}
	if candidate.GitAvailable {
		validHead := candidate.GitHead == "UNBORN" || validGitObjectID(candidate.GitHead)
		if !validHead || !validDigest(candidate.GitIndexHash) || !validDigest(candidate.WorktreeHash) {
			return invalidProof("Git candidate identity is incomplete")
		}
	} else if candidate.GitIndexHash != "" || !validDigest(candidate.WorktreeHash) {
		return invalidProof("non-Git candidate identity is invalid")
	}
	if err := verifyStringCollection("candidate dirty paths", candidate.DirtyPaths, true); err != nil {
		return err
	}
	return nil
}

func verifyTaskIdentity(task Task) error {
	validState := task.State == "absent" || task.State == "active" || task.State == "terminal" || task.State == "invalid"
	if !validState || task.Configured == (task.State == "absent") {
		return invalidProof("TASK identity is inconsistent")
	}
	if task.State == "active" && strings.TrimSpace(task.ID) == "" {
		return invalidProof("active TASK identity is incomplete")
	}
	if task.State == "absent" && task.ID != "" {
		return invalidProof("absent TASK contains an ID")
	}
	if !boundedProofText(task.ID) {
		return invalidProof("TASK ID exceeds its text limit")
	}
	return nil
}

func verifyDecision(bundle *Bundle) error {
	if (bundle.Decision != "pass" && bundle.Decision != "block") || bundle.OK != (bundle.Decision == "pass") {
		return invalidProof("decision is inconsistent")
	}
	hasFailure := false
	passChecks := make([]string, 0, len(bundle.Checks))
	for _, check := range bundle.Checks {
		if check.Status == completiongate.StatusFail {
			hasFailure = true
		}
		if check.Status == completiongate.StatusPass {
			passChecks = append(passChecks, check.ID)
		}
	}
	if bundle.OK == hasFailure || !slices.Equal(passChecks, bundle.Evidence.SatisfiedChecks) {
		return invalidProof("completion checks are inconsistent with the decision")
	}
	if bundle.OK && bundle.NextAction != "" || !bundle.OK && strings.TrimSpace(bundle.NextAction) == "" {
		return invalidProof("next action is inconsistent with the decision")
	}
	if !boundedProofText(bundle.NextAction) {
		return invalidProof("next action exceeds its text limit")
	}
	return nil
}

func verifyBundleCollections(bundle *Bundle) error {
	if bundle.Checks == nil || bundle.Violations == nil || bundle.SupersededBlocks == nil || bundle.Candidate.DirtyPaths == nil || bundle.Evidence.RequiredCommands == nil || bundle.Evidence.RequiredPaths == nil || bundle.Evidence.RequiredClaims == nil || bundle.Evidence.SatisfiedChecks == nil || bundle.Evidence.CommandProofs == nil {
		return invalidProof("contains a null collection")
	}
	if len(bundle.Checks) > maxItems || len(bundle.Violations) > maxItems || len(bundle.SupersededBlocks) > maxItems || len(bundle.Evidence.CommandProofs) > maxItems {
		return invalidProof("contains a collection over its item limit")
	}
	for _, collection := range []struct {
		name  string
		value []string
		paths bool
	}{
		{"required commands", bundle.Evidence.RequiredCommands, false},
		{"required paths", bundle.Evidence.RequiredPaths, true},
		{"required claims", bundle.Evidence.RequiredClaims, false},
		{"satisfied checks", bundle.Evidence.SatisfiedChecks, false},
	} {
		if err := verifyStringCollection(collection.name, collection.value, collection.paths); err != nil {
			return err
		}
	}
	return nil
}

func verifyChecks(checks []Check) error {
	previous := ""
	for _, check := range checks {
		if strings.TrimSpace(check.ID) == "" || !boundedProofText(check.ID) || !boundedProofText(check.Detail) {
			return invalidProof("contains an invalid check")
		}
		if check.Status != completiongate.StatusPass && check.Status != completiongate.StatusWarn && check.Status != completiongate.StatusFail {
			return invalidProof("contains an invalid check")
		}
		if previous != "" && check.ID <= previous {
			return invalidProof("checks are not uniquely sorted")
		}
		previous = check.ID
	}
	return nil
}

func verifyCommandProofs(proofs []CommandProof) error {
	previous := ""
	for _, proof := range proofs {
		if !validCommandProofContract(proof) {
			return invalidProof("contains an invalid command proof")
		}
		if !validDigest(proof.CommandHash) || !validGitObjectID(proof.IndexTree) || !validDigest(proof.ReceiptDigest) || (!validGitObjectID(proof.Head) && proof.Head != "UNBORN") {
			return invalidProof("contains an invalid command proof identity")
		}
		identity := proof.CommandHash + "\x00" + proof.ReceiptDigest
		if previous != "" && identity <= previous {
			return invalidProof("command proofs are not uniquely sorted")
		}
		previous = identity
	}
	return nil
}

func validCommandProofContract(proof CommandProof) bool {
	validMode := proof.ExecutionMode == "direct" || proof.ExecutionMode == "shell"
	return strings.TrimSpace(proof.Command) != "" && boundedProofText(proof.Command) &&
		boundedProofText(proof.ExecutionMode) && boundedProofText(proof.Outcome) && validMode &&
		proof.Outcome == "success" && proof.ExitCode == 0 && proof.CandidateBound && proof.Fresh
}

func validGitObjectID(value string) bool {
	return validHexBytes(value, 20) || validHexBytes(value, 32)
}

func verifyViolations(name string, violations []Violation) error {
	if len(violations) > maxItems {
		return invalidProof(name + " exceeds its item limit")
	}
	previous := ""
	for _, violation := range violations {
		if !validViolationIdentity(violation) {
			return invalidProof(name + " contains an incomplete violation")
		}
		if previous != "" && violation.RuleID <= previous {
			return invalidProof(name + " is not uniquely sorted")
		}
		previous = violation.RuleID
		for _, collection := range []struct {
			name  string
			value []string
			paths bool
		}{
			{"matched paths", violation.MatchedPaths, true},
			{"required paths", violation.RequiredPaths, true},
			{"required commands", violation.RequiredCommands, false},
			{"required claims", violation.RequiredClaims, false},
		} {
			if collection.value == nil {
				return invalidProof(name + " contains a null collection")
			}
			if err := verifyStringCollection(collection.name, collection.value, collection.paths); err != nil {
				return err
			}
		}
	}
	return nil
}

func validViolationIdentity(violation Violation) bool {
	return strings.TrimSpace(violation.RuleID) != "" && strings.TrimSpace(violation.Kind) != "" &&
		strings.TrimSpace(violation.Mode) != "" && strings.TrimSpace(violation.Message) != "" &&
		boundedProofText(violation.RuleID) && boundedProofText(violation.Kind) &&
		boundedProofText(violation.Mode) && boundedProofText(violation.Message) &&
		boundedProofText(violation.RecommendedAction)
}

func verifySupersededBlocks(blocks []SupersededBlock) error {
	previous := ""
	for _, block := range blocks {
		if !validDigest(block.CandidateFingerprint) || !validDigest(block.PolicyReportHash) || block.Violations == nil {
			return invalidProof("contains an invalid superseded block")
		}
		if previous != "" && block.CandidateFingerprint <= previous {
			return invalidProof("superseded blocks are not uniquely sorted")
		}
		previous = block.CandidateFingerprint
		if err := verifyViolations("superseded block violations", block.Violations); err != nil {
			return err
		}
	}
	return nil
}

func verifyStringCollection(name string, values []string, paths bool) error {
	if len(values) > maxItems {
		return invalidProof(name + " exceeds its item limit")
	}
	previous := ""
	for _, value := range values {
		if strings.TrimSpace(value) == "" || !boundedProofText(value) {
			return invalidProof(name + " contains an invalid value")
		}
		if previous != "" && value <= previous {
			return invalidProof(name + " is not uniquely sorted")
		}
		if paths && !portableProofPath(value) {
			return invalidProof(name + " contains a non-portable path")
		}
		previous = value
	}
	return nil
}

func boundedProofText(value string) bool {
	return len(value) <= maxTextBytes+len("...[bounded]")
}

func portableProofPath(value string) bool {
	if value == "<external>" {
		return true
	}
	if strings.Contains(value, "\\") || strings.HasPrefix(value, "/") {
		return false
	}
	cleaned := path.Clean(value)
	return cleaned == value && cleaned != ".." && !strings.HasPrefix(cleaned, "../")
}

func invalidProof(message string) error {
	return fmt.Errorf("%w: proof bundle %s", ErrInvalidContract, message)
}
