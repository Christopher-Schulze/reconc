package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestPackageBinariesAreReproducibleAcrossSourceRoots(t *testing.T) {
	log := filepath.Join(t.TempDir(), "order")
	roots := []string{newPairedBenchmarkSource(t, log, "same"), newPairedBenchmarkSource(t, log, "same")}
	parameters := Parameters{Count: 2, Benchtime: "1x", CPU: 1, Repetitions: 2}
	measurements, err := runPackageBenchmarks(context.Background(), roots, "go", ".", []string{"BenchmarkPairContentDigest"}, parameters)
	if err != nil {
		t.Fatal(err)
	}
	if !validBinarySHA256(measurements[0].binarySHA256) || measurements[0].binarySHA256 != measurements[1].binarySHA256 {
		t.Fatalf("identical source yielded different binaries: %s, %s", measurements[0].binarySHA256, measurements[1].binarySHA256)
	}
	for _, measurement := range measurements {
		if len(measurement.samples["BenchmarkPairContentDigest"]) != parameters.Count || len(measurement.cpu) != parameters.Count {
			t.Fatalf("internal repetitions were not collapsed: %+v", measurement)
		}
	}
	assertFileBytes(t, log, []byte("same\nsame\nsame\nsame\nsame\nsame\nsame\nsame\n"))
}

func TestDirectBinaryUsesPackageDirectoryWithoutRecompiling(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{
		"go.mod": "module binary.example/fixture\n\ngo 1.26\n",
		"input":  "real benchmark input",
		"binary_test.go": `package fixture
import ("crypto/sha256"; "os"; "testing")
var digest [32]byte
func BenchmarkReadAndHash(b *testing.B) {
 input, err := os.ReadFile("input")
 if err != nil { b.Fatal(err) }
 b.ResetTimer()
 for index := 0; index < b.N; index++ {
  data := make([]byte, 32<<20)
  for offset := range data { data[offset] = input[offset%len(input)] }
  digest = sha256.Sum256(data)
 }
}
func BenchmarkHashUntilCanceled(b *testing.B) {
 input, err := os.ReadFile("input")
 if err != nil { b.Fatal(err) }
 if err := os.WriteFile("running", []byte("started"), 0600); err != nil { b.Fatal(err) }
 for { digest = sha256.Sum256(input) }
}
`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	directory := t.TempDir()
	binary, err := buildBenchmarkBinary(ctx, root, "go", ".", filepath.Join(directory, "fixture.test.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cannot_recompile.go"), []byte("invalid Go source\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	parameters := Parameters{Count: 1, Benchtime: "1x", CPU: 1, Repetitions: 1}
	for _, name := range []string{"completed", "canceled"} {
		t.Run(name, func(t *testing.T) {
			sampleCtx, stop := context.WithCancel(ctx)
			defer stop()
			if name == "canceled" {
				stop()
			}
			samples, err := runBenchmarkSample(sampleCtx, binary, ".", "^BenchmarkReadAndHash$", parameters, 0)
			if name == "canceled" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled binary execution = %v", err)
				}
				return
			}
			if err != nil || len(samples["BenchmarkReadAndHash"]) != 1 {
				t.Fatalf("direct execution = %+v, %v", samples, err)
			}
			sample := samples["BenchmarkReadAndHash"][0]
			if sample.BytesPerOp < 32<<20 || sample.AllocsPerOp < 1 {
				t.Fatalf("real allocation was not measured: %+v", sample)
			}
			if (runtime.GOOS == "darwin" || runtime.GOOS == "linux") && sample.PeakRSSBytes < 32<<20 {
				t.Fatalf("benchmark process RSS omitted its touched allocation: %+v", sample)
			}
		})
	}
	body, err := os.ReadFile(binary.path)
	if err != nil {
		t.Fatal(err)
	}
	if binary.sha256 != fmt.Sprintf("%x", sha256.Sum256(body)) {
		t.Fatal("retained hash does not identify the executed binary")
	}
	t.Run("running cancellation", func(t *testing.T) {
		verifyRunningBenchmarkCancellation(t, binary, parameters)
	})
}

func verifyRunningBenchmarkCancellation(t *testing.T, binary benchmarkBinary, parameters Parameters) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	done := make(chan error, 1)
	go func() {
		_, err := runBenchmarkSample(ctx, binary, ".", "^BenchmarkHashUntilCanceled$", parameters, 0)
		done <- err
	}()
	defer func() {
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("running benchmark cancellation = %v", err)
		}
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(filepath.Join(binary.directory, "running")); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("benchmark never entered the measured process")
		case <-ticker.C:
		}
	}
}

func TestBenchmarkBinaryHashRejectsUnboundedArtifacts(t *testing.T) {
	for _, name := range []string{"missing", "directory", "empty", "oversized"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "binary")
			if name == "directory" {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			} else if name != "missing" {
				file, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				var sizeErr error
				if name == "oversized" {
					sizeErr = file.Truncate(maxBenchmarkBinaryBytes + 1)
				}
				if err := errors.Join(sizeErr, file.Close()); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := benchmarkBinarySHA256(path); err == nil {
				t.Fatal("invalid binary accepted")
			}
		})
	}
}
