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

func TestHistoricalV4BaselineRemainsReadable(t *testing.T) {
	baseline, err := readBaseline(filepath.Join("..", "baseline-v4.json"))
	if err != nil {
		t.Fatal(err)
	}
	if baseline.FormatVersion != legacyBaselineFormat || baseline.Result.FormatVersion != legacyResultFormat {
		t.Fatalf("historical baseline was relabeled: %s, %s", baseline.FormatVersion, baseline.Result.FormatVersion)
	}
}
