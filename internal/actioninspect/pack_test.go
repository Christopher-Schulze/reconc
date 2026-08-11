package actioninspect

import (
	"strings"
	"testing"

	"reconc.dev/reconc/internal/action"
)

func TestBuiltinDetectorPackFormatAndLimits(t *testing.T) {
	t.Parallel()
	pack := builtinPack()
	if pack.FormatVersion != builtinPackFormatVersion || pack.ID != action.BuiltinDetectorPackID {
		t.Fatalf("pack metadata = %#v", pack)
	}
	wantLimits := detectorPackLimits{
		MaxRules:         builtinPackMaxRules,
		MaxPatternBytes:  builtinPackMaxPattern,
		MaxMarkers:       builtinPackMaxMarkers,
		MaxMarkerBytes:   builtinPackMaxMarker,
		ScanChunkBytes:   builtinPackScanChunk,
		ScanOverlapBytes: builtinPackScanOverlap,
	}
	if pack.Limits != wantLimits || len(pack.Rules) != 14 {
		t.Fatalf("pack limits = %#v, rule count = %d", pack.Limits, len(pack.Rules))
	}
	compiled, err := compileDetectorPack(pack)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.identity != BuiltinPackIdentity() || len(compiled.rules) != len(pack.Rules) {
		t.Fatalf("compiled pack identity = %q, rules = %d", compiled.identity, len(compiled.rules))
	}
}

func TestDetectorPackIdentityCoversRulesAndLimits(t *testing.T) {
	t.Parallel()
	baseline := builtinPack()
	mutations := []struct {
		name   string
		mutate func(*detectorPack)
	}{
		{name: "rule removed", mutate: func(pack *detectorPack) { pack.Rules = pack.Rules[:len(pack.Rules)-1] }},
		{name: "rule changed", mutate: func(pack *detectorPack) { pack.Rules[0].Severity = severityHigh }},
		{name: "limit changed", mutate: func(pack *detectorPack) { pack.Limits.MaxPatternBytes-- }},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			mutated := baseline
			mutated.Rules = append([]detectorRule(nil), baseline.Rules...)
			test.mutate(&mutated)
			if got := detectorPackIdentity(mutated); got == BuiltinPackIdentity() {
				t.Fatalf("mutation retained pack identity %q", got)
			}
		})
	}
}

func TestDetectorPackRejectsInvalidFormat(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*detectorPack)
	}{
		{name: "format", mutate: func(pack *detectorPack) { pack.FormatVersion = "2" }},
		{name: "identifier", mutate: func(pack *detectorPack) { pack.ID = "other" }},
		{name: "limits", mutate: func(pack *detectorPack) { pack.Limits.MaxRules-- }},
		{name: "empty", mutate: func(pack *detectorPack) { pack.Rules = nil }},
		{name: "duplicate", mutate: func(pack *detectorPack) { pack.Rules[1].ID = pack.Rules[0].ID }},
		{name: "category", mutate: func(pack *detectorPack) { pack.Rules[0].Category = action.DetectorCategory("other") }},
		{name: "severity", mutate: func(pack *detectorPack) { pack.Rules[0].Severity = detectorSeverity("other") }},
		{name: "scope", mutate: func(pack *detectorPack) { pack.Rules[0].Scope = "other" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pack := builtinPack()
			pack.Rules = append([]detectorRule(nil), pack.Rules...)
			test.mutate(&pack)
			if _, err := compileDetectorPack(pack); err == nil {
				t.Fatal("invalid detector pack compiled")
			}
		})
	}
}

func TestDetectorRuleRejectsInvalidMaterial(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		rule detectorRule
	}{
		{name: "empty regexp", rule: detectorRule{ID: "rule", Kind: detectorRegexp}},
		{name: "oversized regexp", rule: detectorRule{ID: "rule", Kind: detectorRegexp, Pattern: strings.Repeat("a", builtinPackMaxPattern+1)}},
		{name: "regexp markers", rule: detectorRule{ID: "rule", Kind: detectorRegexp, Pattern: "x", Markers: []string{"x"}}},
		{name: "empty keyword", rule: detectorRule{ID: "rule", Kind: detectorKeyword}},
		{name: "keyword pattern", rule: detectorRule{ID: "rule", Kind: detectorKeyword, Pattern: "x", Markers: []string{"x"}}},
		{name: "oversized marker", rule: detectorRule{ID: "rule", Kind: detectorKeyword, Markers: []string{strings.Repeat("a", builtinPackMaxMarker+1)}}},
		{name: "duplicate marker", rule: detectorRule{ID: "rule", Kind: detectorKeyword, Markers: []string{"Ignore", "ignore"}}},
		{name: "unknown kind", rule: detectorRule{ID: "rule", Kind: detectorKind("other")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := compileDetectorRule(test.rule); err == nil {
				t.Fatal("invalid detector rule compiled")
			}
		})
	}
}

func TestLikelySecretValueUsesBoundedContext(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "mixed synthetic", value: "q7m9v2p4r8x6l3n5", want: true},
		{name: "long hex", value: "0123456789abcdef0123456789abcdef", want: true},
		{name: "encoded synthetic", value: "abcdefghijklmnop/qrstuv", want: true},
		{name: "descriptive phrase", value: "must-have-twelve-characters", want: false},
		{name: "repeated", value: "00000000000000000000", want: false},
		{name: "short", value: "a1b2c3", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := likelySecretValue(test.value); got != test.want {
				t.Fatalf("likelySecretValue() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestDetectorPrefiltersRetainEveryBuiltInPositiveFixture(t *testing.T) {
	t.Parallel()
	fixtures := loadDetectorCorpus(t)
	pack, err := compileBuiltinPack()
	if err != nil {
		t.Fatal(err)
	}
	rules := make(map[string]compiledDetectorRule, len(pack.rules))
	for _, rule := range pack.rules {
		rules[rule.rule.ID] = rule
	}
	for _, fixture := range fixtures {
		for _, ruleID := range fixture.WantRuleIDs {
			rule, ok := rules[ruleID]
			if !ok {
				continue
			}
			text := inspectionText(fixture.Text, rule.rule.Scope == "confusable_text")
			if !detectorMayMatch(ruleID, text) {
				t.Fatalf("detector prefilter rejected positive fixture %q for %q", fixture.Name, ruleID)
			}
		}
	}
}
