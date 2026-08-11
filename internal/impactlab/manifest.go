package impactlab

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/boundedio"
)

// NewDeltaManifest canonicalizes exact review entries and binds their content.
func NewDeltaManifest(entries []ReviewedActionDelta) (DeltaManifest, error) {
	if entries == nil {
		entries = []ReviewedActionDelta{}
	}
	if len(entries) > maxCases {
		return DeltaManifest{}, fmt.Errorf("impact delta manifest exceeds %d entries", maxCases)
	}
	manifest := DeltaManifest{
		FormatVersion: DeltaManifestFormatVersion,
		Entries:       cloneReviewedActionDeltas(entries),
	}
	sort.Slice(manifest.Entries, func(i, j int) bool {
		return reviewedDeltaKey(manifest.Entries[i]) < reviewedDeltaKey(manifest.Entries[j])
	})
	identity, err := deltaManifestIdentity(manifest)
	if err != nil {
		return DeltaManifest{}, err
	}
	manifest.ManifestID = identity
	if err := validateDeltaManifest(manifest); err != nil {
		return DeltaManifest{}, err
	}
	return manifest, nil
}

// DecodeDeltaManifestFile reads one bounded regular non-symlink manifest.
func DecodeDeltaManifestFile(filePath string) (DeltaManifest, error) {
	info, err := os.Lstat(filePath)
	if err != nil {
		return DeltaManifest{}, err
	}
	if !info.Mode().IsRegular() {
		return DeltaManifest{}, fmt.Errorf("impact delta manifest %s must be a regular file and not a symlink", filePath)
	}
	body, err := boundedio.ReadFile(filePath, MaxDeltaManifestBytes)
	if err != nil {
		return DeltaManifest{}, err
	}
	return DecodeDeltaManifest(body)
}

// DecodeDeltaManifest strictly decodes one exact review manifest.
func DecodeDeltaManifest(body []byte) (DeltaManifest, error) {
	if len(body) > MaxDeltaManifestBytes || !utf8.Valid(body) {
		return DeltaManifest{}, fmt.Errorf("impact delta manifest is oversized or invalid UTF-8")
	}
	if err := validateUniqueJSONKeys(body); err != nil {
		return DeltaManifest{}, err
	}
	if err := validateExactJSONFields(body, reflect.TypeOf(DeltaManifest{})); err != nil {
		return DeltaManifest{}, err
	}
	var manifest DeltaManifest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return DeltaManifest{}, fmt.Errorf("decode impact delta manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return DeltaManifest{}, fmt.Errorf("impact delta manifest must contain exactly one JSON object")
	}
	if err := validateDeltaManifest(manifest); err != nil {
		return DeltaManifest{}, err
	}
	return manifest, nil
}

// MarshalDeltaManifest returns deterministic indented JSON.
func MarshalDeltaManifest(manifest DeltaManifest) ([]byte, error) {
	if err := validateDeltaManifestContract(manifest); err != nil {
		return nil, err
	}
	return marshalDeltaManifestJSON(manifest)
}

func marshalDeltaManifestJSON(manifest DeltaManifest) ([]byte, error) {
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	body = append(body, '\n')
	if len(body) > MaxDeltaManifestBytes {
		return nil, fmt.Errorf("impact delta manifest exceeds %d bytes", MaxDeltaManifestBytes)
	}
	return body, nil
}

func validateDeltaManifest(manifest DeltaManifest) error {
	if err := validateDeltaManifestContract(manifest); err != nil {
		return err
	}
	_, err := marshalDeltaManifestJSON(manifest)
	return err
}

func validateDeltaManifestContract(manifest DeltaManifest) error {
	if manifest.FormatVersion != DeltaManifestFormatVersion || manifest.ManifestID == "" || manifest.Entries == nil {
		return fmt.Errorf("unsupported or incomplete impact delta manifest contract")
	}
	if len(manifest.Entries) > maxCases {
		return fmt.Errorf("impact delta manifest exceeds %d entries", maxCases)
	}
	identity, err := deltaManifestIdentity(manifest)
	if err != nil {
		return fmt.Errorf("encode impact delta manifest identity: %w", err)
	}
	if manifest.ManifestID != identity {
		return fmt.Errorf("impact delta manifest identity does not match its contents")
	}
	for index, entry := range manifest.Entries {
		if err := validateReviewedActionDelta(entry); err != nil {
			return fmt.Errorf("impact delta manifest entry[%d]: %w", index, err)
		}
		if index > 0 && reviewedDeltaKey(manifest.Entries[index-1]) >= reviewedDeltaKey(entry) {
			return fmt.Errorf("impact delta manifest entries must be unique and sorted")
		}
	}
	return nil
}

