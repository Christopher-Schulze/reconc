package main

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"

	"reconc.dev/reconc/internal/boundedio"
)

const (
	resultFormat     = "reconc.benchmark-result/v2"
	baselineFormat   = "reconc.benchmark-baseline/v2"
	comparisonFormat = "reconc.benchmark-comparison/v2"
	suiteVersion     = "reconc.performance-history/v10"
	maxContractBytes = 4 << 20
)

type Environment struct {
	GoVersion string `json:"go_version"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	CPU       string `json:"cpu"`
	Commit    string `json:"commit"`
	Dirty     bool   `json:"dirty"`
}

type Parameters struct {
	Count     int    `json:"count"`
	Benchtime string `json:"benchtime"`
	CPU       int    `json:"cpu"`
}

type MetricValues struct {
	NSPerOp     float64 `json:"ns_per_op"`
	BytesPerOp  float64 `json:"bytes_per_op"`
	AllocsPerOp float64 `json:"allocs_per_op"`
}

type MetricSample struct {
	Iterations   uint64 `json:"iterations"`
	PeakRSSBytes uint64 `json:"peak_rss_bytes"`
	MetricValues
}

type BenchmarkStats struct {
	Name         string         `json:"name"`
	Samples      []MetricSample `json:"samples"`
	Median       MetricValues   `json:"median"`
	P50          MetricValues   `json:"p50"`
	P95          MetricValues   `json:"p95"`
	PeakRSSBytes uint64         `json:"peak_rss_bytes"`
}

type TargetResult struct {
	Benchmark  BenchmarkStats `json:"benchmark"`
	Normalized MetricValues   `json:"normalized"`
}

type GroupResult struct {
	Name        string         `json:"name"`
	Package     string         `json:"package"`
	Calibration BenchmarkStats `json:"calibration"`
	Targets     []TargetResult `json:"targets"`
}

type BenchmarkResult struct {
	FormatVersion string        `json:"format_version"`
	SuiteVersion  string        `json:"suite_version"`
	Environment   Environment   `json:"environment"`
	Parameters    Parameters    `json:"parameters"`
	Groups        []GroupResult `json:"groups"`
}

type Tolerances struct {
	NormalizedNSPerOp     float64 `json:"normalized_ns_per_op"`
	NormalizedBytesPerOp  float64 `json:"normalized_bytes_per_op"`
	NormalizedAllocsPerOp float64 `json:"normalized_allocs_per_op"`
	AbsoluteNSPerOp       float64 `json:"absolute_ns_per_op"`
	AbsoluteBytesPerOp    float64 `json:"absolute_bytes_per_op"`
	AbsoluteAllocsPerOp   float64 `json:"absolute_allocs_per_op"`
	AbsolutePeakRSSBytes  float64 `json:"absolute_peak_rss_bytes"`
}

type BenchmarkBaseline struct {
	FormatVersion string          `json:"format_version"`
	Tolerances    Tolerances      `json:"tolerances"`
	Result        BenchmarkResult `json:"result"`
}

type MetricComparison struct {
	Baseline       float64  `json:"baseline"`
	Current        float64  `json:"current"`
	ChangeFraction *float64 `json:"change_fraction"`
	Tolerance      float64  `json:"tolerance"`
	Regression     bool     `json:"regression"`
}

type GroupComparison struct {
	Name                  string           `json:"name"`
	Benchmark             string           `json:"benchmark"`
	BaselineAbsolute      MetricValues     `json:"baseline_absolute"`
	CurrentAbsolute       MetricValues     `json:"current_absolute"`
	BaselineP50           MetricValues     `json:"baseline_p50"`
	CurrentP50            MetricValues     `json:"current_p50"`
	BaselineP95           MetricValues     `json:"baseline_p95"`
	CurrentP95            MetricValues     `json:"current_p95"`
	BaselinePeakRSSBytes  uint64           `json:"baseline_peak_rss_bytes"`
	CurrentPeakRSSBytes   uint64           `json:"current_peak_rss_bytes"`
	NormalizedNSPerOp     MetricComparison `json:"normalized_ns_per_op"`
	NormalizedBytesPerOp  MetricComparison `json:"normalized_bytes_per_op"`
	NormalizedAllocsPerOp MetricComparison `json:"normalized_allocs_per_op"`
	AbsoluteNSPerOp       MetricComparison `json:"absolute_ns_per_op"`
	AbsoluteBytesPerOp    MetricComparison `json:"absolute_bytes_per_op"`
	AbsoluteAllocsPerOp   MetricComparison `json:"absolute_allocs_per_op"`
	AbsolutePeakRSSBytes  MetricComparison `json:"absolute_peak_rss_bytes"`
}

type Regression struct {
	Group     string  `json:"group"`
	Benchmark string  `json:"benchmark"`
	Metric    string  `json:"metric"`
	Baseline  float64 `json:"baseline"`
	Current   float64 `json:"current"`
	Tolerance float64 `json:"tolerance"`
}

type BenchmarkComparison struct {
	FormatVersion       string            `json:"format_version"`
	SuiteVersion        string            `json:"suite_version"`
	BaselineEnvironment Environment       `json:"baseline_environment"`
	CurrentEnvironment  Environment       `json:"current_environment"`
	Compatible          bool              `json:"compatible"`
	Passed              bool              `json:"passed"`
	CompatibilityIssues []string          `json:"compatibility_issues,omitempty"`
	Groups              []GroupComparison `json:"groups"`
	Regressions         []Regression      `json:"regressions"`
}

func encodeContract(value any) ([]byte, error) {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	body = append(body, '\n')
	if len(body) > maxContractBytes {
		return nil, fmt.Errorf("benchmark contract exceeds %d bytes", maxContractBytes)
	}
	return body, nil
}

func readResult(path string) (BenchmarkResult, error) {
	body, err := boundedio.ReadRegularFile(path, maxContractBytes)
	if err != nil {
		return BenchmarkResult{}, err
	}
	var result BenchmarkResult
	if err := decodeStrict(body, &result); err != nil {
		return BenchmarkResult{}, err
	}
	return result, validateResult(result)
}

func readBaseline(path string) (BenchmarkBaseline, error) {
	body, err := boundedio.ReadRegularFile(path, maxContractBytes)
	if err != nil {
		return BenchmarkBaseline{}, err
	}
	var baseline BenchmarkBaseline
	if err := decodeStrict(body, &baseline); err != nil {
		return BenchmarkBaseline{}, err
	}
	return baseline, validateBaseline(baseline)
}

func decodeStrict(body []byte, target any) error {
	if len(body) == 0 || len(body) > maxContractBytes {
		return fmt.Errorf("benchmark contract size is outside 1..%d bytes", maxContractBytes)
	}
	strict := jsontext.NewDecoder(bytes.NewReader(body))
	if _, err := strict.ReadValue(); err != nil {
		return fmt.Errorf("invalid benchmark JSON: %w", err)
	}
	if strict.PeekKind() != jsontext.KindInvalid {
		return errors.New("benchmark JSON has trailing data")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode benchmark contract: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("benchmark JSON has trailing data")
	}
	return nil
}

func validateBaseline(baseline BenchmarkBaseline) error {
	if baseline.FormatVersion != baselineFormat {
		return fmt.Errorf("unsupported benchmark baseline format %q", baseline.FormatVersion)
	}
	for name, value := range map[string]float64{
		"normalized_ns_per_op":     baseline.Tolerances.NormalizedNSPerOp,
		"normalized_bytes_per_op":  baseline.Tolerances.NormalizedBytesPerOp,
		"normalized_allocs_per_op": baseline.Tolerances.NormalizedAllocsPerOp,
		"absolute_ns_per_op":       baseline.Tolerances.AbsoluteNSPerOp,
		"absolute_bytes_per_op":    baseline.Tolerances.AbsoluteBytesPerOp,
		"absolute_allocs_per_op":   baseline.Tolerances.AbsoluteAllocsPerOp,
		"absolute_peak_rss_bytes":  baseline.Tolerances.AbsolutePeakRSSBytes,
	} {
		if !finite(value) || value < 0 || value > 1 {
			return fmt.Errorf("benchmark tolerance %s is outside 0..1", name)
		}
	}
	if baseline.Result.Environment.Dirty {
		return errors.New("benchmark baseline must reference a clean source tree")
	}
	return validateResult(baseline.Result)
}

func validateResult(result BenchmarkResult) error {
	if result.FormatVersion != resultFormat || result.SuiteVersion != suiteVersion {
		return errors.New("benchmark result format or suite is unsupported")
	}
	if result.Environment.GoVersion == "" || result.Environment.GOOS == "" || result.Environment.GOARCH == "" ||
		result.Environment.CPU == "" || result.Environment.Commit == "" {
		return errors.New("benchmark environment is incomplete")
	}
	if result.Parameters.Count < 1 || result.Parameters.Count > 20 || !validBenchtime(result.Parameters.Benchtime) || result.Parameters.CPU != 1 {
		return errors.New("benchmark parameters are invalid")
	}
	if len(result.Groups) != len(benchmarkSuite) {
		return fmt.Errorf("benchmark result has %d groups, want %d", len(result.Groups), len(benchmarkSuite))
	}
	for index, spec := range benchmarkSuite {
		group := result.Groups[index]
		if group.Name != spec.Name || group.Package != spec.Package || group.Calibration.Name != spec.Calibration || len(group.Targets) != len(spec.Targets) {
			return fmt.Errorf("benchmark group %d is incompatible with suite %s", index, suiteVersion)
		}
		if err := validateStats(group.Calibration, result.Parameters.Count); err != nil {
			return fmt.Errorf("calibration %s: %w", spec.Calibration, err)
		}
		for targetIndex, targetName := range spec.Targets {
			target := group.Targets[targetIndex]
			if target.Benchmark.Name != targetName {
				return fmt.Errorf("benchmark target %d in %s is %q, want %q", targetIndex, group.Name, target.Benchmark.Name, targetName)
			}
			if err := validateStats(target.Benchmark, result.Parameters.Count); err != nil {
				return fmt.Errorf("target %s: %w", targetName, err)
			}
			want, err := normalize(target.Benchmark.Median, group.Calibration.Median)
			if err != nil || !metricValuesEqual(want, target.Normalized) {
				return fmt.Errorf("target %s has invalid normalized metrics", targetName)
			}
		}
	}
	return nil
}

func validateStats(stats BenchmarkStats, count int) error {
	if stats.Name == "" || len(stats.Samples) != count {
		return errors.New("benchmark samples are incomplete")
	}
	for _, sample := range stats.Samples {
		if sample.Iterations == 0 || !validMetricValues(sample.MetricValues) {
			return errors.New("benchmark sample is invalid")
		}
	}
	if err := validateMedian(stats); err != nil {
		return err
	}
	wantP50 := percentileMetrics(stats.Samples, 0.50)
	if !metricValuesEqual(wantP50, stats.P50) {
		return errors.New("benchmark p50 does not match samples")
	}
	wantP95 := percentileMetrics(stats.Samples, 0.95)
	if !metricValuesEqual(wantP95, stats.P95) {
		return errors.New("benchmark p95 does not match samples")
	}
	var peak uint64
	for _, sample := range stats.Samples {
		if sample.PeakRSSBytes > peak {
			peak = sample.PeakRSSBytes
		}
	}
	if stats.PeakRSSBytes != peak {
		return errors.New("benchmark peak RSS does not match samples")
	}
	return nil
}

func validateMedian(stats BenchmarkStats) error {
	values := slices.Clone(stats.Samples)
	want := medianMetrics(values)
	if !metricValuesEqual(want, stats.Median) {
		return errors.New("benchmark median does not match samples")
	}
	return nil
}

func validMetricValues(values MetricValues) bool {
	return finite(values.NSPerOp) && finite(values.BytesPerOp) && finite(values.AllocsPerOp) &&
		values.NSPerOp > 0 && values.BytesPerOp >= 0 && values.AllocsPerOp >= 0
}

func metricValuesEqual(left, right MetricValues) bool {
	return nearlyEqual(left.NSPerOp, right.NSPerOp) && nearlyEqual(left.BytesPerOp, right.BytesPerOp) && nearlyEqual(left.AllocsPerOp, right.AllocsPerOp)
}

func nearlyEqual(left, right float64) bool {
	scale := math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
	return math.Abs(left-right) <= scale*1e-12
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
