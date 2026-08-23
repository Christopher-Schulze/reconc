package runtime

import (
	"reflect"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestEvalSemanticsReachForbidButDoNotForgeCommandEvidence(t *testing.T) {
	const observed = `eval rm -rf '"build cache"'`
	const expected = `rm -rf "build cache"`

	if got := matchingForbiddenCommands([]string{observed}, []string{expected}, "", policy.CommandMatchExact); !reflect.DeepEqual(got, []string{observed}) {
		t.Fatalf("nested forbidden command = %#v", got)
	}
	if got := matchingCommands([]string{observed}, []string{expected}, "", policy.CommandMatchExact); len(got) != 0 {
		t.Fatalf("eval wrapper must not become direct command evidence: %#v", got)
	}
	results := []CommandResult{{Command: observed, Outcome: CommandOutcomeSuccess}}
	if got := matchingCommandResults(results, []string{expected}, CommandOutcomeSuccess, ""); len(got) != 0 {
		t.Fatalf("successful eval wrapper must not forge exact command-success evidence: %#v", got)
	}
}

func TestEvalForbidDistinguishesOuterAndNestedQuoteGrouping(t *testing.T) {
	const grouped = `eval rm -rf '"build cache"'`
	const split = `eval rm -rf "build cache"`

	if got := matchingForbiddenCommands([]string{grouped, split}, []string{`rm -rf "build cache"`}, "", policy.CommandMatchExact); !reflect.DeepEqual(got, []string{grouped}) {
		t.Fatalf("exact nested grouping matches = %#v", got)
	}
	if got := matchingForbiddenCommands([]string{grouped, split}, []string{"rm -rf"}, "", policy.CommandMatchPrefix); !reflect.DeepEqual(got, []string{grouped, split}) {
		t.Fatalf("prefix nested grouping matches = %#v", got)
	}
}