func validateReviewedActionDelta(entry ReviewedActionDelta) error {
	if !validCaseID(entry.CaseID) || !validSHAIdentity(entry.CaseIdentity) ||
		(entry.Delta != DeltaNewlyAllowed && entry.Delta != DeltaNewlyBlocked) ||
		!lowerHexDigest(entry.CandidateLockDigest) {
		return fmt.Errorf("case, delta, or digest binding is invalid")
	}
	if err := validateManifestAssertion(entry.Current); err != nil {
		return fmt.Errorf("current outcome: %w", err)
	}
	if err := validateManifestAssertion(entry.Candidate); err != nil {
		return fmt.Errorf("candidate outcome: %w", err)
	}
	if strings.TrimSpace(entry.Rationale) != entry.Rationale || entry.Rationale == "" ||
		len(entry.Rationale) > maxRationaleBytes || !utf8.ValidString(entry.Rationale) ||
		containsUnsafeControl(entry.Rationale) || unsafeActionMetadata(entry.Rationale) {
		return fmt.Errorf("rationale must contain 1..%d safe bytes without surrounding whitespace", maxRationaleBytes)
	}
	if entry.Permanent == (entry.ExpiresAt != "") {
		return fmt.Errorf("review must be permanent or have one expiry, never both or neither")
	}
	if entry.ExpiresAt != "" {
		parsed, err := time.Parse(time.RFC3339, entry.ExpiresAt)
		if err != nil || parsed.UTC().Format(time.RFC3339) != entry.ExpiresAt {
			return fmt.Errorf("expiry must be canonical UTC RFC3339 without fractional seconds")
		}
	}
	return nil
}

func validateManifestAssertion(assertion ActionAssertion) error {
	if !assertion.Decision.Valid() || !assertion.Reason.Valid() || !assertion.Cache.Reason.Valid() ||
		!assertion.PhaseOutcome.Valid() || assertion.Cache.Eligible != (assertion.Cache.Reason == action.CacheEligible) ||
		assertion.FailureCode != "" && (!assertion.FailureCode.Valid() || assertion.FailureCode != assertion.Reason) ||
		assertion.MatchedRuleIDs == nil || assertion.Completeness.Missing == nil {
		return fmt.Errorf("outcome assertion is invalid")
	}
	if assertion.ToolID != "" && (!action.SafeLabel(assertion.ToolID) || unsafeActionMetadata(assertion.ToolID)) {
		return fmt.Errorf("outcome tool id is invalid")
	}
	if err := validateActionLedgerAssertion("", assertion.Ledger); err != nil {
		return err
	}
	canonical, err := action.NormalizeCompleteness(assertion.Completeness)
	if err != nil || !equalActionCompleteness(canonical, assertion.Completeness) {
		return fmt.Errorf("outcome completeness is invalid or non-canonical")
	}
	seen := make(map[string]struct{}, len(assertion.MatchedRuleIDs))
	for _, ruleID := range assertion.MatchedRuleIDs {
		if !action.SafeLabel(ruleID) || unsafeActionMetadata(ruleID) {
			return fmt.Errorf("outcome rule id is invalid")
		}
		if _, duplicate := seen[ruleID]; duplicate {
			return fmt.Errorf("outcome rule id is duplicated")
		}
		seen[ruleID] = struct{}{}
	}
	return nil
}

