package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func historicalResult() BenchmarkResult {
	result := syntheticResult()
	result.FormatVersion = legacyResultFormat
	for index := range result.Groups {
		result.Groups[index].BinarySHA256 = ""
	}
	return result
}

func TestMeasurementFormatsPreserveHistoricalComparisons(t *testing.T) {
	for _, name := range []string{"historical", "precompiled", "mixed", "same binary regression"} {
		t.Run(name, func(t *testing.T) {
			baseline, err := refreshBaseline(syntheticResult())
			if err != nil {
				t.Fatal(err)
			}
			current := syntheticResult()
			if name == "historical" || name == "mixed" {
				baseline.FormatVersion, baseline.Result = legacyBaselineFormat, historicalResult()
			}
			if name == "historical" {
				current = historicalResult()
			}
			if name == "same binary regression" {
				setTargetNS(&current, 100)
			}
			report, err := compareResults(baseline, current)
			if report.BaselineResultFormat != baseline.Result.FormatVersion || report.CurrentResultFormat != current.FormatVersion {
				t.Fatalf("comparison lost measurement methods: %+v", report)
			}
			switch name {
			case "mixed":
				if err == nil || report.Compatible || report.Passed || !strings.Contains(strings.Join(report.CompatibilityIssues, ";"), "measurement format") {
					t.Fatalf("mixed measurement methods accepted: %+v, %v", report, err)
				}
			case "same binary regression":
				if !errors.Is(err, errRegression) || report.Passed {
					t.Fatalf("binary equality bypassed a regression: %+v, %v", report, err)
				}
			default:
				if err != nil || !report.Compatible || !report.Passed {
					t.Fatalf("valid method comparison rejected: %+v, %v", report, err)
				}
				for index, group := range report.Groups {
					if group.BaselineBinarySHA256 != current.Groups[0].BinarySHA256 || group.CurrentBinarySHA256 != current.Groups[0].BinarySHA256 {
						t.Fatalf("comparison %d lost executable identities: %+v", index, group)
					}
				}
			}
		})
	}
}

func TestMeasurementContractsRejectRelabeling(t *testing.T) {
	for _, name := range []string{"missing hash", "uppercase hash", "invalid hex", "package mismatch", "legacy hash", "baseline mismatch", "legacy refresh", "legacy runner"} {
		t.Run(name, func(t *testing.T) {
			result := syntheticResult()
			var err error
			switch name {
			case "missing hash":
				result.Groups[0].BinarySHA256 = ""
			case "uppercase hash":
				result.Groups[0].BinarySHA256 = strings.Repeat("A", 64)
			case "invalid hex":
				result.Groups[0].BinarySHA256 = strings.Repeat("g", 64)
			case "package mismatch":
				result.Groups[1].BinarySHA256 = strings.Repeat("b", 64)
			case "legacy hash":
				result.FormatVersion = legacyResultFormat
			case "baseline mismatch":
				err = validateBaseline(BenchmarkBaseline{FormatVersion: legacyBaselineFormat, Result: result})
			case "legacy refresh":
				_, err = refreshBaseline(historicalResult())
			case "legacy runner":
				reference := BenchmarkBaseline{FormatVersion: legacyBaselineFormat, Result: historicalResult()}
				_, err = bindRunnerBaseline(reference, historicalResult())
			}
			if name != "baseline mismatch" && name != "legacy refresh" && name != "legacy runner" {
				err = validateResult(result)
			}
			if err == nil {
				t.Fatal("invalid measurement identity accepted")
			}
		})
	}
}

func TestHistoricalReferenceRequiresActualPrecompiledRemeasurement(t *testing.T) {
	reference := BenchmarkBaseline{FormatVersion: legacyBaselineFormat, Result: historicalResult(),
		Tolerances: Tolerances{0.01, 0.02, 0.03, 0.04, 0.05, 0.06, 0.07}}
	before, err := encodeContract(reference)
	if err != nil {
		t.Fatal(err)
	}
	measured := syntheticResult()
	measured.Environment.CPU = "remeasured CPU"
	runner, err := bindRunnerBaseline(reference, measured)
	if err != nil || runner.FormatVersion != baselineFormat || runner.Tolerances != reference.Tolerances || !reflect.DeepEqual(runner.Result, measured) {
		t.Fatalf("remeasurement lost exact source, method, or budgets: %+v, %v", runner, err)
	}
	after, err := encodeContract(reference)
	if err != nil || string(before) != string(after) {
		t.Fatalf("historical reference mutated: %v", err)
	}
}
