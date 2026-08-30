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
	"strconv"
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
		futureDated := parseErr == nil && verifiedAt.After(inputs.Now.Add(5*time.Minute))
		stale := parseErr == nil && gate.MaxAgeHours > 0 && inputs.Now.Sub(verifiedAt) > time.Duration(gate.MaxAgeHours)*time.Hour
		if parseErr != nil || futureDated || stale {
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
	if !validCanonicalSHA256(proof.EvidenceSHA256) {
		return fmt.Errorf("evidence_sha256 must contain exactly 64 lowercase hexadecimal characters")
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
	if actual != proof.EvidenceSHA256 {
		return fmt.Errorf("evidence hash mismatch for %s", proof.EvidencePath)
	}
	evidenceSamples, err := parseEvidenceSamples(body)
	if err != nil {
		return fmt.Errorf("evidence samples for %s: %w", proof.EvidencePath, err)
	}
	if !sameEvidenceSamples(proof.Samples, evidenceSamples) {
		return fmt.Errorf("evidence samples do not match declared samples for %s", proof.EvidencePath)
	}
	return nil
}

func validCanonicalSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for index := range value {
		if (value[index] < '0' || value[index] > '9') && (value[index] < 'a' || value[index] > 'f') {
			return false
		}
	}
	return true
}

const evidenceTextPrefix = "measured samples:"
const legacyEvidenceTextPrefix = "benchmark samples:"

func parseEvidenceSamples(body []byte) ([]float64, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("evidence must contain measured samples")
	}
	if trimmed[0] == '{' || trimmed[0] == '[' {
		return parseJSONEvidenceSamples(trimmed)
	}
	return parseTextEvidenceSamples(trimmed)
}

func parseJSONEvidenceSamples(body []byte) ([]float64, error) {
	if err := rejectDuplicateProofJSONKeys(body); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var numbers []json.Number
	if body[0] == '{' {
		var document struct {
			Samples []json.Number `json:"samples"`
		}
		if err := decoder.Decode(&document); err != nil {
			return nil, fmt.Errorf("expected an object with a samples array: %w", err)
		}
		numbers = document.Samples
	} else {
		if err := decoder.Decode(&numbers); err != nil {
			return nil, fmt.Errorf("expected a JSON samples array: %w", err)
		}
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	return parseEvidenceNumbers(numbers)
}

func parseTextEvidenceSamples(body []byte) ([]float64, error) {
	text := strings.TrimSpace(string(body))
	prefix := ""
	for _, candidate := range []string{evidenceTextPrefix, legacyEvidenceTextPrefix} {
		if strings.HasPrefix(text, candidate) {
			prefix = candidate
			break
		}
	}
	if prefix == "" {
		return nil, fmt.Errorf("unsupported format; expected %q, %q, or a JSON samples array", evidenceTextPrefix, legacyEvidenceTextPrefix)
	}
	payload := strings.TrimSpace(strings.TrimPrefix(text, prefix))
	if payload == "" {
		return nil, fmt.Errorf("evidence must contain at least one measured sample")
	}
	values := strings.Split(payload, ",")
	samples := make([]float64, len(values))
	for index, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			return nil, fmt.Errorf("sample %d is empty", index+1)
		}
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return nil, fmt.Errorf("sample %d is not a finite number: %q", index+1, value)
		}
		samples[index] = parsed
	}
	return samples, nil
}

func parseEvidenceNumbers(numbers []json.Number) ([]float64, error) {
	if len(numbers) == 0 {
		return nil, fmt.Errorf("evidence must contain at least one measured sample")
	}
	samples := make([]float64, len(numbers))
	for index, number := range numbers {
		parsed, err := strconv.ParseFloat(string(number), 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return nil, fmt.Errorf("sample %d is not a finite number: %q", index+1, number)
		}
		samples[index] = parsed
	}
	return samples, nil
}

func sameEvidenceSamples(declared, measured []float64) bool {
	if len(declared) != len(measured) {
		return false
	}
	for index := range declared {
		if math.Float64bits(declared[index]) != math.Float64bits(measured[index]) {
			return false
		}
	}
	return true
}

func rejectDuplicateProofJSONKeys(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := walkProofJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func walkProofJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate evidence object key %q", key)
			}
			seen[key] = struct{}{}
			if err := walkProofJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return fmt.Errorf("invalid evidence object termination")
		}
	case '[':
		for decoder.More() {
			if err := walkProofJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return fmt.Errorf("invalid evidence array termination")
		}
	default:
		return fmt.Errorf("unexpected evidence JSON delimiter %q", delimiter)
	}
	return nil
}

func aggregateSamples(aggregation string, samples []float64) (float64, error) {
	if len(samples) == 0 {
		return 0, fmt.Errorf("samples must contain at least one value")
	}
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
