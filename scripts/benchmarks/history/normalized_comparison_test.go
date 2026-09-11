package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/templates"
)

func TestNormalizedComparisonRequiresTargetDegradation(t *testing.T) {
	for _, test := range []struct {
		name     string
		target   MetricValues
		sentinel float64
		want     []string
	}{
		{"improved target", MetricValues{40, 40, 4}, 100, nil},
		{"unchanged target", MetricValues{50, 50, 5}, 100, nil},
		{"resource boundary", MetricValues{50, 52.5, 5.25}, 100, nil},
		{"resource exceeds normalized only", MetricValues{50, 52.51, 5.251}, 100, []string{"normalized_bytes_per_op", "normalized_allocs_per_op"}},
		{"time boundary", MetricValues{60, 50, 5}, 100, nil},
		{"time exceeds boundary", MetricValues{60.01, 50, 5}, 100, []string{"normalized_ns_per_op", "absolute_ns_per_op"}},
		{"resource absolute boundary", MetricValues{50, 55, 5.5}, 100, []string{"normalized_bytes_per_op", "normalized_allocs_per_op"}},
		{"resource absolute exceeded", MetricValues{50, 55.01, 5.501}, 100, []string{"normalized_bytes_per_op", "normalized_allocs_per_op", "absolute_bytes_per_op", "absolute_allocs_per_op"}},
		{"CPU drift explains raw slowdown", MetricValues{100, 50, 5}, 200, nil},
		{"CPU adjusted slowdown remains", MetricValues{121, 50, 5}, 200, []string{"normalized_ns_per_op", "absolute_ns_per_op"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			baseline, current := comparisonWithImprovedCalibration(t, test.target, test.sentinel)
			report, err := compareResults(baseline, current)
			if (len(test.want) > 0) != errors.Is(err, errRegression) || (len(test.want) == 0 && err != nil) {
				t.Fatalf("comparison error = %v", err)
			}
			assertNormalizedComparison(t, report, test.want)
		})
	}
}

func comparisonWithImprovedCalibration(t *testing.T, targetValues MetricValues, sentinel float64) (BenchmarkBaseline, BenchmarkResult) {
	t.Helper()
	baseline, err := refreshBaseline(syntheticResult())
	if err != nil {
		t.Fatal(err)
	}
	current := syntheticResult()
	group := &current.Groups[0]
	group.Calibration = syntheticStats(group.Calibration.Name, MetricValues{50, 50, 5})
	group.CPUCalibration = syntheticStats(cpuSentinelName, MetricValues{sentinel, 0, 0})
	for index := range group.Targets {
		target := &group.Targets[index]
		if index == 0 {
			target.Benchmark = syntheticStats(target.Benchmark.Name, targetValues)
		}
		target.Normalized, err = normalize(target.Benchmark.Median, group.Calibration.Median)
		if err != nil {
			t.Fatal(err)
		}
	}
	return baseline, current
}

func assertNormalizedComparison(t *testing.T, report BenchmarkComparison, want []string) {
	t.Helper()
	if report.FormatVersion != "reconc.benchmark-comparison/v7" || !report.Compatible || report.Passed != (len(want) == 0) {
		t.Fatalf("comparison status = %s compatible=%t passed=%t", report.FormatVersion, report.Compatible, report.Passed)
	}
	group := report.Groups[0]
	for _, metric := range []MetricComparison{group.NormalizedNSPerOp, group.NormalizedBytesPerOp, group.NormalizedAllocsPerOp} {
		if !metric.Regression || metric.ChangeFraction == nil || *metric.ChangeFraction <= metric.Tolerance {
			t.Fatalf("ratio exceedance disappeared from report: %+v", metric)
		}
	}
	var got []string
	for _, regression := range report.Regressions {
		if regression.Group != group.Name || regression.Benchmark != group.Benchmark {
			t.Fatalf("unmodified target blocked: %+v", regression)
		}
		got = append(got, regression.Metric)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("blocking metrics = %v, want %v", got, want)
	}
}

func TestRecipeComparisonAllowsImprovedTargetAndPreservesRatioEvidence(t *testing.T) {
	t.Chdir(t.TempDir())
	baseline, current := comparisonWithImprovedCalibration(t, MetricValues{40, 40, 4}, 100)
	writeTestContract(t, "baseline.json", baseline)
	writeTestContract(t, "result.json", current)
	args := []string{"--recipe", "--baseline", "baseline.json", "--result", "result.json", "--output", "comparison.json", "--suite", suiteVersion, "--current", current.Environment.Commit}
	var stdout bytes.Buffer
	if err := runCompare(args, &stdout); err != nil {
		t.Fatal(err)
	}
	if err := templates.ValidateRecipeEvidence(policy.RecipeContractPerformance, args, stdout.String(), "pass", "test"); err != nil {
		t.Fatal(err)
	}
	var evidence templates.RecipeEvidence
	if err := json.Unmarshal(stdout.Bytes(), &evidence); err != nil {
		t.Fatal(err)
	}
	if !evidence.AbsoluteBudgetPass || !evidence.NormalizedBudgetPass {
		t.Fatalf("improved target failed recipe budgets: %+v", evidence)
	}
	body, err := os.ReadFile("comparison.json")
	if err != nil {
		t.Fatal(err)
	}
	var report BenchmarkComparison
	if err := decodeStrict(body, &report); err != nil {
		t.Fatal(err)
	}
	assertNormalizedComparison(t, report, nil)
}
