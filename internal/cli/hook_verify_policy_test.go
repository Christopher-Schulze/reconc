package cli

import "testing"

func TestLiveHookPolicyMetadataRejectsMissingAndAmbiguousDecisions(t *testing.T) {
	for _, test := range []struct {
		name, body, decision string
		valid                bool
	}{
		{"pass", "duration_ns=100\npolicy_decision=pass\n", "pass", true},
		{"block", "duration_ns=100\npolicy_decision=block\n", "block", true},
		{"unknown", "duration_ns=100\npolicy_decision=unproven\n", "unproven", true},
		{"missing", "duration_ns=100\n", "unproven", false},
		{"negative-time", "duration_ns=-1\npolicy_decision=block\n", "unproven", false},
		{"overflow-time", "duration_ns=999999999999999999999\npolicy_decision=block\n", "unproven", false},
		{"long-time", "duration_ns=60000000001\npolicy_decision=block\n", "unproven", false},
		{"unknown-decision", "duration_ns=100\npolicy_decision=allow\n", "unproven", false},
		{"duplicate-decision", "duration_ns=100\npolicy_decision=pass\npolicy_decision=block\n", "unproven", false},
		{"wrong-field", "duration_ns=100\nexit_code=2\n", "unproven", false},
		{"missing-terminator", "duration_ns=100\npolicy_decision=block", "unproven", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			decision, err := parseLiveHookPolicyProbe([]byte(test.body))
			if (err == nil) != test.valid || decision != test.decision {
				t.Fatalf("decision=%s error=%v", decision, err)
			}
		})
	}
}
