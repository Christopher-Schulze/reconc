package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseBenchmarkJSONAndNormalize(t *testing.T) {
	body := []byte(strings.Join([]string{
		`{"Action":"output","Package":"reconc.dev/reconc/internal/runtime","Output":"goos: darwin\n"}`,
		`{"Action":"output","Package":"reconc.dev/reconc/internal/runtime","Output":"BenchmarkTarget\n"}`,
		`{"Action":"output","Package":"reconc.dev/reconc/internal/runtime","Output":"BenchmarkTarget-1\t100\t10 ns/op\t20 B/op\t2 allocs/op\t5 custom/op\n"}`,
		`{"Action":"output","Package":"reconc.dev/reconc/internal/runtime","Output":"BenchmarkTarget-1\t100\t14 ns/op\t24 B/op\t4 allocs/op\n"}`,
	}, "\n"))
	parsed, err := parseBenchmarkJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	samples := parsed["BenchmarkTarget"]
	if len(samples) != 2 {
		t.Fatalf("samples = %d, want 2", len(samples))
	}
	median := medianMetrics(samples)
	if median != (MetricValues{NSPerOp: 12, BytesPerOp: 22, AllocsPerOp: 3}) {
		t.Fatalf("median = %+v", median)
	}
	normalized, err := normalize(median, MetricValues{NSPerOp: 24, BytesPerOp: 44, AllocsPerOp: 6})
	if err != nil {
		t.Fatal(err)
	}
	if normalized != (MetricValues{NSPerOp: 0.5, BytesPerOp: 0.5, AllocsPerOp: 0.5}) {
		t.Fatalf("normalized = %+v", normalized)
	}
}

func TestParseBenchmarkJSONReassemblesFragmentedOutputEvents(t *testing.T) {
	body := []byte(strings.Join([]string{
		`{"Action":"output","Package":"reconc.dev/reconc/internal/runtime","Output":"BenchmarkFragmented-1"}`,
		`{"Action":"output","Package":"reconc.dev/reconc/internal/runtime","Output":"\t100\t10 ns/op\t20 B/op\t2 allocs/op\n"}`,
	}, "\n"))
	parsed, err := parseBenchmarkJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	samples := parsed["BenchmarkFragmented"]
	if len(samples) != 1 || samples[0].Iterations != 100 || samples[0].BytesPerOp != 20 {
		t.Fatalf("fragmented benchmark samples = %#v", samples)
	}
}

