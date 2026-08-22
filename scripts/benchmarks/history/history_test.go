package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
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

func TestBuildGroupsRefusesMissingBenchmark(t *testing.T) {
	_, err := buildGroups(map[string][]MetricSample{}, 5)
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
		{name: "parameters", mutate: func(result *BenchmarkResult) { result.Parameters.Benchtime = "200x" }},
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
		Parameters:  Parameters{Count: 5, Benchtime: "100x", CPU: 1},
	}
	for _, spec := range benchmarkSuite {
		calibration := syntheticStats(spec.Calibration, MetricValues{NSPerOp: 100, BytesPerOp: 100, AllocsPerOp: 10})
		group := GroupResult{Name: spec.Name, Package: spec.Package, Calibration: calibration}
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
	return BenchmarkStats{Name: name, Samples: samples, Median: values}
}

func setTargetNS(result *BenchmarkResult, value float64) {
	target := &result.Groups[0].Targets[0]
	for index := range target.Benchmark.Samples {
		target.Benchmark.Samples[index].NSPerOp = value
	}
	target.Benchmark.Median.NSPerOp = value
	normalized, _ := normalize(target.Benchmark.Median, result.Groups[0].Calibration.Median)
	target.Normalized = normalized
}
