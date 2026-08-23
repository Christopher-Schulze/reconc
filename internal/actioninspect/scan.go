package actioninspect

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"reconc.dev/reconc/internal/action"
)

type compiledDetectorRule struct {
	rule    detectorRule
	pattern *regexp.Regexp
	markers []string
}

type compiledDetectorPack struct {
	identity     string
	rules        []compiledDetectorRule
	chunkBytes   int
	overlapBytes int
}

func compileBuiltinPack() (compiledDetectorPack, error) {
	return compileDetectorPack(builtinPack())
}

func compileDetectorPack(pack detectorPack) (compiledDetectorPack, error) {
	if pack.FormatVersion != builtinPackFormatVersion || pack.ID != action.BuiltinDetectorPackID {
		return compiledDetectorPack{}, fmt.Errorf("built-in detector pack metadata is invalid")
	}
	if pack.Limits.MaxRules != builtinPackMaxRules || pack.Limits.MaxPatternBytes != builtinPackMaxPattern ||
		pack.Limits.MaxMarkers != builtinPackMaxMarkers || pack.Limits.MaxMarkerBytes != builtinPackMaxMarker ||
		pack.Limits.ScanChunkBytes != builtinPackScanChunk || pack.Limits.ScanOverlapBytes != builtinPackScanOverlap {
		return compiledDetectorPack{}, fmt.Errorf("built-in detector pack limits are invalid")
	}
	if len(pack.Rules) == 0 || len(pack.Rules) > pack.Limits.MaxRules {
		return compiledDetectorPack{}, fmt.Errorf("built-in detector pack rule count is invalid")
	}
	compiled := compiledDetectorPack{
		identity: detectorPackIdentity(pack), chunkBytes: pack.Limits.ScanChunkBytes,
		overlapBytes: pack.Limits.ScanOverlapBytes,
	}
	if !action.ValidSHA256Identity(compiled.identity) {
		return compiledDetectorPack{}, fmt.Errorf("built-in detector pack identity is invalid")
	}
	seen := make(map[string]struct{}, len(pack.Rules))
	compiled.rules = make([]compiledDetectorRule, len(pack.Rules))
	for index, rule := range pack.Rules {
		if !action.SafeLabel(rule.ID) || !rule.Category.Valid() ||
			rule.Severity != severityModerate && rule.Severity != severityHigh && rule.Severity != severityCritical ||
			rule.Scope != "normalized_text" && rule.Scope != "confusable_text" {
			return compiledDetectorPack{}, fmt.Errorf("built-in detector rule %d is invalid", index)
		}
		if _, duplicate := seen[rule.ID]; duplicate {
			return compiledDetectorPack{}, fmt.Errorf("built-in detector rule %q is duplicated", rule.ID)
		}
		seen[rule.ID] = struct{}{}
		entry, err := compileDetectorRule(rule)
		if err != nil {
			return compiledDetectorPack{}, err
		}
		compiled.rules[index] = entry
	}
	return compiled, nil
}

func compileDetectorRule(rule detectorRule) (compiledDetectorRule, error) {
	compiled := compiledDetectorRule{rule: rule}
	switch rule.Kind {
	case detectorRegexp, detectorPaymentCard, detectorSecretValue:
		if rule.Pattern == "" || len(rule.Pattern) > builtinPackMaxPattern || len(rule.Markers) != 0 {
			return compiledDetectorRule{}, fmt.Errorf("detector %q pattern material is invalid", rule.ID)
		}
		pattern, err := regexp.Compile(rule.Pattern)
		if err != nil {
			return compiledDetectorRule{}, fmt.Errorf("compile detector %q: %w", rule.ID, err)
		}
		compiled.pattern = pattern
	case detectorKeyword:
		if rule.Pattern != "" || len(rule.Markers) == 0 || len(rule.Markers) > builtinPackMaxMarkers {
			return compiledDetectorRule{}, fmt.Errorf("detector %q marker material is invalid", rule.ID)
		}
		compiled.markers = make([]string, len(rule.Markers))
		seen := make(map[string]struct{}, len(rule.Markers))
		for index, marker := range rule.Markers {
			normalized := inspectionText(marker, rule.Scope == "confusable_text")
			if marker == "" || len(marker) > builtinPackMaxMarker || normalized == "" {
				return compiledDetectorRule{}, fmt.Errorf("detector %q marker %d is invalid", rule.ID, index)
			}
			if _, duplicate := seen[normalized]; duplicate {
				return compiledDetectorRule{}, fmt.Errorf("detector %q marker %d is duplicated", rule.ID, index)
			}
			seen[normalized] = struct{}{}
			compiled.markers[index] = normalized
		}
		sort.Strings(compiled.markers)
	default:
		return compiledDetectorRule{}, fmt.Errorf("detector %q kind is invalid", rule.ID)
	}
	return compiled, nil
}

