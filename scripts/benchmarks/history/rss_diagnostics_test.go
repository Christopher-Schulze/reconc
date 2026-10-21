package main

import (
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The diagnostics report lists every process peak per sample in stable
// benchmark order, with the value the gate compares and the highest peak.
func TestRSSDiagnosticsReportKeepsEveryProcessPeak(t *testing.T) {
	collector := newRSSCollector()
	collector.record("./b", "BenchmarkZ", []uint64{30, 162, 31, 29, 32}, 31)
	collector.record("./a", "BenchmarkY", []uint64{10, 11, 12, 13, 14}, 12)
	collector.record("./b", "BenchmarkZ", []uint64{60, 29, 32, 30, 33}, 32)
	result := BenchmarkResult{SuiteVersion: suiteVersion, Parameters: Parameters{Count: 2, Benchtime: "250ms", CPU: 1, Repetitions: 3}}
	result.Environment.Commit = "abc"
	report := collector.report(result)
	want := RSSDiagnostics{
		FormatVersion: rssDiagnosticsFormat, SuiteVersion: suiteVersion, Commit: "abc", Parameters: result.Parameters,
		Benchmarks: []RSSBenchmarkDiagnostics{
			{Package: "./a", Name: "BenchmarkY", Samples: []RSSSampleDiagnostics{{ProcessPeakRSSBytes: []uint64{10, 11, 12, 13, 14}, KeptPeakRSSBytes: 12}},
				GatedPeakRSSBytes: 12, HighestProcessPeakRSSBytes: 14},
			{Package: "./b", Name: "BenchmarkZ", Samples: []RSSSampleDiagnostics{
				{ProcessPeakRSSBytes: []uint64{30, 162, 31, 29, 32}, KeptPeakRSSBytes: 31},
				{ProcessPeakRSSBytes: []uint64{60, 29, 32, 30, 33}, KeptPeakRSSBytes: 32},
			}, GatedPeakRSSBytes: 32, HighestProcessPeakRSSBytes: 162},
		},
	}
	if !reflect.DeepEqual(report, want) {
		t.Fatalf("report = %+v\nwant %+v", report, want)
	}
	var nilCollector *rssCollector
	nilCollector.record("./a", "BenchmarkY", []uint64{1}, 1)
}

// Single-root recording rejects a diagnostics path that names the result
// file through any spelling, before any benchmark runs.
func TestRecordRejectsDiagnosticsAliasingTheResult(t *testing.T) {
	directory := t.TempDir()
	t.Chdir(directory)
	missingRoot := filepath.Join(directory, "missing-root")
	absolute := filepath.Join(directory, "result.json")
	for name, paths := range map[string][2]string{
		"same spelling":     {"result.json", "result.json"},
		"dot prefix":        {"result.json", "./result.json"},
		"relative absolute": {"result.json", absolute},
		"parent traversal":  {absolute, filepath.Join(directory, "nested", "..", "result.json")},
	} {
		err := run([]string{"record", "--root", missingRoot, "--output", paths[0], "--rss-diagnostics", paths[1]}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "distinct from --output") {
			t.Fatalf("%s: aliased diagnostics error = %v", name, err)
		}
	}
}
