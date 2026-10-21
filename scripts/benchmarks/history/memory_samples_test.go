package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestFixedMemorySamplesPreserveTimedMetrics(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{
		"go.mod": "module memory.example/fixture\n\ngo 1.26\n",
		"memory_test.go": `package fixture
import ("crypto/sha256"; "testing")
var digest [32]byte
func BenchmarkMemory(b *testing.B) {
 size := 64
 if b.N == 64 { size = 32<<20 }
 data := make([]byte, size)
 for index := range data { data[index] = byte(index) }
 b.ResetTimer()
 for index := 0; index < b.N; index++ { digest = sha256.Sum256(data) }
}
`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	binary, err := buildBenchmarkBinary(context.Background(), root, "go", ".", filepath.Join(t.TempDir(), "memory.test.exe"))
	if err != nil {
		t.Fatal(err)
	}
	parameters := Parameters{Count: 1, Benchtime: "1x", CPU: 1, Repetitions: 1}
	for _, name := range []string{"valid", "missing", "unexpected", "duplicate", "canceled"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			timed, err := runBenchmarkSample(ctx, binary, ".", "^BenchmarkMemory$", parameters, 0)
			if err != nil {
				t.Fatal(err)
			}
			before := timed["BenchmarkMemory"][0]
			switch name {
			case "missing":
				timed["BenchmarkAbsent"] = timed["BenchmarkMemory"]
			case "unexpected":
				timed["BenchmarkMemory"] = append(timed["BenchmarkMemory"], before)
			case "duplicate":
				timed["BenchmarkMemory/sub"] = timed["BenchmarkMemory"]
			case "canceled":
				cancel()
			}
			err = applyMemorySamples(ctx, binary, ".", parameters, timed, 0, nil)
			if name != "valid" {
				if err == nil || (name == "canceled" && !errors.Is(err, context.Canceled)) {
					t.Fatalf("%s memory execution = %v", name, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			after := timed["BenchmarkMemory"][0]
			if after.Iterations != before.Iterations || !reflect.DeepEqual(after.MetricValues, before.MetricValues) || after.PeakRSSIterations != 64 {
				t.Fatalf("RSS pass changed timed metrics: before=%+v after=%+v", before, after)
			}
			if (runtime.GOOS == "linux" || runtime.GOOS == "darwin") && after.PeakRSSBytes < 32<<20 {
				t.Fatalf("RSS omitted fixed-work allocation: %+v", after)
			}
		})
	}
}

func TestMemoryContractRejectsWrongMethod(t *testing.T) {
	for _, name := range []string{"missing", "wrong count", "CPU count", "legacy count", "compiled refresh", "compiled runner", "compiled mismatch", "lowest-peak refresh", "lowest-peak runner"} {
		t.Run(name, func(t *testing.T) {
			result := syntheticResult()
			var err error
			switch name {
			case "missing":
				result.Groups[0].Calibration.Samples[0].PeakRSSIterations = 0
			case "wrong count":
				result.Groups[0].Targets[0].Benchmark.Samples[0].PeakRSSIterations = 63
			case "CPU count":
				result.Groups[0].CPUCalibration.Samples[0].PeakRSSIterations = 64
			case "legacy count":
				result = historicalCompiledResult()
				result.Groups[0].Calibration.Samples[0].PeakRSSIterations = 64
			case "compiled refresh":
				_, err = refreshBaseline(historicalCompiledResult())
			case "compiled runner":
				reference := BenchmarkBaseline{FormatVersion: compiledBaselineFormat, Result: historicalCompiledResult()}
				_, err = bindRunnerBaseline(reference, historicalCompiledResult())
			case "compiled mismatch":
				err = validateBaseline(BenchmarkBaseline{FormatVersion: compiledBaselineFormat, Result: result})
			case "lowest-peak refresh":
				_, err = refreshBaseline(lowestPeakResult())
			case "lowest-peak runner":
				reference := BenchmarkBaseline{FormatVersion: lowestPeakBaselineFormat, Result: lowestPeakResult()}
				_, err = bindRunnerBaseline(reference, lowestPeakResult())
			}
			if !strings.HasPrefix(name, "compiled") && !strings.HasPrefix(name, "lowest-peak") {
				err = validateResult(result)
			}
			if err == nil {
				t.Fatal("incorrect memory method accepted")
			}
		})
	}
}

// A process peak covers every benchmark the process ran, so each benchmark's
// fixed-work peak comes from its own process: a small benchmark next to a
// large one keeps its own small peak. On Linux a child's peak starts at its
// parent's high-water mark, so the allocations dwarf the race-instrumented
// test process.
func TestFixedMemorySamplesMeasureEachBenchmarkInItsOwnProcess(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("peak RSS is reported on Linux and macOS")
	}
	root := t.TempDir()
	for name, body := range map[string]string{
		"go.mod": "module memory.example/fixture\n\ngo 1.26\n",
		"memory_test.go": `package fixture
import ("crypto/sha256"; "testing")
var digest [32]byte
func BenchmarkLarge(b *testing.B) {
 data := make([]byte, 512<<20)
 for index := range data { data[index] = byte(index) }
 for index := 0; index < b.N; index++ { digest = sha256.Sum256(data[:64]) }
}
func BenchmarkSmall(b *testing.B) {
 for index := 0; index < b.N; index++ { digest = sha256.Sum256(digest[:]) }
}
`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	binary, err := buildBenchmarkBinary(context.Background(), root, "go", ".", filepath.Join(t.TempDir(), "memory.test.exe"))
	if err != nil {
		t.Fatal(err)
	}
	parameters := Parameters{Count: 1, Benchtime: "1x", CPU: 1, Repetitions: 1}
	timed, err := runBenchmarkSample(context.Background(), binary, ".", "^(BenchmarkLarge|BenchmarkSmall)$", parameters, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyMemorySamples(context.Background(), binary, ".", parameters, timed, 0, nil); err != nil {
		t.Fatal(err)
	}
	large, small := timed["BenchmarkLarge"][0].PeakRSSBytes, timed["BenchmarkSmall"][0].PeakRSSBytes
	if large < 512<<20 || small >= 256<<20 {
		t.Fatalf("benchmarks share a process peak: large=%d small=%d", large, small)
	}
}

// A garbage-collection spike in a minority of processes is not a property of
// the workload: each sample keeps the median peak of its repeated fixed-work
// processes, so spikes in two of five processes are not charged, while a
// sporadic allocation that three of five processes make is.
func TestFixedMemorySamplesKeepTheMedianPeakOfRepeatedProcesses(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("peak RSS is reported on Linux and macOS")
	}
	root := t.TempDir()
	for name, body := range map[string]string{
		"go.mod": "module memory.example/fixture\n\ngo 1.26\n",
		"memory_test.go": `package fixture
import ("crypto/sha256"; "errors"; "io/fs"; "os"; "strconv"; "testing")
var digest [32]byte
var processIndex = -1
func BenchmarkTransient(b *testing.B) {
 if processIndex < 0 {
  counter := os.Getenv("RECONC_TRANSIENT_COUNTER")
  body, err := os.ReadFile(counter)
  if err != nil && !errors.Is(err, fs.ErrNotExist) { b.Fatal(err) }
  processIndex = len(body)
  if err := os.WriteFile(counter, append(body, '.'), 0o600); err != nil { b.Fatal(err) }
  spikes, err := strconv.Atoi(os.Getenv("RECONC_TRANSIENT_SPIKES"))
  if err != nil { b.Fatal(err) }
  if processIndex < spikes {
   data := make([]byte, 512<<20)
   for index := range data { data[index] = byte(index) }
   digest = sha256.Sum256(data[:64])
  }
 }
 for index := 0; index < b.N; index++ { digest = sha256.Sum256(digest[:]) }
}
`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	binary, err := buildBenchmarkBinary(context.Background(), root, "go", ".", filepath.Join(t.TempDir(), "memory.test.exe"))
	if err != nil {
		t.Fatal(err)
	}
	parameters := Parameters{Count: 1, Benchtime: "1x", CPU: 1, Repetitions: 1}
	for _, scenario := range []struct {
		spikes  int
		charged bool
	}{{spikes: 2, charged: false}, {spikes: 3, charged: true}} {
		t.Run(strconv.Itoa(scenario.spikes)+" of 5 processes spike", func(t *testing.T) {
			t.Setenv("RECONC_TRANSIENT_COUNTER", filepath.Join(t.TempDir(), "processes"))
			t.Setenv("RECONC_TRANSIENT_SPIKES", strconv.Itoa(scenario.spikes))
			timed := map[string][]MetricSample{"BenchmarkTransient": {{Iterations: 1, MetricValues: MetricValues{NSPerOp: 1}}}}
			diagnostics := newRSSCollector()
			if err := applyMemorySamples(context.Background(), binary, ".", parameters, timed, 0, diagnostics); err != nil {
				t.Fatal(err)
			}
			// A spiking process allocates 512 MiB, a quiet one a few MiB; under
			// memory pressure the platform may keep part of the allocation
			// compressed, so the boundary lies halfway.
			kept := timed["BenchmarkTransient"][0].PeakRSSBytes
			if (kept >= 256<<20) != scenario.charged {
				t.Fatalf("kept peak %d, charged=%t", kept, scenario.charged)
			}
			// Every process peak stays visible in the separate diagnostics
			// report, including the spikes the gate discards.
			report := diagnostics.report(BenchmarkResult{SuiteVersion: suiteVersion, Parameters: parameters})
			if len(report.Benchmarks) != 1 || len(report.Benchmarks[0].Samples) != 1 {
				t.Fatalf("diagnostics benchmarks = %+v", report.Benchmarks)
			}
			spiked := report.Benchmarks[0]
			peaks := spiked.Samples[0].ProcessPeakRSSBytes
			if len(peaks) != memoryBenchmarkRepetitions {
				t.Fatalf("process peaks = %v", peaks)
			}
			for index, peak := range peaks {
				if (index < scenario.spikes) != (peak >= 256<<20) {
					t.Fatalf("process %d peak %d with %d spiking processes", index, peak, scenario.spikes)
				}
			}
			if spiked.Samples[0].KeptPeakRSSBytes != kept || spiked.GatedPeakRSSBytes != kept || spiked.HighestProcessPeakRSSBytes != slices.Max(peaks) {
				t.Fatalf("diagnostics lost a process peak: %+v", spiked)
			}
		})
	}
}
