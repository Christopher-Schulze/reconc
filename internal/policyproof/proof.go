// Package policyproof persists the latest explicit policy decision as a
// tamper-evident receipt in Reconc-owned state outside the repository.
package policyproof

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/privatefs"
	"reconc.dev/reconc/internal/retention"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/schema"
)

const (
	Schema        = "reconc.policy-decision/v1"
	FormatVersion = "1"
	maxProofBytes = 8 << 20
)

// Record binds one complete policy report to the exact repository/session
// candidate that produced it. Digest covers every field except itself.
type Record struct {
	Schema               string               `json:"schema"`
	FormatVersion        string               `json:"format_version"`
	Event                string               `json:"event"`
	RepoRoot             string               `json:"repo_root"`
	CandidateFingerprint string               `json:"candidate_fingerprint"`
	PolicyReportHash     string               `json:"policy_report_hash"`
	Report               *runtime.CheckReport `json:"report"`
	Digest               string               `json:"digest"`
}

type recordPayload struct {
	Schema               string          `json:"schema"`
	FormatVersion        string          `json:"format_version"`
	Event                string          `json:"event"`
	RepoRoot             string          `json:"repo_root"`
	CandidateFingerprint string          `json:"candidate_fingerprint"`
	PolicyReportHash     string          `json:"policy_report_hash"`
	Report               json.RawMessage `json:"report"`
}

// Store atomically replaces the unresolved blocking receipt after validating
// its complete in-memory shape. A non-blocking report durably clears any older
// block, so elapsed time and retention can never manufacture remediation.
func Store(repoRoot, event, candidateFingerprint string, report *runtime.CheckReport) error {
	record, err := newRecord(repoRoot, event, candidateFingerprint, report)
	if err != nil {
		return err
	}
	if record.Report.Decision != runtime.DecisionBlock {
		return clear(repoRoot)
	}
	body, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal policy decision proof: %w", err)
	}
	body = append(body, '\n')
	if len(body) > maxProofBytes {
		return fmt.Errorf("policy decision proof exceeds %d bytes", maxProofBytes)
	}
	dir := filepath.Dir(Path(repoRoot))
	if err := privatefs.RepairDirectory(dir); err != nil {
		return fmt.Errorf("secure policy decision proof directory: %w", err)
	}
	if _, err := privatefs.WritePrivateIfChanged(Path(repoRoot), body, 0o600); err != nil {
		return fmt.Errorf("write policy decision proof: %w", err)
	}
	return nil
}

func clear(repoRoot string) error {
	path := Path(repoRoot)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect policy decision proof before clear: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("policy decision proof is not a regular file: %s", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("clear remediated policy decision proof: %w", err)
	}
	if err := syncProofDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync cleared policy decision proof: %w", err)
	}
	return nil
}

// LoadLatest validates the bounded receipt and every nested digest. A missing
// receipt is normal and returns (Record{}, false, nil); malformed or tampered
// bytes fail closed.
func LoadLatest(repoRoot string) (Record, bool, error) {
	path := Path(repoRoot)
	body, err := boundedio.ReadRegularFile(path, maxProofBytes)
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("read policy decision proof as a bounded regular file: %w", err)
	}
	var record Record
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return Record{}, false, fmt.Errorf("decode policy decision proof: %w", err)
	}
	if err := ensureEOF(decoder); err != nil {
		return Record{}, false, fmt.Errorf("decode policy decision proof: %w", err)
	}
	if err := validateRecord(record, repoRoot); err != nil {
		return Record{}, false, err
	}
	return record, true, nil
}

// Path returns the private Reconc-owned receipt location for repoRoot.
func Path(repoRoot string) string {
	root := filepath.Clean(repoRoot)
	if resolved, err := pathidentity.ResolveExisting(root); err == nil {
		root = resolved
	}
	project := retention.ProjectDir(retention.ResolveStateRoot(), root)
	return filepath.Join(project, "policy-decisions", "latest.json")
}

func newRecord(repoRoot, event, candidateFingerprint string, report *runtime.CheckReport) (Record, error) {
	reportRoot := ""
	if report != nil {
		reportRoot = filepath.Clean(report.RepoRoot)
	}
	reportBytes, err := marshalReport(report)
	if err != nil {
		return Record{}, fmt.Errorf("encode policy report identity: %w", err)
	}
	record := Record{
		Schema: Schema, FormatVersion: FormatVersion, Event: strings.TrimSpace(event),
		RepoRoot: reportRoot, CandidateFingerprint: strings.TrimSpace(candidateFingerprint),
		Report: report,
	}
	if report != nil {
		record.PolicyReportHash = hash(reportBytes)
	}
	if err := validateRecordShape(record); err != nil {
		return Record{}, err
	}
	if err := requireSameRepository(record.RepoRoot, repoRoot); err != nil {
		return Record{}, err
	}
	digest, err := recordDigestWithReportBytes(record, reportBytes)
	if err != nil {
		return Record{}, err
	}
	record.Digest = digest
	return record, nil
}

