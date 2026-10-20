package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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
			err = applyMemorySamples(ctx, binary, ".", parameters, timed, 0)
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
	for _, name := range []string{"missing", "wrong count", "CPU count", "legacy count", "compiled refresh", "compiled runner", "compiled mismatch"} {
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
			}
			if !strings.HasPrefix(name, "compiled") {
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
	if err := applyMemorySamples(context.Background(), binary, ".", parameters, timed, 0); err != nil {
		t.Fatal(err)
	}
	large, small := timed["BenchmarkLarge"][0].PeakRSSBytes, timed["BenchmarkSmall"][0].PeakRSSBytes
	if large < 512<<20 || small >= 256<<20 {
		t.Fatalf("benchmarks share a process peak: large=%d small=%d", large, small)
	}
}

// A garbage-collection spike in one process is not a property of the
// workload: each sample keeps the lowest peak of its repeated fixed-work
// processes, so one spiking process is not charged, while an allocation every
// process makes still is.
func TestFixedMemorySamplesKeepTheLowestPeakOfRepeatedProcesses(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("peak RSS is reported on Linux and macOS")
	}
	root := t.TempDir()
	for name, body := range map[string]string{
		"go.mod": "module memory.example/fixture\n\ngo 1.26\n",
		"memory_test.go": `package fixture
import ("crypto/sha256"; "os"; "testing")
var digest [32]byte
func allocate(size int) {
 data := make([]byte, size)
 for index := range data { data[index] = byte(index) }
 digest = sha256.Sum256(data[:64])
}
func BenchmarkSteady(b *testing.B) {
 allocate(512 << 20)
 for index := 0; index < b.N; index++ { digest = sha256.Sum256(digest[:]) }
}
func BenchmarkTransient(b *testing.B) {
 marker := os.Getenv("RECONC_TRANSIENT_MARKER")
 if _, err := os.Stat(marker); err != nil {
  if err := os.WriteFile(marker, nil, 0o600); err != nil { b.Fatal(err) }
  allocate(512 << 20)
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
	marker := filepath.Join(t.TempDir(), "spiked")
	t.Setenv("RECONC_TRANSIENT_MARKER", marker)
	parameters := Parameters{Count: 1, Benchtime: "1x", CPU: 1, Repetitions: 3}
	timed, err := runBenchmarkSample(context.Background(), binary, ".", "^(BenchmarkSteady|BenchmarkTransient)$", parameters, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The timed pass spiked once; the fixed-work pass spikes in its first
	// process only.
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err := applyMemorySamples(context.Background(), binary, ".", parameters, timed, 0); err != nil {
		t.Fatal(err)
	}
	steady, transient := timed["BenchmarkSteady"][0].PeakRSSBytes, timed["BenchmarkTransient"][0].PeakRSSBytes
	if steady < 512<<20 || transient >= 256<<20 {
		t.Fatalf("fixed-work peaks: steady=%d transient=%d", steady, transient)
	}
}
