package lockdiff

import (
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestIndexRulesUsesCanonicalPolicyModes(t *testing.T) {
	valid := []policy.Mode{policy.ModeObserve, policy.ModeWarn, policy.ModeBlock, policy.ModeFix}
	for _, mode := range valid {
		t.Run(string(mode), func(t *testing.T) {
			if _, err := indexRules(lockdiffModeFixture(string(mode))); err != nil {
				t.Fatalf("canonical mode %q rejected: %v", mode, err)
			}
		})
	}

	for _, mode := range []interface{}{"", " warn", "warning", 1, nil} {
		payload := lockdiffModeFixture(mode)
		if _, err := indexRules(payload); err == nil {
			t.Fatalf("invalid mode %#v accepted", mode)
		}
	}
}

func lockdiffModeFixture(mode interface{}) map[string]interface{} {
	return map[string]interface{}{
		"default_mode": mode,
		"rule_count":   0,
		"rules":        []interface{}{},
		"source_count": 0,
		"sources":      []interface{}{},
	}
}