func validateRecord(record Record, repoRoot string) error {
	if err := validateRecordShape(record); err != nil {
		return err
	}
	if record.Report.Decision != runtime.DecisionBlock {
		return errors.New("stored policy decision proof is not an unresolved block")
	}
	if err := requireSameRepository(record.RepoRoot, repoRoot); err != nil {
		return err
	}
	reportBytes, err := marshalReport(record.Report)
	if err != nil {
		return fmt.Errorf("encode policy report identity: %w", err)
	}
	reportDigest := hash(reportBytes)
	if record.PolicyReportHash != reportDigest {
		return errors.New("policy decision proof report hash mismatch")
	}
	digest, err := recordDigestWithReportBytes(record, reportBytes)
	if err != nil {
		return err
	}
	if !equalDigest(record.Digest, digest) {
		return errors.New("policy decision proof digest mismatch")
	}
	return nil
}

func validateRecordShape(record Record) error {
	switch {
	case record.Schema != Schema:
		return errors.New("unsupported policy decision proof schema")
	case record.FormatVersion != FormatVersion:
		return errors.New("unsupported policy decision proof format version")
	case record.Event == "":
		return errors.New("policy decision proof event is empty")
	case strings.ContainsAny(record.Event, "\r\n") || len(record.Event) > 128:
		return errors.New("policy decision proof event must be one line and at most 128 bytes")
	case record.RepoRoot == "" || !filepath.IsAbs(record.RepoRoot):
		return errors.New("policy decision proof repository root is not absolute")
	case record.CandidateFingerprint == "":
		return errors.New("policy decision proof candidate fingerprint is empty")
	case !validDigest(record.CandidateFingerprint):
		return errors.New("policy decision proof candidate fingerprint is invalid")
	case record.Report == nil:
		return errors.New("policy decision proof report is missing")
	case filepath.Clean(record.Report.RepoRoot) != filepath.Clean(record.RepoRoot):
		return errors.New("policy decision proof report repository mismatch")
	case record.PolicyReportHash == "":
		return errors.New("policy decision proof report hash is empty")
	case !validDigest(record.PolicyReportHash):
		return errors.New("policy decision proof report hash is invalid")
	}
	return validatePolicyReport(record.Report)
}

func validatePolicyReport(report *runtime.CheckReport) error {
	if !schema.AcceptsFormat(schema.PolicyReport, report.Schema, report.FormatVersion) {
		return errors.New("policy decision proof report schema or format version is invalid")
	}
	derived := *report
	derived.Finalize()
	if derived.OK != report.OK || derived.Decision != report.Decision ||
		derived.ViolationCount != report.ViolationCount ||
		derived.BlockingViolationCount != report.BlockingViolationCount ||
		!slices.Equal(derived.Actions, report.Actions) ||
		!slices.Equal(derived.RuleIDs, report.RuleIDs) {
		return errors.New("policy decision proof report derived fields are inconsistent")
	}
	return nil
}

func requireSameRepository(left, right string) error {
	leftIdentity, leftErr := pathidentity.ResolveExisting(left)
	rightIdentity, rightErr := pathidentity.ResolveExisting(right)
	if leftErr != nil || rightErr != nil || leftIdentity != rightIdentity {
		return fmt.Errorf("policy decision proof repository %q does not match %q", left, right)
	}
	return nil
}

func marshalReport(report *runtime.CheckReport) ([]byte, error) {
	if report == nil {
		return nil, nil
	}
	body, err := json.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("marshal policy report identity: %w", err)
	}
	return body, nil
}

func reportHash(report *runtime.CheckReport) (string, error) {
	body, err := marshalReport(report)
	if err != nil {
		return "", err
	}
	if report == nil {
		return "", nil
	}
	return hash(body), nil
}

func recordDigest(record Record) (string, error) {
	reportBytes, err := marshalReport(record.Report)
	if err != nil {
		return "", fmt.Errorf("encode policy report identity: %w", err)
	}
	return recordDigestWithReportBytes(record, reportBytes)
}

func recordDigestWithReportBytes(record Record, reportBytes []byte) (string, error) {
	payload := recordPayload{
		Schema: record.Schema, FormatVersion: record.FormatVersion, Event: record.Event,
		RepoRoot: record.RepoRoot, CandidateFingerprint: record.CandidateFingerprint,
		PolicyReportHash: record.PolicyReportHash, Report: json.RawMessage(reportBytes),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal policy decision proof digest: %w", err)
	}
	return hash(body), nil
}

func hash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func equalDigest(left, right string) bool {
	leftBytes, leftErr := hex.DecodeString(left)
	rightBytes, rightErr := hex.DecodeString(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftBytes, rightBytes)
}

func validDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func ensureEOF(decoder *json.Decoder) error {
	var trailing interface{}
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("unexpected trailing JSON value")
	}
	return err
}