func (p compiledDetectorPack) scan(
	ctx context.Context,
	text string,
	categories map[action.DetectorCategory]struct{},
	forbiddenTerms []string,
	maxBytes uint64,
) ([]Finding, error) {
	return p.scanTerms(ctx, text, categories, len(forbiddenTerms), func(index int) string {
		return forbiddenTerms[index]
	}, maxBytes)
}

func (p compiledDetectorPack) scanPolicy(
	ctx context.Context,
	text string,
	categories map[action.DetectorCategory]struct{},
	policy action.DetectorPolicyView,
	maxBytes uint64,
) ([]Finding, error) {
	return p.scanTerms(ctx, text, categories, policy.ForbiddenTermCount(), func(index int) string {
		term, _ := policy.ForbiddenTerm(index)
		return term
	}, maxBytes)
}

func (p compiledDetectorPack) scanTerms(
	ctx context.Context,
	text string,
	categories map[action.DetectorCategory]struct{},
	forbiddenTermCount int,
	forbiddenTerm func(int) string,
	maxBytes uint64,
) ([]Finding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	needNormalized, needConfusable := false, false
	for _, rule := range p.rules {
		if _, selected := categories[rule.rule.Category]; !selected {
			continue
		}
		if rule.rule.Scope == "confusable_text" {
			needConfusable = true
		} else {
			needNormalized = true
		}
	}
	if _, selected := categories[action.DetectorForbiddenData]; selected && forbiddenTermCount > 0 {
		needConfusable = true
	}
	var normalized, confusable string
	if needNormalized {
		normalized = inspectionText(text, false)
		if uint64(len(normalized)) > maxBytes {
			return nil, fmt.Errorf("%w: normalized text exceeds byte boundary", errInspectionLimit)
		}
	}
	if needConfusable {
		confusable = inspectionText(text, true)
		if uint64(len(confusable)) > maxBytes {
			return nil, fmt.Errorf("%w: confusable text exceeds byte boundary", errInspectionLimit)
		}
	}
	var findings []Finding
	for _, rule := range p.rules {
		if _, selected := categories[rule.rule.Category]; !selected {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		matched, err := p.detectorMatches(ctx, rule, normalized, confusable)
		if err != nil {
			return nil, err
		}
		if matched {
			findings = append(findings, Finding{RuleID: rule.rule.ID, Category: rule.rule.Category})
		}
	}
	if _, selected := categories[action.DetectorForbiddenData]; selected {
		for index := 0; index < forbiddenTermCount; index++ {
			term := forbiddenTerm(index)
			matched, err := p.matchWindows(ctx, confusable, func(window string) bool {
				return strings.Contains(window, inspectionText(term, true))
			})
			if err != nil {
				return nil, err
			}
			if matched {
				findings = append(findings, Finding{RuleID: "forbidden-data-term", Category: action.DetectorForbiddenData})
				break
			}
		}
	}
	return findings, ctx.Err()
}

func (p compiledDetectorPack) detectorMatches(
	ctx context.Context,
	rule compiledDetectorRule,
	normalized, confusable string,
) (bool, error) {
	text := normalized
	if rule.rule.Scope == "confusable_text" {
		text = confusable
	}
	return p.matchWindows(ctx, text, func(window string) bool {
		return detectorMatchesWindow(rule, window)
	})
}

func (p compiledDetectorPack) matchWindows(
	ctx context.Context,
	text string,
	match func(string) bool,
) (bool, error) {
	for coreStart := 0; ; {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		coreEnd := min(len(text), coreStart+p.chunkBytes)
		windowStart := max(0, coreStart-p.overlapBytes)
		windowEnd := min(len(text), coreEnd+p.overlapBytes)
		windowStart, windowEnd = runeAlignedWindow(text, windowStart, windowEnd)
		if match(text[windowStart:windowEnd]) {
			return true, nil
		}
		if coreEnd == len(text) {
			return false, nil
		}
		coreStart = coreEnd
	}
}

func detectorMatchesWindow(rule compiledDetectorRule, text string) bool {
	if !detectorMayMatch(rule.rule.ID, text) {
		return false
	}
	switch rule.rule.Kind {
	case detectorRegexp:
		return rule.pattern.FindStringIndex(text) != nil
	case detectorKeyword:
		for _, marker := range rule.markers {
			if strings.Contains(text, marker) {
				return true
			}
		}
	case detectorPaymentCard:
		for _, match := range rule.pattern.FindAllString(text, -1) {
			if validPaymentCard(match) {
				return true
			}
		}
	case detectorSecretValue:
		for _, match := range rule.pattern.FindAllStringSubmatch(text, -1) {
			if len(match) == 2 && likelySecretValue(match[1]) {
				return true
			}
		}
	}
	return false
}

func detectorMayMatch(ruleID, text string) bool {
	switch ruleID {
	case "credential-aws-access-key":
		return containsAny(text, "akia", "asia")
	case "credential-github-token":
		return containsAny(text, "ghp_", "gho_", "ghu_", "ghs_", "ghr_")
	case "credential-bearer-token":
		return strings.Contains(text, "bearer")
	case "secret-private-key":
		return strings.Contains(text, "private key")
	case "secret-assignment":
		return containsAny(text, "api_key", "api-key", "apikey", "client_secret", "client-secret",
			"clientsecret", "access_token", "access-token", "accesstoken", "password", "passwd")
	case "pii-email":
		return strings.Contains(text, "@")
	case "pii-phone":
		return containsAny(text, "+", "phone", "mobile", "tel")
	case "pii-payment-card":
		return containsASCIIDigit(text)
	case "exfiltration-request":
		return containsAny(text, "send", "upload", "post", "transmit") &&
			containsAny(text, "credential", "secret", "token", "private key", "environment variable")
	default:
		return true
	}
}

func containsAny(text string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func containsASCIIDigit(text string) bool {
	for index := range text {
		if text[index] >= '0' && text[index] <= '9' {
			return true
		}
	}
	return false
}

func runeAlignedWindow(value string, start, end int) (int, int) {
	for start > 0 && !utf8.RuneStart(value[start]) {
		start--
	}
	for end < len(value) && !utf8.RuneStart(value[end]) {
		end++
	}
	return start, end
}

func likelySecretValue(value string) bool {
	if len(value) < 12 || len(value) > 512 {
		return false
	}
	var ascii [2]uint64
	var nonASCII map[rune]struct{}
	distinct := 0
	hasLetter := false
	hasDigit := false
	hasEncodedSymbol := false
	for _, character := range value {
		if character >= 0 && character < 128 {
			word := character / 64
			bit := uint64(1) << (character % 64)
			if ascii[word]&bit == 0 {
				ascii[word] |= bit
				distinct++
			}
		} else {
			if nonASCII == nil {
				nonASCII = make(map[rune]struct{})
			}
			if _, exists := nonASCII[character]; !exists {
				nonASCII[character] = struct{}{}
				distinct++
			}
		}
		switch {
		case character >= 'a' && character <= 'z':
			hasLetter = true
		case character >= '0' && character <= '9':
			hasDigit = true
		case strings.ContainsRune("._~+/=", character):
			hasEncodedSymbol = true
		}
	}
	if distinct < 6 || !hasLetter {
		return false
	}
	return hasDigit || (hasEncodedSymbol && len(value) >= 20)
}

func inspectionText(value string, confusable bool) string {
	normalized := strings.ToLower(norm.NFKC.String(value))
	if !confusable {
		return normalized
	}
	return strings.Map(confusableRune, norm.NFKD.String(normalized))
}

// confusableSkeleton is deliberately bounded to compatibility forms and the
// cross-script characters needed by the built-in detector vocabulary.
var confusableSkeleton = map[rune]rune{
	'а': 'a', 'α': 'a',
	'е': 'e', 'ε': 'e',
	'і': 'i', 'ι': 'i',
	'к': 'k', 'κ': 'k',
	'м': 'm', 'μ': 'm',
	'о': 'o', 'ο': 'o',
	'р': 'p', 'ρ': 'p',
	'с': 'c',
	'т': 't', 'τ': 't',
	'х': 'x', 'χ': 'x',
	'у': 'y',
	'ѕ': 's', 'ꜱ': 's',
}

func confusableRune(value rune) rune {
	if unicode.Is(unicode.Cf, value) || unicode.Is(unicode.Mn, value) {
		return -1
	}
	if skeleton, ok := confusableSkeleton[value]; ok {
		return skeleton
	}
	return value
}

func validPaymentCard(value string) bool {
	digits := make([]byte, 0, 19)
	for _, character := range value {
		if unicode.IsDigit(character) && character >= '0' && character <= '9' {
			digits = append(digits, byte(character))
		}
	}
	if len(digits) < 13 || len(digits) > 19 || allBytesEqual(digits) {
		return false
	}
	sum := 0
	parity := len(digits) % 2
	for index, digit := range digits {
		value := int(digit - '0')
		if index%2 == parity {
			value *= 2
			if value > 9 {
				value -= 9
			}
		}
		sum += value
	}
	return sum%10 == 0
}

func allBytesEqual(value []byte) bool {
	for index := 1; index < len(value); index++ {
		if value[index] != value[0] {
			return false
		}
	}
	return true
}
