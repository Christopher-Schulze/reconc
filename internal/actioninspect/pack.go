package actioninspect

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"reconc.dev/reconc/internal/action"
)

type detectorKind string

const (
	detectorRegexp      detectorKind = "regexp"
	detectorKeyword     detectorKind = "keyword"
	detectorPaymentCard detectorKind = "payment_card"
	detectorSecretValue detectorKind = "secret_value"
)

type detectorSeverity string

const (
	severityModerate detectorSeverity = "moderate"
	severityHigh     detectorSeverity = "high"
	severityCritical detectorSeverity = "critical"

	builtinPackFormatVersion = "1"
	builtinPackMaxRules      = 256
	builtinPackMaxPattern    = 4096
	builtinPackMaxMarkers    = 64
	builtinPackMaxMarker     = 512
	builtinPackScanChunk     = 64 << 10
	builtinPackScanOverlap   = 4096
)

type detectorPackLimits struct {
	MaxRules         int `json:"max_rules"`
	MaxPatternBytes  int `json:"max_pattern_bytes"`
	MaxMarkers       int `json:"max_markers_per_rule"`
	MaxMarkerBytes   int `json:"max_marker_bytes"`
	ScanChunkBytes   int `json:"scan_chunk_bytes"`
	ScanOverlapBytes int `json:"scan_overlap_bytes"`
}

type detectorRule struct {
	ID       string                  `json:"id"`
	Category action.DetectorCategory `json:"category"`
	Severity detectorSeverity        `json:"severity"`
	Scope    string                  `json:"scope"`
	Kind     detectorKind            `json:"kind"`
	Pattern  string                  `json:"pattern,omitempty"`
	Markers  []string                `json:"markers,omitempty"`
}

type detectorPack struct {
	FormatVersion string             `json:"format_version"`
	ID            string             `json:"id"`
	Limits        detectorPackLimits `json:"limits"`
	Rules         []detectorRule     `json:"rules"`
}

func builtinPack() detectorPack {
	return detectorPack{
		FormatVersion: builtinPackFormatVersion,
		ID:            action.BuiltinDetectorPackID,
		Limits: detectorPackLimits{
			MaxRules:         builtinPackMaxRules,
			MaxPatternBytes:  builtinPackMaxPattern,
			MaxMarkers:       builtinPackMaxMarkers,
			MaxMarkerBytes:   builtinPackMaxMarker,
			ScanChunkBytes:   builtinPackScanChunk,
			ScanOverlapBytes: builtinPackScanOverlap,
		},
		Rules: []detectorRule{
			{ID: "credential-aws-access-key", Category: action.DetectorCredential, Severity: severityCritical, Scope: "normalized_text", Kind: detectorRegexp, Pattern: `(?i)\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`},
			{ID: "credential-github-token", Category: action.DetectorCredential, Severity: severityCritical, Scope: "normalized_text", Kind: detectorRegexp, Pattern: `\bgh[pousr]_[A-Za-z0-9]{36,255}\b`},
			{ID: "credential-bearer-token", Category: action.DetectorCredential, Severity: severityHigh, Scope: "normalized_text", Kind: detectorRegexp, Pattern: `(?i)\bbearer[ \t]+[A-Za-z0-9._~+/=-]{20,512}\b`},
			{ID: "secret-private-key", Category: action.DetectorSecret, Severity: severityCritical, Scope: "normalized_text", Kind: detectorRegexp, Pattern: `(?i)-----begin (?:rsa |ec |openssh )?private key-----`},
			{ID: "secret-assignment", Category: action.DetectorSecret, Severity: severityHigh, Scope: "normalized_text", Kind: detectorSecretValue, Pattern: `(?i)\b(?:api[_-]?key|client[_-]?secret|access[_-]?token|password|passwd)\b[ \t]*[:=][ \t]*["']?([A-Za-z0-9._~+/=-]{12,512})`},
			{ID: "pii-email", Category: action.DetectorPIIEmail, Severity: severityModerate, Scope: "normalized_text", Kind: detectorRegexp, Pattern: `(?i)\b[A-Z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]{1,64}@[A-Z0-9](?:[A-Z0-9-]{0,61}[A-Z0-9])?(?:\.[A-Z0-9](?:[A-Z0-9-]{0,61}[A-Z0-9])?){1,10}\b`},
			{ID: "pii-phone", Category: action.DetectorPIIPhone, Severity: severityModerate, Scope: "normalized_text", Kind: detectorRegexp, Pattern: `(?i)(?:\b(?:phone|mobile|tel)\b[ \t]*[:=]?[ \t]*|\+)[0-9][0-9(). -]{6,22}[0-9]`},
			{ID: "pii-payment-card", Category: action.DetectorPIIPaymentCard, Severity: severityHigh, Scope: "normalized_text", Kind: detectorPaymentCard, Pattern: `\b(?:[0-9][ -]?){12,18}[0-9]\b`},
			{ID: "prompt-injection-direct", Category: action.DetectorPromptInjection, Severity: severityHigh, Scope: "confusable_text", Kind: detectorKeyword, Markers: []string{"disregard all prior instructions", "ignore all previous instructions", "ignore previous instructions"}},
			{ID: "role-override", Category: action.DetectorRoleOverride, Severity: severityHigh, Scope: "confusable_text", Kind: detectorKeyword, Markers: []string{"act as the system", "you are now the system", "replace your system instructions"}},
			{ID: "privilege-claim", Category: action.DetectorPrivilegeClaim, Severity: severityHigh, Scope: "confusable_text", Kind: detectorKeyword, Markers: []string{"administrator has approved", "authorized by the system", "policy is disabled"}},
			{ID: "indirect-instruction", Category: action.DetectorIndirectInstruction, Severity: severityModerate, Scope: "confusable_text", Kind: detectorKeyword, Markers: []string{"follow the instructions in this file", "read and execute the instructions", "retrieve and follow the instructions"}},
			{ID: "delimiter-control", Category: action.DetectorDelimiterAttack, Severity: severityHigh, Scope: "normalized_text", Kind: detectorKeyword, Markers: []string{"<|assistant|>", "<|system|>", "[/inst]", "[system]"}},
			{ID: "exfiltration-request", Category: action.DetectorExfiltration, Severity: severityHigh, Scope: "confusable_text", Kind: detectorRegexp, Pattern: `(?is)\b(?:send|upload|post|transmit)\b.{0,80}\b(?:credential|secret|token|private key|environment variable)s?\b`},
		},
	}
}

func BuiltinPackIdentity() string {
	return detectorPackIdentity(builtinPack())
}

func detectorPackIdentity(pack detectorPack) string {
	body, err := json.Marshal(pack)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func ValidateCompiledPlan(plan *action.CompiledPlan) error {
	if plan == nil {
		return fmt.Errorf("compiled action plan is unavailable")
	}
	wantDigest := BuiltinPackIdentity()
	if !action.ValidSHA256Identity(wantDigest) {
		return fmt.Errorf("built-in detector pack identity is unavailable")
	}
	for _, detector := range plan.Detectors() {
		if detector.PackID != action.BuiltinDetectorPackID {
			return fmt.Errorf("detector %q references unsupported pack %q", detector.ID, detector.PackID)
		}
		if detector.PackDigest != wantDigest {
			return fmt.Errorf("detector %q pack digest does not match %s", detector.ID, detector.PackID)
		}
	}
	return nil
}
