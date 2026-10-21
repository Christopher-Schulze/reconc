package main

import "sort"

const rssDiagnosticsFormat = "reconc.benchmark-rss-diagnostics/v1"

// RSSDiagnostics lists every fixed-work process peak behind one recorded
// result. The memory gate compares, per sample, the lowest peak of the
// sample's repeated processes (see applyMemorySamples), so a peak that only
// some processes reach does not fail it. This separate report keeps such
// peaks visible without changing the result, baseline, or comparison formats.
type RSSDiagnostics struct {
	FormatVersion string                    `json:"format_version"`
	SuiteVersion  string                    `json:"suite_version"`
	Commit        string                    `json:"commit"`
	Parameters    Parameters                `json:"parameters"`
	Benchmarks    []RSSBenchmarkDiagnostics `json:"benchmarks"`
}

// RSSBenchmarkDiagnostics holds one benchmark's process peaks per sample, the
// highest kept sample peak the gate compares, and the highest peak of any
// process.
type RSSBenchmarkDiagnostics struct {
	Package                    string                 `json:"package"`
	Name                       string                 `json:"name"`
	Samples                    []RSSSampleDiagnostics `json:"samples"`
	GatedPeakRSSBytes          uint64                 `json:"gated_peak_rss_bytes"`
	HighestProcessPeakRSSBytes uint64                 `json:"highest_process_peak_rss_bytes"`
}

// RSSSampleDiagnostics holds one sample's process peaks in repetition order
// and the lowest of them, which the sample keeps.
type RSSSampleDiagnostics struct {
	ProcessPeakRSSBytes []uint64 `json:"process_peak_rss_bytes"`
	KeptPeakRSSBytes    uint64   `json:"kept_peak_rss_bytes"`
}

type rssBenchmarkKey struct {
	packageName string
	name        string
}

// rssCollector gathers the process peaks of one recorded source root. A nil
// collector records nothing.
type rssCollector struct {
	benchmarks map[rssBenchmarkKey][]RSSSampleDiagnostics
}

func newRSSCollector() *rssCollector {
	return &rssCollector{benchmarks: make(map[rssBenchmarkKey][]RSSSampleDiagnostics)}
}

func (collector *rssCollector) record(packageName, name string, peaks []uint64, kept uint64) {
	if collector == nil {
		return
	}
	key := rssBenchmarkKey{packageName: packageName, name: name}
	collector.benchmarks[key] = append(collector.benchmarks[key], RSSSampleDiagnostics{
		ProcessPeakRSSBytes: append([]uint64(nil), peaks...), KeptPeakRSSBytes: kept,
	})
}

func (collector *rssCollector) report(result BenchmarkResult) RSSDiagnostics {
	report := RSSDiagnostics{
		FormatVersion: rssDiagnosticsFormat, SuiteVersion: result.SuiteVersion,
		Commit: result.Environment.Commit, Parameters: result.Parameters,
		Benchmarks: []RSSBenchmarkDiagnostics{},
	}
	keys := make([]rssBenchmarkKey, 0, len(collector.benchmarks))
	for key := range collector.benchmarks {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		if keys[left].packageName != keys[right].packageName {
			return keys[left].packageName < keys[right].packageName
		}
		return keys[left].name < keys[right].name
	})
	for _, key := range keys {
		benchmark := RSSBenchmarkDiagnostics{Package: key.packageName, Name: key.name, Samples: collector.benchmarks[key]}
		for _, sample := range benchmark.Samples {
			benchmark.GatedPeakRSSBytes = max(benchmark.GatedPeakRSSBytes, sample.KeptPeakRSSBytes)
			for _, peak := range sample.ProcessPeakRSSBytes {
				benchmark.HighestProcessPeakRSSBytes = max(benchmark.HighestProcessPeakRSSBytes, peak)
			}
		}
		report.Benchmarks = append(report.Benchmarks, benchmark)
	}
	return report
}
