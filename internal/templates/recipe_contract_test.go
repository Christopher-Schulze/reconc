package templates

import (
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestRecipeInvocationRejectsAmbiguousArguments(t *testing.T) {
	const identity = "0123456789abcdef0123456789abcdef01234567"
	for _, test := range []struct {
		name     string
		contract policy.RecipeContract
		args     []string
	}{
		{"flags after terminator", policy.RecipeContractPublicAPI, []string{"--", "--base", identity, "--current", identity}},
		{"duplicate identity", policy.RecipeContractPublicAPI, []string{"--base", identity, "--base", identity, "--current", identity}},
		{"whitespace flag", policy.RecipeContractPublicAPI, []string{" --base", identity, "--current", identity}},
		{"separate false isolation", policy.RecipeContractSchemaMigration, []string{"--engine", "sqlite", "--database", "test.db", "--forward", "--rollback", "--isolated", "false"}},
		{"conflicting rollback policy", policy.RecipeContractSchemaMigration, []string{"--engine", "sqlite", "--database", "test.db", "--forward", "--rollback", "--rollback-not-supported", "--isolated"}},
		{"escaped second output", policy.RecipeContractGenerated, []string{"--source", identity, "--outputs", "generated/a,/outside"}},
		{"duplicate output", policy.RecipeContractGenerated, []string{"--source", identity, "--outputs", "generated/a,generated/a"}},
		{"empty output", policy.RecipeContractGenerated, []string{"--source", identity, "--outputs", "generated/a,"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateRecipeInvocation(test.contract, test.args, test.name); err == nil {
				t.Fatal("ambiguous invocation accepted")
			}
		})
	}
}

func TestMigrationEvidenceHonorsDeclaredRollbackPolicy(t *testing.T) {
	for _, test := range []struct {
		flag  string
		valid bool
	}{
		{"--rollback", false}, {"--rollback-required", false}, {"--rollback-not-supported", true},
	} {
		t.Run(test.flag, func(t *testing.T) {
			args := []string{"--engine", "sqlite", "--database", "test.db", "--forward", test.flag, "--isolated"}
			body := `{"contract":"schema-migration-safety","result":"pass","engine":"sqlite","database":"test.db","forward":true,"rollback":false,"isolated":true,"evidence":"forward-invariants"}`
			if err := ValidateRecipeEvidence(policy.RecipeContractSchemaMigration, args, body, "pass", "migration"); (err == nil) != test.valid {
				t.Fatalf("rollback policy %s: %v", test.flag, err)
			}
		})
	}
}

func TestRecipeEvidenceRejectsDuplicateDisposition(t *testing.T) {
	const identity = "0123456789abcdef0123456789abcdef01234567"
	args := []string{"--base", identity, "--current", identity}
	body := `{"contract":"public-api-compatibility","result":"block","result":"pass","base":"` + identity + `","current":"` + identity + `","evidence":"comparison"}`
	if err := ValidateRecipeEvidence(policy.RecipeContractPublicAPI, args, body, "pass", "api"); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate result accepted: %v", err)
	}
}

func TestRecipeFailedBudgetsRemainValidBlockingEvidence(t *testing.T) {
	const current = "0123456789abcdef0123456789abcdef01234567"
	args := []string{"--baseline", "baseline.json", "--result", "result.json", "--output", "comparison.json", "--suite", "suite-v10", "--current", current}
	for _, disposition := range []string{"block", "pass"} {
		t.Run(disposition, func(t *testing.T) {
			body := `{"contract":"performance-budget","result":"` + disposition + `","current":"` + current + `","baseline":"baseline.json","benchmark_result":"result.json","comparison":"comparison.json","suite":"suite-v10","absolute_budget_pass":false,"normalized_budget_pass":true,"evidence":"allocation regression"}`
			err := ValidateRecipeEvidence(policy.RecipeContractPerformance, args, body, disposition, "performance")
			if (err == nil) != (disposition == "block") {
				t.Fatalf("failed budget disposition %s: %v", disposition, err)
			}
		})
	}
}