func TestBenchmarkStatsRetainPercentilesAndPeakRSS(t *testing.T) {
	samples := make([]MetricSample, 5)
	for index := range samples {
		value := float64(index + 1)
		samples[index] = MetricSample{
			Iterations: 100, PeakRSSBytes: uint64((index + 1) * 100),
			MetricValues: MetricValues{NSPerOp: value, BytesPerOp: value * 2, AllocsPerOp: value * 3},
		}
	}
	stats, err := statsFor("BenchmarkStats", samples, len(samples))
	if err != nil {
		t.Fatal(err)
	}
	if stats.P50.NSPerOp != 3 || stats.P95.NSPerOp != 4.8 || stats.PeakRSSBytes != 500 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestCollapseMetricSamplesUsesMedianMetricsAndPeakRSS(t *testing.T) {
	samples := []MetricSample{
		{Iterations: 100, PeakRSSBytes: 30, MetricValues: MetricValues{NSPerOp: 30, BytesPerOp: 3, AllocsPerOp: 6}},
		{Iterations: 200, PeakRSSBytes: 90, MetricValues: MetricValues{NSPerOp: 10, BytesPerOp: 1, AllocsPerOp: 2}},
		{Iterations: 150, PeakRSSBytes: 60, MetricValues: MetricValues{NSPerOp: 20, BytesPerOp: 2, AllocsPerOp: 4}},
	}
	collapsed, err := collapseMetricSamples(samples)
	if err != nil {
		t.Fatal(err)
	}
	want := MetricSample{Iterations: 150, PeakRSSBytes: 90, MetricValues: MetricValues{NSPerOp: 20, BytesPerOp: 2, AllocsPerOp: 4}}
	if collapsed != want {
		t.Fatalf("collapsed sample = %+v, want %+v", collapsed, want)
	}
	if _, err := collapseMetricSamples(nil); err == nil {
		t.Fatal("empty sample set was accepted")
	}
}

func TestBenchmarkPatternsRespectGoSubBenchmarkHierarchy(t *testing.T) {
	patterns := benchmarkPatterns([]string{
		"BenchmarkPlain", "BenchmarkTransport/one-shot", "BenchmarkTransport/stdio-worker",
	})
	want := []string{
		"^(BenchmarkPlain)$",
		"^BenchmarkTransport$/^(one-shot|stdio-worker)$",
	}
	if !reflect.DeepEqual(patterns, want) {
		t.Fatalf("patterns = %#v, want %#v", patterns, want)
	}
}

func TestProfileGroupSelectionIsExplicitAndBounded(t *testing.T) {
	selected, err := parseProfileGroups("hook-worker-end-to-end, session-evidence-workloads")
	if err != nil || len(selected) != 2 || !selected["hook-worker-end-to-end"] || !selected["session-evidence-workloads"] {
		t.Fatalf("selected profile groups = %#v err=%v", selected, err)
	}
	for _, value := range []string{"", "unknown", "hook-worker-end-to-end,hook-worker-end-to-end"} {
		if _, err := parseProfileGroups(value); err == nil {
			t.Fatalf("invalid profile groups %q were accepted", value)
		}
	}
	if _, err := profileOptionsFromFlags("/repo", "", "hook-worker-end-to-end"); err == nil {
		t.Fatal("profile groups without a directory were accepted")
	}
	if _, err := profileOptionsFromFlags("/repo", ".build/profiles", ""); err == nil {
		t.Fatal("profile directory without groups was accepted")
	}
}

func TestProfileManifestValidationRejectsUnsafeArtifacts(t *testing.T) {
	manifest := ProfileManifest{
		FormatVersion: profileFormat,
		Environment:   Environment{GoVersion: "go1.27.0", GOOS: "darwin", GOARCH: "arm64", CPU: "Test CPU", Commit: strings.Repeat("a", 40)},
		Parameters:    Parameters{Count: 1, Benchtime: "1x", CPU: 1, Repetitions: 3},
		Workloads: []ProfileWorkload{{
			Group: benchmarkSuite[0].Name, Package: benchmarkSuite[0].Package, Pattern: benchmarkPatterns(append([]string{benchmarkSuite[0].Calibration}, benchmarkSuite[0].Targets...))[0],
			Profiles: []ProfileArtifact{{Kind: "cpu", Path: "profile.pprof", Bytes: 1, SHA256: strings.Repeat("a", 64)}},
		}},
	}
	if err := validateProfileManifest(manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Workloads[0].Profiles[0].Path = "../outside.pprof"
	if err := validateProfileManifest(manifest); err == nil {
		t.Fatal("profile artifact escaped its manifest directory")
	}
}

func TestBuildGroupsRefusesMissingBenchmark(t *testing.T) {
	_, err := buildGroups(map[string][]MetricSample{}, map[string][]MetricSample{}, 5)
	if err == nil || !strings.Contains(err.Error(), benchmarkSuite[0].Calibration) {
		t.Fatalf("missing benchmark error = %v", err)
	}
}

func TestComparisonCompatibilityAndToleranceBoundaries(t *testing.T) {
	baselineResult := syntheticResult()
	baseline, err := refreshBaseline(baselineResult)
	if err != nil {
		t.Fatal(err)
	}
	report, err := compareResults(baseline, baselineResult)
	if err != nil || !report.Passed || len(report.Regressions) != 0 {
		t.Fatalf("identical comparison = (%+v, %v)", report, err)
	}

	atBoundary := syntheticResult()
	setTargetNS(&atBoundary, 60)
	report, err = compareResults(baseline, atBoundary)
	if err != nil || !report.Passed {
		t.Fatalf("exact tolerance boundary failed: (%+v, %v)", report, err)
	}

	regressed := syntheticResult()
	setTargetNS(&regressed, 60.01)
	report, err = compareResults(baseline, regressed)
	if !errors.Is(err, errRegression) || report.Passed || len(report.Regressions) == 0 {
		t.Fatalf("regression comparison = (%+v, %v)", report, err)
	}

	tests := []struct {
		name   string
		mutate func(*BenchmarkResult)
	}{
		{name: "go", mutate: func(result *BenchmarkResult) { result.Environment.GoVersion = "go9.9" }},
		{name: "os", mutate: func(result *BenchmarkResult) { result.Environment.GOOS = "other" }},
		{name: "arch", mutate: func(result *BenchmarkResult) { result.Environment.GOARCH = "other" }},
		{name: "cpu", mutate: func(result *BenchmarkResult) { result.Environment.CPU = "other cpu" }},
		{name: "parameters", mutate: func(result *BenchmarkResult) { result.Parameters.Benchtime = "200x" }},
		{name: "repetitions", mutate: func(result *BenchmarkResult) { result.Parameters.Repetitions = 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := syntheticResult()
			test.mutate(&current)
			if _, err := compareResults(baseline, current); err == nil || !strings.Contains(err.Error(), "incompatible") {
				t.Fatalf("compatibility error = %v", err)
			}
		})
	}
}

func TestComparisonRejectsEqualRatioSlowdownWithAbsoluteBudgets(t *testing.T) {
	baselineResult := syntheticResult()
	baseline, err := refreshBaseline(baselineResult)
	if err != nil {
		t.Fatal(err)
	}
	current := syntheticResult()
	for groupIndex := range current.Groups {
		calibration := &current.Groups[groupIndex].Calibration
		for index := range calibration.Samples {
			calibration.Samples[index].NSPerOp *= 2
			calibration.Samples[index].BytesPerOp *= 2
			calibration.Samples[index].AllocsPerOp *= 2
		}
		calibration.Median.NSPerOp *= 2
		calibration.Median.BytesPerOp *= 2
		calibration.Median.AllocsPerOp *= 2
		calibration.P50.NSPerOp *= 2
		calibration.P50.BytesPerOp *= 2
		calibration.P50.AllocsPerOp *= 2
		calibration.P95.NSPerOp *= 2
		calibration.P95.BytesPerOp *= 2
		calibration.P95.AllocsPerOp *= 2
		for targetIndex := range current.Groups[groupIndex].Targets {
			target := &current.Groups[groupIndex].Targets[targetIndex]
			for index := range target.Benchmark.Samples {
				target.Benchmark.Samples[index].NSPerOp *= 2
				target.Benchmark.Samples[index].BytesPerOp *= 2
				target.Benchmark.Samples[index].AllocsPerOp *= 2
			}
			target.Benchmark.Median.NSPerOp *= 2
			target.Benchmark.Median.BytesPerOp *= 2
			target.Benchmark.Median.AllocsPerOp *= 2
			target.Benchmark.P50.NSPerOp *= 2
			target.Benchmark.P50.BytesPerOp *= 2
			target.Benchmark.P50.AllocsPerOp *= 2
			target.Benchmark.P95.NSPerOp *= 2
			target.Benchmark.P95.BytesPerOp *= 2
			target.Benchmark.P95.AllocsPerOp *= 2
			normalized, normalizeErr := normalize(target.Benchmark.Median, calibration.Median)
			if normalizeErr != nil {
				t.Fatal(normalizeErr)
			}
			target.Normalized = normalized
		}
	}
	report, err := compareResults(baseline, current)
	if !errors.Is(err, errRegression) || report.Passed {
		t.Fatalf("equal-ratio slowdown was accepted: report=%+v err=%v", report, err)
	}
	foundAbsolute := false
	for _, regression := range report.Regressions {
		if regression.Metric == "absolute_ns_per_op" || regression.Metric == "absolute_bytes_per_op" || regression.Metric == "absolute_allocs_per_op" {
			foundAbsolute = true
			break
		}
	}
	if !foundAbsolute {
		t.Fatalf("absolute regression was not reported: %+v", report.Regressions)
	}
}

func TestComparisonSuppressesTimingDriftMeasuredByCPUSentinel(t *testing.T) {
	baselineResult := syntheticResult()
	baseline, err := refreshBaseline(baselineResult)
	if err != nil {
		t.Fatal(err)
	}
	current := syntheticResult()
	for groupIndex := range current.Groups {
		cpuCalibration := &current.Groups[groupIndex].CPUCalibration
		cpuCalibration.Median.NSPerOp *= 2
		cpuCalibration.P50.NSPerOp *= 2
		cpuCalibration.P95.NSPerOp *= 2
		for index := range cpuCalibration.Samples {
			cpuCalibration.Samples[index].NSPerOp *= 2
		}
		calibration := &current.Groups[groupIndex].Calibration
		calibration.Median.NSPerOp *= 2
		calibration.P50.NSPerOp *= 2
		calibration.P95.NSPerOp *= 2
		for index := range calibration.Samples {
			calibration.Samples[index].NSPerOp *= 2
		}
		for targetIndex := range current.Groups[groupIndex].Targets {
			target := &current.Groups[groupIndex].Targets[targetIndex]
			target.Benchmark.Median.NSPerOp *= 2
			target.Benchmark.P50.NSPerOp *= 2
			target.Benchmark.P95.NSPerOp *= 2
			for index := range target.Benchmark.Samples {
				target.Benchmark.Samples[index].NSPerOp *= 2
			}
			normalized, normalizeErr := normalize(target.Benchmark.Median, calibration.Median)
			if normalizeErr != nil {
				t.Fatal(normalizeErr)
			}
			target.Normalized = normalized
		}
	}
	report, err := compareResults(baseline, current)
	if err != nil || !report.Passed {
		t.Fatalf("CPU-sentinel-explained slowdown = report=%+v err=%v", report, err)
	}
	for _, group := range report.Groups {
		if !group.CalibrationAbsoluteNS.Regression || !group.CPUCalibrationAbsoluteNS.Regression {
			t.Fatalf("calibration slowdown was not recorded: %+v", group)
		}
		if !group.RawAbsoluteNSPerOp.Regression {
			t.Fatalf("raw target slowdown was not retained: %+v", group)
		}
		if group.AbsoluteNSPerOp.Regression {
			t.Fatalf("CPU-adjusted target slowdown was not suppressed: %+v", group)
		}
	}

	targetOnly := syntheticResult()
	target := &targetOnly.Groups[0].Targets[0]
	target.Benchmark.Median.NSPerOp *= 2
	target.Benchmark.P50.NSPerOp *= 2
	target.Benchmark.P95.NSPerOp *= 2
	for index := range target.Benchmark.Samples {
		target.Benchmark.Samples[index].NSPerOp *= 2
	}
	normalized, err := normalize(target.Benchmark.Median, targetOnly.Groups[0].Calibration.Median)
	if err != nil {
		t.Fatal(err)
	}
	target.Normalized = normalized
	report, err = compareResults(baseline, targetOnly)
	if !errors.Is(err, errRegression) || report.Passed {
		t.Fatalf("target-specific slowdown was accepted: report=%+v err=%v", report, err)
	}
	for _, regression := range report.Regressions {
		if regression.Group == targetOnly.Groups[0].Name && regression.Metric == "normalized_ns_per_op" {
			return
		}
	}
	t.Fatalf("target-specific normalized timing regression missing: %+v", report.Regressions)
}

func TestComparisonRejectsEqualTargetAndCalibrationSlowdownWithoutCPUSentinelDrift(t *testing.T) {
	baselineResult := syntheticResult()
	baseline, err := refreshBaseline(baselineResult)
	if err != nil {
		t.Fatal(err)
	}
	current := syntheticResult()
	for groupIndex := range current.Groups {
		calibration := &current.Groups[groupIndex].Calibration
		calibration.Median.NSPerOp *= 2
		calibration.P50.NSPerOp *= 2
		calibration.P95.NSPerOp *= 2
		for index := range calibration.Samples {
			calibration.Samples[index].NSPerOp *= 2
		}
		for targetIndex := range current.Groups[groupIndex].Targets {
			target := &current.Groups[groupIndex].Targets[targetIndex]
			target.Benchmark.Median.NSPerOp *= 2
			target.Benchmark.P50.NSPerOp *= 2
			target.Benchmark.P95.NSPerOp *= 2
			for index := range target.Benchmark.Samples {
				target.Benchmark.Samples[index].NSPerOp *= 2
			}
			normalized, normalizeErr := normalize(target.Benchmark.Median, calibration.Median)
			if normalizeErr != nil {
				t.Fatal(normalizeErr)
			}
			target.Normalized = normalized
		}
	}
	report, err := compareResults(baseline, current)
	if !errors.Is(err, errRegression) || report.Passed {
		t.Fatalf("equal target/calibration slowdown was accepted: report=%+v err=%v", report, err)
	}
	for _, regression := range report.Regressions {
		if regression.Metric == "absolute_ns_per_op" {
			return
		}
	}
	t.Fatalf("CPU-independent absolute timing regression missing: %+v", report.Regressions)
}

func TestComparisonReportsIncompatibleEvidence(t *testing.T) {
	baselineResult := syntheticResult()
	baseline, err := refreshBaseline(baselineResult)
	if err != nil {
		t.Fatal(err)
	}
	current := syntheticResult()
	current.Environment.CPU = "different cpu"
	report, err := compareResults(baseline, current)
	if err == nil || report.Compatible || report.Passed || len(report.CompatibilityIssues) != 1 {
		t.Fatalf("incompatible comparison = report=%+v err=%v", report, err)
	}
}

func TestComparisonRejectsAbsoluteMetricWhenBaselineIsZero(t *testing.T) {
	baselineResult := syntheticResult()
	baselineResult.Groups[0].Targets[0].Benchmark.Median.BytesPerOp = 0
	baselineResult.Groups[0].Targets[0].Benchmark.P50.BytesPerOp = 0
	baselineResult.Groups[0].Targets[0].Benchmark.P95.BytesPerOp = 0
	for index := range baselineResult.Groups[0].Targets[0].Benchmark.Samples {
		baselineResult.Groups[0].Targets[0].Benchmark.Samples[index].BytesPerOp = 0
	}
	baselineResult.Groups[0].Targets[0].Normalized.BytesPerOp = 0
	baseline, err := refreshBaseline(baselineResult)
	if err != nil {
		t.Fatal(err)
	}
	current := syntheticResult()
	report, err := compareResults(baseline, current)
	if !errors.Is(err, errRegression) || report.Passed {
		t.Fatalf("zero absolute baseline was accepted: report=%+v err=%v", report, err)
	}
	found := false
	for _, regression := range report.Regressions {
		if regression.Metric == "absolute_bytes_per_op" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("zero baseline regression missing: %+v", report.Regressions)
	}
}

func TestComparisonRejectsAllocationOnlyAbsoluteRegression(t *testing.T) {
	baselineResult := syntheticResult()
	baseline, err := refreshBaseline(baselineResult)
	if err != nil {
		t.Fatal(err)
	}
	current := syntheticResult()
	target := &current.Groups[0].Targets[0]
	for index := range target.Benchmark.Samples {
		target.Benchmark.Samples[index].AllocsPerOp *= 2
	}
	target.Benchmark.Median.AllocsPerOp *= 2
	target.Benchmark.P50.AllocsPerOp *= 2
	target.Benchmark.P95.AllocsPerOp *= 2
	normalized, err := normalize(target.Benchmark.Median, current.Groups[0].Calibration.Median)
	if err != nil {
		t.Fatal(err)
	}
	target.Normalized = normalized
	report, err := compareResults(baseline, current)
	if !errors.Is(err, errRegression) || report.Passed {
		t.Fatalf("allocation-only regression was accepted: report=%+v err=%v", report, err)
	}
	for _, regression := range report.Regressions {
		if regression.Metric == "absolute_allocs_per_op" {
			return
		}
	}
	t.Fatalf("allocation regression missing: %+v", report.Regressions)
}

func TestBaselineRejectsDirtySource(t *testing.T) {
	result := syntheticResult()
	baseline, err := refreshBaseline(result)
	if err != nil {
		t.Fatal(err)
	}
	baseline.Result.Environment.Dirty = true
	if err := validateBaseline(baseline); err == nil || !strings.Contains(err.Error(), "clean source") {
		t.Fatalf("dirty baseline validation error = %v", err)
	}
}

func TestStrictContractsRejectHostileAndOversizedInput(t *testing.T) {
	result := syntheticResult()
	body, err := encodeContract(result)
	if err != nil {
		t.Fatal(err)
	}
	var decoded BenchmarkResult
	if err := decodeStrict(body, &decoded); err != nil {
		t.Fatal(err)
	}
	duplicate := bytes.Replace(body, []byte(`"format_version":`), []byte(`"format_version":"duplicate","format_version":`), 1)
	if err := decodeStrict(duplicate, &decoded); err == nil {
		t.Fatal("duplicate JSON name was accepted")
	}
	unknown := bytes.Replace(body, []byte(`"suite_version":`), []byte(`"unknown":true,"suite_version":`), 1)
	if err := decodeStrict(unknown, &decoded); err == nil {
		t.Fatal("unknown JSON field was accepted")
	}
	if err := decodeStrict(bytes.Repeat([]byte{'x'}, maxContractBytes+1), &decoded); err == nil {
		t.Fatal("oversized contract was accepted")
	}
	if _, err := parseBenchmarkJSON(bytes.Repeat([]byte{'x'}, maxBenchmarkOutput+1)); err == nil {
		t.Fatal("oversized benchmark output was accepted")
	}
}

func TestContractEncodingIsDeterministic(t *testing.T) {
	result := syntheticResult()
	first, err := encodeContract(result)
	if err != nil {
		t.Fatal(err)
	}
	second, err := encodeContract(result)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("contract encoding is not deterministic")
	}
	for _, value := range []string{"100x", "250ms", "1s"} {
		if !validBenchtime(value) {
			t.Fatalf("valid benchtime rejected: %s", value)
		}
	}
	for _, value := range []string{"", "0x", "1m", "auto", "-1s"} {
		if validBenchtime(value) {
			t.Fatalf("invalid benchtime accepted: %s", value)
		}
	}
}

func TestFirstCPUField(t *testing.T) {
	body := "processor : 0\nmodel name : Example CPU 9000\nHardware : fallback\n"
	if got := firstCPUField(body, "model name", "Hardware"); got != "Example CPU 9000" {
		t.Fatalf("CPU identity = %q", got)
	}
	if got := firstCPUField("unrelated: value\n", "model name"); got != "" {
		t.Fatalf("missing CPU identity = %q", got)
	}
}

func TestCLIRefreshRequiresExplicitConfirmation(t *testing.T) {
	directory := t.TempDir()
	resultPath := filepath.Join(directory, "result.json")
	guardedPath := filepath.Join(directory, "guarded.json")
	result := syntheticResult()
	writeTestContract(t, resultPath, result)
	if err := os.WriteFile(guardedPath, []byte("sentinel\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runBaseline([]string{"--result", resultPath, "--output", guardedPath}, io.Discard); err == nil {
		t.Fatal("baseline refresh succeeded without explicit confirmation")
	}
	guarded, err := os.ReadFile(guardedPath)
	if err != nil || string(guarded) != "sentinel\n" {
		t.Fatalf("guarded destination changed: body=%q err=%v", guarded, err)
	}
}

func TestCLIComparisonDoesNotModifyInputs(t *testing.T) {
	directory := t.TempDir()
	resultPath := filepath.Join(directory, "result.json")
	baselinePath := filepath.Join(directory, "baseline.json")
	result := syntheticResult()
	baseline, err := refreshBaseline(result)
	if err != nil {
		t.Fatal(err)
	}
	writeTestContract(t, resultPath, result)
	writeTestContract(t, baselinePath, baseline)
	resultBefore, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatal(err)
	}
	baselineBefore, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	var report bytes.Buffer
	if err := runCompare([]string{"--baseline", baselinePath, "--result", resultPath}, &report); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(report.Bytes(), []byte(`"passed": true`)) {
		t.Fatalf("comparison report = %s", report.Bytes())
	}
	assertFileBytes(t, resultPath, resultBefore)
	assertFileBytes(t, baselinePath, baselineBefore)
}

func TestCLIComparisonRetainsIncompatibleReport(t *testing.T) {
	directory := t.TempDir()
	resultPath := filepath.Join(directory, "result.json")
	baselinePath := filepath.Join(directory, "baseline.json")
	result := syntheticResult()
	baseline, err := refreshBaseline(result)
	if err != nil {
		t.Fatal(err)
	}
	result.Environment.CPU = "different cpu"
	writeTestContract(t, resultPath, result)
	writeTestContract(t, baselinePath, baseline)
	var report bytes.Buffer
	err = runCompare([]string{"--baseline", baselinePath, "--result", resultPath}, &report)
	if err == nil || !strings.Contains(report.String(), `"compatible": false`) || !strings.Contains(report.String(), "compatibility_issues") {
		t.Fatalf("incompatible CLI report = %s err=%v", report.String(), err)
	}
}

func writeTestContract(t *testing.T, path string, value any) {
	t.Helper()
	body, err := encodeContract(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertFileBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s changed during read-only comparison", path)
	}
}

func syntheticResult() BenchmarkResult {
	result := BenchmarkResult{
		FormatVersion: resultFormat, SuiteVersion: suiteVersion,
		Environment: Environment{GoVersion: "go1.27.0", GOOS: "darwin", GOARCH: "arm64", CPU: "Test CPU", Commit: strings.Repeat("a", 40)},
		Parameters:  Parameters{Count: 5, Benchtime: "100x", CPU: 1, Repetitions: 3},
	}
	for _, spec := range benchmarkSuite {
		calibration := syntheticStats(spec.Calibration, MetricValues{NSPerOp: 100, BytesPerOp: 100, AllocsPerOp: 10})
		cpuCalibration := syntheticStats(cpuSentinelName, MetricValues{NSPerOp: 100, BytesPerOp: 0, AllocsPerOp: 0})
		group := GroupResult{Name: spec.Name, Package: spec.Package, Calibration: calibration, CPUCalibration: cpuCalibration}
		for _, targetName := range spec.Targets {
			target := syntheticStats(targetName, MetricValues{NSPerOp: 50, BytesPerOp: 50, AllocsPerOp: 5})
			normalized, _ := normalize(target.Median, calibration.Median)
			group.Targets = append(group.Targets, TargetResult{Benchmark: target, Normalized: normalized})
		}
		result.Groups = append(result.Groups, group)
	}
	return result
}

func syntheticStats(name string, values MetricValues) BenchmarkStats {
	samples := make([]MetricSample, 5)
	for index := range samples {
		samples[index] = MetricSample{Iterations: 100, MetricValues: values}
	}
	return BenchmarkStats{Name: name, Samples: samples, Median: values, P50: values, P95: values}
}

func setTargetNS(result *BenchmarkResult, value float64) {
	target := &result.Groups[0].Targets[0]
	for index := range target.Benchmark.Samples {
		target.Benchmark.Samples[index].NSPerOp = value
	}
	target.Benchmark.Median.NSPerOp = value
	target.Benchmark.P50.NSPerOp = value
	target.Benchmark.P95.NSPerOp = value
	normalized, _ := normalize(target.Benchmark.Median, result.Groups[0].Calibration.Median)
	target.Normalized = normalized
}
