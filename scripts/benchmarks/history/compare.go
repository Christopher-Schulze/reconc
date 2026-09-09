package main

import (
	"errors"
	"fmt"
	"strings"
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
			AbsoluteNSPerOp:       0.20,
			AbsoluteBytesPerOp:    0.10,
			AbsoluteAllocsPerOp:   0.10,
			AbsolutePeakRSSBytes:  0.20,
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
	report := BenchmarkComparison{
		FormatVersion: comparisonFormat, SuiteVersion: suiteVersion,
		BaselineEnvironment: baseline.Result.Environment, CurrentEnvironment: current.Environment,
		Compatible: true, Passed: true,
	}
	if issues := compatibilityIssues(baseline.Result, current); len(issues) > 0 {
		report.Compatible = false
		report.Passed = false
		report.CompatibilityIssues = issues
		return report, fmt.Errorf("incompatible benchmark environment: %s", strings.Join(issues, "; "))
	}
	for groupIndex := range current.Groups {
		baselineGroup := baseline.Result.Groups[groupIndex]
		currentGroup := current.Groups[groupIndex]
		for targetIndex := range currentGroup.Targets {
			baselineTarget := baselineGroup.Targets[targetIndex]
			currentTarget := currentGroup.Targets[targetIndex]
			calibrationAbsoluteNS := compareMetric(
				baselineGroup.Calibration.Median.NSPerOp,
				currentGroup.Calibration.Median.NSPerOp,
				baseline.Tolerances.AbsoluteNSPerOp,
			)
			cpuCalibrationAbsoluteNS := compareMetric(
				baselineGroup.CPUCalibration.Median.NSPerOp,
				currentGroup.CPUCalibration.Median.NSPerOp,
				baseline.Tolerances.AbsoluteNSPerOp,
			)
			rawAbsoluteNS := compareMetric(
				baselineTarget.Benchmark.Median.NSPerOp,
				currentTarget.Benchmark.Median.NSPerOp,
				baseline.Tolerances.AbsoluteNSPerOp,
			)
			adjustedCurrentNS, err := cpuAdjustedNSPerOp(
				currentTarget.Benchmark.Median.NSPerOp,
				baselineGroup.CPUCalibration.Median.NSPerOp,
				currentGroup.CPUCalibration.Median.NSPerOp,
			)
			if err != nil {
				return report, fmt.Errorf("adjust %s CPU timing: %w", currentTarget.Benchmark.Name, err)
			}
			comparison := GroupComparison{
				Name:                     currentGroup.Name,
				Benchmark:                currentTarget.Benchmark.Name,
				BaselineAbsolute:         baselineTarget.Benchmark.Median,
				CurrentAbsolute:          currentTarget.Benchmark.Median,
				CalibrationAbsoluteNS:    calibrationAbsoluteNS,
				CPUCalibrationAbsoluteNS: cpuCalibrationAbsoluteNS,
				BaselineP50:              baselineTarget.Benchmark.P50,
				CurrentP50:               currentTarget.Benchmark.P50,
				BaselineP95:              baselineTarget.Benchmark.P95,
				CurrentP95:               currentTarget.Benchmark.P95,
				BaselinePeakRSSBytes:     baselineTarget.Benchmark.PeakRSSBytes,
				CurrentPeakRSSBytes:      currentTarget.Benchmark.PeakRSSBytes,
				NormalizedNSPerOp:        compareMetric(baselineTarget.Normalized.NSPerOp, currentTarget.Normalized.NSPerOp, baseline.Tolerances.NormalizedNSPerOp),
				NormalizedBytesPerOp:     compareMetric(baselineTarget.Normalized.BytesPerOp, currentTarget.Normalized.BytesPerOp, baseline.Tolerances.NormalizedBytesPerOp),
				NormalizedAllocsPerOp:    compareMetric(baselineTarget.Normalized.AllocsPerOp, currentTarget.Normalized.AllocsPerOp, baseline.Tolerances.NormalizedAllocsPerOp),
				RawAbsoluteNSPerOp:       rawAbsoluteNS,
				AbsoluteNSPerOp:          compareMetric(baselineTarget.Benchmark.Median.NSPerOp, adjustedCurrentNS, baseline.Tolerances.AbsoluteNSPerOp),
				AbsoluteBytesPerOp:       compareMetric(baselineTarget.Benchmark.Median.BytesPerOp, currentTarget.Benchmark.Median.BytesPerOp, baseline.Tolerances.AbsoluteBytesPerOp),
				AbsoluteAllocsPerOp:      compareMetric(baselineTarget.Benchmark.Median.AllocsPerOp, currentTarget.Benchmark.Median.AllocsPerOp, baseline.Tolerances.AbsoluteAllocsPerOp),
				AbsolutePeakRSSBytes:     compareMetric(float64(baselineTarget.Benchmark.PeakRSSBytes), float64(currentTarget.Benchmark.PeakRSSBytes), baseline.Tolerances.AbsolutePeakRSSBytes),
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

func cpuAdjustedNSPerOp(current, baselineSentinel, currentSentinel float64) (float64, error) {
	if !finite(current) || !finite(baselineSentinel) || !finite(currentSentinel) ||
		current <= 0 || baselineSentinel <= 0 || currentSentinel <= 0 {
		return 0, errors.New("CPU timing values must be finite and positive")
	}
	adjusted := current * baselineSentinel / currentSentinel
	if !finite(adjusted) || adjusted <= 0 {
		return 0, errors.New("CPU-adjusted timing is outside the finite positive range")
	}
	return adjusted, nil
}

func compatibilityIssues(baseline, current BenchmarkResult) []string {
	checks := []struct {
		name     string
		baseline string
		current  string
	}{
		{"suite", baseline.SuiteVersion, current.SuiteVersion},
		{"Go version", baseline.Environment.GoVersion, current.Environment.GoVersion},
		{"operating system", baseline.Environment.GOOS, current.Environment.GOOS},
		{"architecture", baseline.Environment.GOARCH, current.Environment.GOARCH},
		{"CPU", baseline.Environment.CPU, current.Environment.CPU},
		{"benchtime", baseline.Parameters.Benchtime, current.Parameters.Benchtime},
	}
	issues := make([]string, 0, len(checks)+1)
	for _, check := range checks {
		if check.baseline != check.current {
			issues = append(issues, fmt.Sprintf("%s differs (baseline %q, current %q)", check.name, check.baseline, check.current))
		}
	}
	if baseline.Parameters.Count != current.Parameters.Count {
		issues = append(issues, fmt.Sprintf("sample count differs (baseline %d, current %d)", baseline.Parameters.Count, current.Parameters.Count))
	}
	if baseline.Parameters.CPU != current.Parameters.CPU {
		issues = append(issues, fmt.Sprintf("benchmark CPU parallelism differs (baseline %d, current %d)", baseline.Parameters.CPU, current.Parameters.CPU))
	}
	return issues
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
		{"absolute_ns_per_op", group.AbsoluteNSPerOp},
		{"absolute_bytes_per_op", group.AbsoluteBytesPerOp},
		{"absolute_allocs_per_op", group.AbsoluteAllocsPerOp},
		{"absolute_peak_rss_bytes", group.AbsolutePeakRSSBytes},
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
