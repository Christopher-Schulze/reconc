package main

import (
	"path/filepath"
	"testing"
)

func TestCheckedBaselineMatchesCurrentSuite(t *testing.T) {
	baseline, err := readBaseline(filepath.Join("..", "baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	if baseline.FormatVersion != baselineFormat || baseline.Result.FormatVersion != resultFormat ||
		baseline.Result.SuiteVersion != suiteVersion || baseline.Result.Environment.Dirty {
		t.Fatalf("checked baseline uses a stale or dirty contract: %s, %s, %s, dirty=%t",
			baseline.FormatVersion, baseline.Result.FormatVersion, baseline.Result.SuiteVersion, baseline.Result.Environment.Dirty)
	}
	if len(baseline.Result.Groups) != len(benchmarkSuite) {
		t.Fatalf("checked baseline has %d groups, suite has %d", len(baseline.Result.Groups), len(benchmarkSuite))
	}
	for index, spec := range benchmarkSuite {
		group := baseline.Result.Groups[index]
		if group.Name != spec.Name || group.Package != spec.Package || group.Calibration.Name != spec.Calibration ||
			len(group.Targets) != len(spec.Targets) {
			t.Fatalf("checked baseline group %d differs from suite: %+v, %+v", index, group, spec)
		}
		for targetIndex, name := range spec.Targets {
			if group.Targets[targetIndex].Benchmark.Name != name {
				t.Fatalf("checked baseline target %d/%d = %q, want %q", index, targetIndex,
					group.Targets[targetIndex].Benchmark.Name, name)
			}
		}
	}
}

func TestHistoricalBaselinesRemainReadable(t *testing.T) {
	for _, historical := range []struct{ file, baselineFormat, resultFormat string }{
		{"baseline-v4.json", legacyBaselineFormat, legacyResultFormat},
		{"baseline-v6.json", lowestPeakBaselineFormat, lowestPeakResultFormat},
	} {
		baseline, err := readBaseline(filepath.Join("..", historical.file))
		if err != nil {
			t.Fatalf("%s: %v", historical.file, err)
		}
		if baseline.FormatVersion != historical.baselineFormat || baseline.Result.FormatVersion != historical.resultFormat {
			t.Fatalf("historical baseline %s was relabeled: %s, %s", historical.file, baseline.FormatVersion, baseline.Result.FormatVersion)
		}
	}
}
