package runtime

import (
	"reflect"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestCommandEvidenceIndexRetainsNormalizedClaimsAndRedirects(t *testing.T) {
	inputs := ExecutionInputs{
		Claims: []string{" ci   green ", "other"},
		CommandResults: []CommandResult{
			{Command: "go test ./... 2>&1", Outcome: CommandOutcomeSuccess},
			{Command: "go test ./... | tail", Outcome: CommandOutcomeSuccess},
		},
	}
	index := newCommandEvidenceIndex(inputs, "")
	if index.results[0].strippedReady || index.results[1].strippedReady {
		t.Fatalf("irrelevant evidence eagerly parsed redirects: %+v", index.results)
	}
	if index.claims[0].normalized != "ci green" {
		t.Fatalf("precomputed claims = %+v", index.claims)
	}
	for attempt := 0; attempt < 2; attempt++ {
		claims := matchingClaimsWithEvidence(index, inputs.Claims, []string{"ci green"})
		if !reflect.DeepEqual(claims, []string{" ci   green "}) {
			t.Fatalf("raw claim spelling lost: %q", claims)
		}
		commands := matchingCommandResultsSinceWithEvidence(index, nil, inputs.CommandResults,
			[]string{"go test ./..."}, CommandOutcomeSuccess, "", 0, policy.CommandMatchExact)
		if !reflect.DeepEqual(commands, []string{"go test ./... 2>&1"}) {
			t.Fatalf("redirect/pipeline result changed: %q", commands)
		}
		if !index.results[0].strippedReady || index.results[0].stripped != "go test ./..." ||
			!index.results[1].strippedReady || index.results[1].stripped != "go test ./... | tail" {
			t.Fatalf("redirect forms were not retained: %+v", index.results)
		}
	}
}