// ApplyDeltaManifest verifies an exact manifest and marks only the matched
// gated deltas reviewed. Orphaned, stale, or expired entries fail; a partial
// manifest leaves the report gate failed and its unmatched cases explicit.
func ApplyDeltaManifest(report Report, manifest DeltaManifest, now time.Time) (Report, error) {
	if err := validateDeltaManifest(manifest); err != nil {
		return Report{}, err
	}
	out := cloneReportForReview(report)
	required := requiredActionDeltaEntries(out)
	matched := make(map[string]struct{}, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		key := reviewedDeltaKey(entry)
		requirement, ok := required[key]
		if !ok {
			return Report{}, fmt.Errorf("impact delta manifest entry %q/%s is orphaned or no longer gated", entry.CaseID, entry.Delta)
		}
		if entry.CandidateLockDigest != out.Candidate.LockDigest || entry.CaseIdentity != requirement.caseIdentity ||
			!equalActionAssertion(entry.Current, requirement.current) ||
			!equalActionAssertion(entry.Candidate, requirement.candidate) {
			return Report{}, fmt.Errorf("impact delta manifest entry %q/%s is stale or digest-mismatched", entry.CaseID, entry.Delta)
		}
		if entry.ExpiresAt != "" {
			expires, err := time.Parse(time.RFC3339, entry.ExpiresAt)
			if err != nil {
				return Report{}, fmt.Errorf("impact delta manifest entry %q/%s has invalid expiry: %w", entry.CaseID, entry.Delta, err)
			}
			if !expires.After(now.UTC()) {
				return Report{}, fmt.Errorf("impact delta manifest entry %q/%s expired at %s", entry.CaseID, entry.Delta, entry.ExpiresAt)
			}
		}
		matched[key] = struct{}{}
	}
	unreviewed := []string{}
	reviewedCount := 0
	for index := range out.Cases {
		comparison := &out.Cases[index]
		if comparison.Action == nil {
			continue
		}
		gated, reviewed := 0, 0
		for _, delta := range comparison.Action.Deltas {
			if delta != DeltaNewlyAllowed && delta != DeltaNewlyBlocked {
				continue
			}
			gated++
			key := comparison.ID + "\x00" + string(delta)
			if _, ok := matched[key]; ok {
				reviewed++
				reviewedCount++
			}
		}
		comparison.Action.Reviewed = gated > 0 && reviewed == gated
		if gated > reviewed {
			unreviewed = append(unreviewed, comparison.ID)
		}
	}
	if len(matched) != len(manifest.Entries) {
		return Report{}, fmt.Errorf("impact delta manifest did not match every entry")
	}
	out.DeltaGate = DeltaGate{
		Passed: reviewedCount == len(required), RequiredCount: len(required),
		ReviewedCount: reviewedCount, UnreviewedCases: unreviewed,
	}
	return out, nil
}

type requiredActionDelta struct {
	caseIdentity string
	current      ActionAssertion
	candidate    ActionAssertion
}

func requiredActionDeltaEntries(report Report) map[string]requiredActionDelta {
	required := map[string]requiredActionDelta{}
	for _, comparison := range report.Cases {
		if comparison.Action == nil {
			continue
		}
		for _, delta := range comparison.Action.Deltas {
			if delta != DeltaNewlyAllowed && delta != DeltaNewlyBlocked {
				continue
			}
			required[comparison.ID+"\x00"+string(delta)] = requiredActionDelta{
				caseIdentity: comparison.CaseIdentity,
				current:      comparison.Action.Current.Outcome,
				candidate:    comparison.Action.Candidate.Outcome,
			}
		}
	}
	return required
}

func cloneReportForReview(report Report) Report {
	out := report
	out.Cases = make([]CaseComparison, len(report.Cases))
	for index, comparison := range report.Cases {
		out.Cases[index] = comparison
		if comparison.Repository != nil {
			repository := *comparison.Repository
			out.Cases[index].Repository = &repository
		}
		if comparison.Action != nil {
			actionComparison := *comparison.Action
			actionComparison.Deltas = append([]ActionDeltaKind(nil), comparison.Action.Deltas...)
			out.Cases[index].Action = &actionComparison
		}
	}
	return out
}

func reviewedDeltaKey(entry ReviewedActionDelta) string {
	return entry.CaseID + "\x00" + string(entry.Delta)
}

func cloneReviewedActionDeltas(entries []ReviewedActionDelta) []ReviewedActionDelta {
	out := make([]ReviewedActionDelta, len(entries))
	for index, entry := range entries {
		out[index] = entry
		out[index].Current = cloneManifestAssertion(entry.Current)
		out[index].Candidate = cloneManifestAssertion(entry.Candidate)
	}
	return out
}

func cloneManifestAssertion(assertion ActionAssertion) ActionAssertion {
	out := assertion
	if assertion.MatchedRuleIDs != nil {
		out.MatchedRuleIDs = append([]string{}, assertion.MatchedRuleIDs...)
	}
	if assertion.Completeness.Missing != nil {
		out.Completeness.Missing = append([]action.MissingEvidence{}, assertion.Completeness.Missing...)
	}
	out.Ledger = cloneActionLedgerAssertion(assertion.Ledger)
	return out
}

func deltaManifestIdentity(manifest DeltaManifest) (string, error) {
	manifest.ManifestID = ""
	body, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("encode impact delta manifest identity: %w", err)
	}
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func validSHAIdentity(value string) bool {
	return strings.HasPrefix(value, "sha256:") && lowerHexDigest(strings.TrimPrefix(value, "sha256:"))
}

func lowerHexDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if character >= '0' && character <= '9' || character >= 'a' && character <= 'f' {
			continue
		}
		return false
	}
	return true
}
