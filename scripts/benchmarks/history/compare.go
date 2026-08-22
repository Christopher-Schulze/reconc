package main

import (
	"errors"
	"fmt"
)

var errRegression = errors.New("benchmark regression detected")

func refreshBaseline(result BenchmarkResult) (BenchmarkBaseline, error) {
	if err := validateResult(result); err != nil {
		return BenchmarkBaseline{}, err
	}
	baseline := BenchmarkBaseline{
		FormatVersion: baselineFormat,
		Tolerances: Tolerances{
			NormalizedNSPerOp:     0.20,
			NormalizedBytesPerOp:  0.05,
			NormalizedAllocsPerOp: 0.05,
		},
		Result: result,
	}
	return baseline, validateBaseline(baseline)
}

func compareResults(baseline BenchmarkBaseline, current BenchmarkResult) (BenchmarkComparison, error) {
	if err := validateBaseline(baseline); err != nil {
		return BenchmarkComparison{}, err
	}
	if err := validateResult(current); err != nil {
		return BenchmarkComparison{}, err
	}
	if err := compatibleResults(baseline.Result, current); err != nil {
		return BenchmarkComparison{}, err
	}
	report := BenchmarkComparison{
		FormatVersion: comparisonFormat, SuiteVersion: suiteVersion,
		BaselineEnvironment: baseline.Result.Environment, CurrentEnvironment: current.Environment,
		Compatible: true, Passed: true,
	}
	for groupIndex := range current.Groups {
		baselineGroup := baseline.Result.Groups[groupIndex]
		currentGroup := current.Groups[groupIndex]
		for targetIndex := range currentGroup.Targets {
			baselineTarget := baselineGroup.Targets[targetIndex]
			currentTarget := currentGroup.Targets[targetIndex]
			comparison := GroupComparison{
				Name: currentGroup.Name, Benchmark: currentTarget.Benchmark.Name,
				BaselineAbsolute:      baselineTarget.Benchmark.Median,
				CurrentAbsolute:       currentTarget.Benchmark.Median,
				NormalizedNSPerOp:     compareMetric(baselineTarget.Normalized.NSPerOp, currentTarget.Normalized.NSPerOp, baseline.Tolerances.NormalizedNSPerOp),
				NormalizedBytesPerOp:  compareMetric(baselineTarget.Normalized.BytesPerOp, currentTarget.Normalized.BytesPerOp, baseline.Tolerances.NormalizedBytesPerOp),
				NormalizedAllocsPerOp: compareMetric(baselineTarget.Normalized.AllocsPerOp, currentTarget.Normalized.AllocsPerOp, baseline.Tolerances.NormalizedAllocsPerOp),
			}
			report.Groups = append(report.Groups, comparison)
			appendRegressions(&report, comparison)
		}
	}
	report.Passed = len(report.Regressions) == 0
	if !report.Passed {
		return report, errRegression
	}
	return report, nil
}

func compatibleResults(baseline, current BenchmarkResult) error {
	checks := []struct {
		name     string
		baseline string
		current  string
	}{
		{"suite", baseline.SuiteVersion, current.SuiteVersion},
		{"Go version", baseline.Environment.GoVersion, current.Environment.GoVersion},
		{"operating system", baseline.Environment.GOOS, current.Environment.GOOS},
		{"architecture", baseline.Environment.GOARCH, current.Environment.GOARCH},
		{"benchtime", baseline.Parameters.Benchtime, current.Parameters.Benchtime},
	}
	for _, check := range checks {
		if check.baseline != check.current {
			return fmt.Errorf("incompatible benchmark %s: baseline %q, current %q", check.name, check.baseline, check.current)
		}
	}
	if baseline.Parameters.Count != current.Parameters.Count || baseline.Parameters.CPU != current.Parameters.CPU {
		return errors.New("incompatible benchmark count or CPU parameter")
	}
	return nil
}

func compareMetric(baseline, current, tolerance float64) MetricComparison {
	comparison := MetricComparison{Baseline: baseline, Current: current, Tolerance: tolerance}
	if baseline == 0 {
		comparison.Regression = current > 0
		return comparison
	}
	change := current/baseline - 1
	comparison.ChangeFraction = &change
	comparison.Regression = change > tolerance && !nearlyEqual(change, tolerance)
	return comparison
}

func appendRegressions(report *BenchmarkComparison, group GroupComparison) {
	metrics := []struct {
		name       string
		comparison MetricComparison
	}{
		{"normalized_ns_per_op", group.NormalizedNSPerOp},
		{"normalized_bytes_per_op", group.NormalizedBytesPerOp},
		{"normalized_allocs_per_op", group.NormalizedAllocsPerOp},
	}
	for _, metric := range metrics {
		if metric.comparison.Regression {
			report.Regressions = append(report.Regressions, Regression{
				Group: group.Name, Benchmark: group.Benchmark, Metric: metric.name,
				Baseline: metric.comparison.Baseline, Current: metric.comparison.Current,
				Tolerance: metric.comparison.Tolerance,
			})
		}
	}
}
