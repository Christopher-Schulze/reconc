package actioninspect

import (
	"context"
	"fmt"
	"sort"
	"unicode/utf8"

	"reconc.dev/reconc/internal/action"
)

// TextScanner shares the immutable built-in detector programs for privacy-safe
// local classification outside the full action evaluator.
type TextScanner struct {
	pack compiledDetectorPack
}

func NewTextScanner() (*TextScanner, error) {
	programs, err := loadBuiltinDetectorPrograms()
	if err != nil {
		return nil, err
	}
	return &programs.scanner, nil
}

func (s *TextScanner) PrivateCategories(
	ctx context.Context,
	text string,
	maxBytes uint64,
) ([]action.DetectorCategory, error) {
	return s.categories(ctx, text, maxBytes, map[action.DetectorCategory]struct{}{
		action.DetectorCredential:     {},
		action.DetectorSecret:         {},
		action.DetectorPIIEmail:       {},
		action.DetectorPIIPhone:       {},
		action.DetectorPIIPaymentCard: {},
	})
}

// UntrustedInstructionCategories classifies deterministic instruction and
// exfiltration markers in untrusted public metadata. It deliberately excludes
// PII detectors so ordinary tool documentation and contact text do not become
// an accidental availability policy.
func (s *TextScanner) UntrustedInstructionCategories(
	ctx context.Context,
	text string,
	maxBytes uint64,
) ([]action.DetectorCategory, error) {
	return s.categories(ctx, text, maxBytes, map[action.DetectorCategory]struct{}{
		action.DetectorCredential:          {},
		action.DetectorSecret:              {},
		action.DetectorPromptInjection:     {},
		action.DetectorRoleOverride:        {},
		action.DetectorPrivilegeClaim:      {},
		action.DetectorIndirectInstruction: {},
		action.DetectorDelimiterAttack:     {},
		action.DetectorExfiltration:        {},
	})
}

func (s *TextScanner) categories(
	ctx context.Context,
	text string,
	maxBytes uint64,
	categories map[action.DetectorCategory]struct{},
) ([]action.DetectorCategory, error) {
	if s == nil || len(s.pack.rules) == 0 {
		return nil, fmt.Errorf("text scanner is unavailable")
	}
	if !utf8.ValidString(text) || maxBytes == 0 || maxBytes > action.MaxArgumentBytes {
		return nil, fmt.Errorf("text scanner input is outside its boundary")
	}
	findings, err := s.pack.scan(ctx, text, categories, nil, maxBytes)
	if err != nil {
		return nil, err
	}
	seen := make(map[action.DetectorCategory]struct{}, len(findings))
	out := make([]action.DetectorCategory, 0, len(findings))
	for _, finding := range findings {
		if _, duplicate := seen[finding.Category]; duplicate {
			continue
		}
		seen[finding.Category] = struct{}{}
		out = append(out, finding.Category)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}
