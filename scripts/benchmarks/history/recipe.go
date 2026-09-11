package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/templates"
)

func runRecipeComparison(baselinePath, resultPath, output, suite, current string, stdout io.Writer) error {
	args := []string{"--baseline", baselinePath, "--result", resultPath, "--output", output, "--suite", suite, "--current", current}
	if err := templates.ValidateRecipeInvocation(policy.RecipeContractPerformance, args, "benchmark compare"); err != nil {
		return err
	}
	if filepath.Clean(output) == filepath.Clean(baselinePath) || filepath.Clean(output) == filepath.Clean(resultPath) {
		return errors.New("recipe comparison output must differ from its inputs")
	}
	baseline, err := readBaseline(baselinePath)
	if err != nil {
		return err
	}
	result, err := readResult(resultPath)
	if err != nil {
		return err
	}
	if result.SuiteVersion != suite || result.Environment.Commit != current || result.Environment.Dirty {
		return errors.New("recipe benchmark result is stale, dirty, or belongs to a different suite")
	}
	report, comparisonErr := compareResults(baseline, result)
	if comparisonErr != nil && !errors.Is(comparisonErr, errRegression) {
		return comparisonErr
	}
	body, err := encodeContract(report)
	if err != nil {
		return err
	}
	if err := publishContract(output, body, io.Discard); err != nil {
		return err
	}
	evidence := benchmarkRecipeEvidence(report, baselinePath, resultPath, output, current, body)
	if err := json.NewEncoder(stdout).Encode(evidence); err != nil {
		return err
	}
	return comparisonErr
}

func benchmarkRecipeEvidence(report BenchmarkComparison, baseline, result, output, current string, body []byte) templates.RecipeEvidence {
	evidence := templates.RecipeEvidence{
		Contract: policy.RecipeContractPerformance, Result: "pass", Current: current,
		Baseline: baseline, BenchmarkResult: result, Comparison: output, Suite: report.SuiteVersion,
		AbsoluteBudgetPass: true, NormalizedBudgetPass: true,
		Evidence: fmt.Sprintf("sha256:%x", sha256.Sum256(body)),
	}
	for _, regression := range report.Regressions {
		if strings.HasPrefix(regression.Metric, "absolute_") {
			evidence.AbsoluteBudgetPass = false
		}
		if strings.HasPrefix(regression.Metric, "normalized_") {
			evidence.NormalizedBudgetPass = false
		}
	}
	if !report.Passed {
		evidence.Result = "block"
	}
	return evidence
}
