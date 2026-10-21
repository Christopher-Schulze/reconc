package main

import (
	"reflect"
	"testing"
)

// The diagnostics report lists every process peak per sample in stable
// benchmark order, with the value the gate compares and the highest peak.
func TestRSSDiagnosticsReportKeepsEveryProcessPeak(t *testing.T) {
	collector := newRSSCollector()
	collector.record("./b", "BenchmarkZ", []uint64{30, 162, 31}, 30)
	collector.record("./a", "BenchmarkY", []uint64{10, 11, 12}, 10)
	collector.record("./b", "BenchmarkZ", []uint64{60, 29, 32}, 29)
	result := BenchmarkResult{SuiteVersion: suiteVersion, Parameters: Parameters{Count: 2, Benchtime: "250ms", CPU: 1, Repetitions: 3}}
	result.Environment.Commit = "abc"
	report := collector.report(result)
	want := RSSDiagnostics{
		FormatVersion: rssDiagnosticsFormat, SuiteVersion: suiteVersion, Commit: "abc", Parameters: result.Parameters,
		Benchmarks: []RSSBenchmarkDiagnostics{
			{Package: "./a", Name: "BenchmarkY", Samples: []RSSSampleDiagnostics{{ProcessPeakRSSBytes: []uint64{10, 11, 12}, KeptPeakRSSBytes: 10}},
				GatedPeakRSSBytes: 10, HighestProcessPeakRSSBytes: 12},
			{Package: "./b", Name: "BenchmarkZ", Samples: []RSSSampleDiagnostics{
				{ProcessPeakRSSBytes: []uint64{30, 162, 31}, KeptPeakRSSBytes: 30},
				{ProcessPeakRSSBytes: []uint64{60, 29, 32}, KeptPeakRSSBytes: 29},
			}, GatedPeakRSSBytes: 30, HighestProcessPeakRSSBytes: 162},
		},
	}
	if !reflect.DeepEqual(report, want) {
		t.Fatalf("report = %+v\nwant %+v", report, want)
	}
	var nilCollector *rssCollector
	nilCollector.record("./a", "BenchmarkY", []uint64{1}, 1)
}
