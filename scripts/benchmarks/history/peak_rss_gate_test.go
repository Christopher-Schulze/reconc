package main

import (
	"errors"
	"testing"
)

// withPeakRSS sets per-sample process peaks on the first target and keeps the
// reported maximum consistent with them.
func withPeakRSS(result BenchmarkResult, peaks []uint64) BenchmarkResult {
	stats := &result.Groups[0].Targets[0].Benchmark
	stats.PeakRSSBytes = 0
	for index := range stats.Samples {
		stats.Samples[index].PeakRSSBytes = peaks[index]
		stats.PeakRSSBytes = max(stats.PeakRSSBytes, peaks[index])
	}
	return result
}

func TestPeakRSSGateComparesTheTypicalSamplePeak(t *testing.T) {
	const megabyte = 1 << 20
	baseline, err := refreshBaseline(withPeakRSS(syntheticResult(), []uint64{30 * megabyte, 31 * megabyte, 30 * megabyte, 32 * megabyte, 30 * megabyte}))
	if err != nil {
		t.Fatal(err)
	}
	outlier := withPeakRSS(syntheticResult(), []uint64{30 * megabyte, 31 * megabyte, 106 * megabyte, 30 * megabyte, 31 * megabyte})
	report, err := compareResults(baseline, outlier)
	if err != nil || !report.Passed {
		t.Fatalf("one garbage-collection outlier sample must not fail the memory gate: report=%+v err=%v", report.Regressions, err)
	}
	if report.Groups[0].CurrentPeakRSSBytes != 106*megabyte {
		t.Fatalf("report must keep the maximum peak: %d", report.Groups[0].CurrentPeakRSSBytes)
	}
	doubled := withPeakRSS(syntheticResult(), []uint64{61 * megabyte, 62 * megabyte, 60 * megabyte, 63 * megabyte, 61 * megabyte})
	report, err = compareResults(baseline, doubled)
	if !errors.Is(err, errRegression) {
		t.Fatalf("a doubled typical peak must fail the memory gate: report=%+v err=%v", report.Regressions, err)
	}
	for _, regression := range report.Regressions {
		if regression.Metric == "absolute_peak_rss_bytes" && regression.Current == 61*megabyte {
			return
		}
	}
	t.Fatalf("memory regression missing or not median-based: %+v", report.Regressions)
}
