package assurance

import (
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func FuzzCommandPolicyEvaluationRejectsUnknownValues(f *testing.F) {
	for _, seed := range []string{"all", "any", "", "unknown", "ALL", " all "} {
		f.Add(seed)
	}
	root := f.TempDir()
	f.Fuzz(func(t *testing.T, commandPolicy string) {
		gate := policy.AssuranceGate{
			ID: "live", Type: policy.AssuranceLiveVerification,
			Commands: []string{"go test ./..."}, CommandPolicy: commandPolicy,
		}
		_, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{SuccessfulCommands: []string{"go test ./..."}})
		valid := commandPolicy == "all" || commandPolicy == "any"
		if valid == (err != nil) {
			t.Fatalf("command_policy %q validity drift: %v", commandPolicy, err)
		}
	})
}
