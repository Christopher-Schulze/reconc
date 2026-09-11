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
			patterns := []string{"^BenchmarkMemory$"}
			switch name {
			case "missing":
				patterns[0] = "^BenchmarkAbsent$"
			case "unexpected":
				delete(timed, "BenchmarkMemory")
			case "duplicate":
				patterns = append(patterns, patterns[0])
			case "canceled":
				cancel()
			}
			err = applyMemorySamples(ctx, binary, ".", patterns, parameters, timed, 0)
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
