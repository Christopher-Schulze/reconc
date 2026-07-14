package assurance

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/policy"
)

type proofDocument struct {
	FormatVersion string        `json:"format_version"`
	Proofs        []proofRecord `json:"proofs"`
}

type proofRecord struct {
	ID             string    `json:"id"`
	Subject        string    `json:"subject"`
	Command        string    `json:"command"`
	Outcome        string    `json:"outcome"`
	Aggregation    string    `json:"aggregation"`
	Comparator     string    `json:"comparator"`
	Threshold      *float64  `json:"threshold"`
	Actual         *float64  `json:"actual"`
	Samples        []float64 `json:"samples"`
	EvidencePath   string    `json:"evidence_path"`
	EvidenceSHA256 string    `json:"evidence_sha256"`
	VerifiedAt     string    `json:"verified_at"`
}

func evaluateSubstantiveProof(root string, gate policy.AssuranceGate, inputs Inputs, state *evaluationState) ([]Finding, error) {
	resolved, err := state.resolve(root, gate.ProofFile)
	if err != nil {
		return nil, err
	}
	if !resolved.exists {
		return []Finding{{GateID: gate.ID, Paths: []string{gate.ProofFile}, Message: "substantive proof manifest is missing", Remediation: "Create the configured proof manifest from real measured evidence."}}, nil
	}
	body, err := state.read(resolved.full)
	if err != nil {
		return nil, err
	}
	var document proofDocument
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode proof manifest %s: %w", gate.ProofFile, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("decode proof manifest %s: %w", gate.ProofFile, err)
	}
	findings := []Finding{}
	if document.FormatVersion != "1" {
		findings = append(findings, proofFinding(gate, gate.ProofFile, "format_version must be 1"))
	}
	if len(document.Proofs) == 0 {
		findings = append(findings, proofFinding(gate, gate.ProofFile, "proofs must contain at least one measured proof"))
	}
	successful := stringSetNormalized(inputs.SuccessfulCommands)
	seen := map[string]bool{}
	for index, proof := range document.Proofs {
		label := fmt.Sprintf("proofs[%d]", index)
		proofID := strings.TrimSpace(proof.ID)
		if proofID == "" || seen[proofID] {
			findings = append(findings, proofFinding(gate, gate.ProofFile, label+" requires a unique non-empty id"))
		}
		seen[proofID] = true
		if strings.TrimSpace(proof.Subject) == "" || proof.Threshold == nil || proof.Actual == nil {
			findings = append(findings, proofFinding(gate, gate.ProofFile, label+" requires subject, threshold, and actual"))
		}
		if strings.TrimSpace(proof.Command) == "" {
			findings = append(findings, proofFinding(gate, gate.ProofFile, label+" requires a non-empty command"))
		}
		if proof.Outcome != "pass" {
			findings = append(findings, proofFinding(gate, gate.ProofFile, label+" outcome must be pass"))
		}
		if len(proof.Samples) < gate.MinSamples {
			findings = append(findings, proofFinding(gate, gate.ProofFile, fmt.Sprintf("%s has %d samples, requires at least %d", label, len(proof.Samples), gate.MinSamples)))
		} else if proof.Threshold != nil && proof.Actual != nil {
			computed, computeErr := aggregateSamples(proof.Aggregation, proof.Samples)
			if computeErr != nil {
				findings = append(findings, proofFinding(gate, gate.ProofFile, label+" "+computeErr.Error()))
			} else if !nearlyEqual(computed, *proof.Actual) {
				findings = append(findings, proofFinding(gate, gate.ProofFile, fmt.Sprintf("%s actual %.9g does not match %s(samples) %.9g", label, *proof.Actual, proof.Aggregation, computed)))
			} else if !validComparator(proof.Comparator) {
				findings = append(findings, proofFinding(gate, gate.ProofFile, label+" comparator must be one of lt, lte, eq, gte, or gt"))
			} else if !comparisonPasses(proof.Comparator, computed, *proof.Threshold) {
				findings = append(findings, proofFinding(gate, gate.ProofFile, fmt.Sprintf("%s measured value %.9g does not satisfy %s %.9g", label, computed, proof.Comparator, *proof.Threshold)))
			}
		}
		if !successful[normalizeCommand(proof.Command)] {
			findings = append(findings, proofFinding(gate, gate.ProofFile, label+" command has no current successful runtime evidence: "+proof.Command))
		}
		verifiedAt, parseErr := time.Parse(time.RFC3339, proof.VerifiedAt)
		if parseErr != nil || verifiedAt.After(inputs.Now.Add(5*time.Minute)) || inputs.Now.Sub(verifiedAt) > time.Duration(gate.MaxAgeHours)*time.Hour {
			findings = append(findings, proofFinding(gate, gate.ProofFile, label+" verified_at is invalid, future-dated, or stale"))
		}
		if err := verifyEvidenceHash(root, proof, state); err != nil {
			findings = append(findings, proofFinding(gate, proof.EvidencePath, label+" "+err.Error()))
		}
	}
	return findings, nil
}

func verifyEvidenceHash(root string, proof proofRecord, state *evaluationState) error {
	if strings.TrimSpace(proof.EvidencePath) == "" || strings.TrimSpace(proof.EvidenceSHA256) == "" {
		return fmt.Errorf("requires evidence_path and evidence_sha256")
	}
	resolved, err := state.resolve(root, proof.EvidencePath)
	if err != nil {
		return err
	}
	if !resolved.exists {
		return fmt.Errorf("evidence file is missing: %s", proof.EvidencePath)
	}
	body, err := state.read(resolved.full)
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return fmt.Errorf("evidence file is empty: %s", proof.EvidencePath)
	}
	sum := sha256.Sum256(body)
	actual := hex.EncodeToString(sum[:])
	if !strings.EqualFold(actual, strings.TrimSpace(proof.EvidenceSHA256)) {
		return fmt.Errorf("evidence hash mismatch for %s", proof.EvidencePath)
	}
	return nil
}

func aggregateSamples(aggregation string, samples []float64) (float64, error) {
	for _, sample := range samples {
		if math.IsNaN(sample) || math.IsInf(sample, 0) {
			return 0, fmt.Errorf("samples must contain only finite numbers")
		}
	}
	values := append([]float64(nil), samples...)
	switch aggregation {
	case "last":
		return values[len(values)-1], nil
	case "mean":
		total := 0.0
		for _, value := range values {
			total += value
		}
		return total / float64(len(values)), nil
	case "min":
		sort.Float64s(values)
		return values[0], nil
	case "max":
		sort.Float64s(values)
		return values[len(values)-1], nil
	case "median":
		sort.Float64s(values)
		middle := len(values) / 2
		if len(values)%2 == 0 {
			return (values[middle-1] + values[middle]) / 2, nil
		}
		return values[middle], nil
	case "p95":
		sort.Float64s(values)
		index := int(math.Ceil(0.95*float64(len(values)))) - 1
		return values[index], nil
	default:
		return 0, fmt.Errorf("aggregation must be one of last, mean, min, max, median, or p95")
	}
}

func comparisonPasses(comparator string, actual, threshold float64) bool {
	switch comparator {
	case "lte":
		return actual <= threshold
	case "gte":
		return actual >= threshold
	case "lt":
		return actual < threshold
	case "gt":
		return actual > threshold
	case "eq":
		return nearlyEqual(actual, threshold)
	default:
		return false
	}
}

func validComparator(comparator string) bool {
	return comparator == "lt" || comparator == "lte" || comparator == "eq" || comparator == "gte" || comparator == "gt"
}

func nearlyEqual(left, right float64) bool {
	scale := math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
	return math.Abs(left-right) <= 1e-9*scale
}

func proofFinding(gate policy.AssuranceGate, path, message string) Finding {
	return Finding{GateID: gate.ID, Paths: []string{path}, Message: message, Remediation: "Regenerate the proof manifest from a current successful command and byte-matched evidence."}
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing interface{}
	err := decoder.Decode(&trailing)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		return fmt.Errorf("unexpected trailing JSON value")
	}
	return err
}
