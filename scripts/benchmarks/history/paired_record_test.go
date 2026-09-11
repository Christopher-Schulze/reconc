package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPairedPackageRecordsRealAlternatingSources(t *testing.T) {
	for _, name := range []string{"complete", "canceled", "missing benchmark"} {
		t.Run(name, func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "order")
			roots := [2]string{newPairedBenchmarkSource(t, log, "baseline"), newPairedBenchmarkSource(t, log, "candidate")}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			benchmark := "BenchmarkPairContentDigest"
			if name == "canceled" {
				cancel()
			}
			if name == "missing benchmark" {
				benchmark = "BenchmarkMissing"
			}
			parameters := Parameters{Count: 2, Benchtime: "1x", CPU: 1, Repetitions: 1}
			samples, cpu, err := runPairedPackageBenchmarks(ctx, roots, "go", ".", []string{benchmark}, parameters)
			if name != "complete" {
				if err == nil || (name == "canceled" && !errors.Is(err, context.Canceled)) {
					t.Fatalf("%s execution error = %v", name, err)
				}
				if _, statErr := os.Stat(log); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("failed execution ran the workload: %v", statErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			assertFileBytes(t, log, []byte("baseline\ncandidate\ncandidate\nbaseline\n"))
			for index := range roots {
				if len(samples[index]) != 1 || len(samples[index][benchmark]) != 2 || len(cpu[index]) != 2 {
					t.Fatalf("root %d samples = %#v, CPU = %#v", index, samples[index], cpu[index])
				}
				for _, sample := range samples[index][benchmark] {
					if sample.Iterations != 1 || sample.NSPerOp <= 0 {
						t.Fatalf("invalid real benchmark sample: %+v", sample)
					}
				}
			}
		})
	}
}

func newPairedBenchmarkSource(t *testing.T, log, label string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module paired.example/fixture\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := fmt.Sprintf(`package fixture
import ("crypto/sha256"; "os"; "testing")
var digest [32]byte
func BenchmarkPairContentDigest(b *testing.B) {
 data := []byte(%q)
 b.ResetTimer()
 for index := 0; index < b.N; index++ { digest = sha256.Sum256(data) }
 b.StopTimer()
 file, err := os.OpenFile(%q, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
 if err != nil { b.Fatal(err) }
 _, writeErr := file.WriteString(%q)
 closeErr := file.Close()
 if writeErr != nil { b.Fatal(writeErr) }
 if closeErr != nil { b.Fatal(closeErr) }
}
`, strings.Repeat(label, 64), log, label+"\n")
	if err := os.WriteFile(filepath.Join(root, "paired_test.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestPairedRecordingRejectsUnboundEnvironments(t *testing.T) {
	for _, name := range []string{"valid", "wrong baseline", "dirty baseline", "dirty candidate", "mutable candidate", "Go version", "CPU", "parameters"} {
		t.Run(name, func(t *testing.T) {
			reference, err := refreshBaseline(syntheticResult())
			if err != nil {
				t.Fatal(err)
			}
			results := [2]BenchmarkResult{syntheticResult(), syntheticResult()}
			results[1].Environment.Commit = strings.Repeat("b", 40)
			switch name {
			case "wrong baseline":
				results[0].Environment.Commit = strings.Repeat("c", 40)
			case "dirty baseline":
				results[0].Environment.Dirty = true
			case "dirty candidate":
				results[1].Environment.Dirty = true
			case "mutable candidate":
				results[1].Environment.Commit = "main"
			case "Go version":
				results[1].Environment.GoVersion = "different"
			case "CPU":
				results[1].Environment.CPU = "different"
			case "parameters":
				results[1].Parameters.Count++
			}
			err = validatePairedEnvironments(reference, results)
			if (err == nil) != (name == "valid") {
				t.Fatalf("%s environment validation = %v", name, err)
			}
		})
	}
}

func TestPairedOutputsPreserveExistingEvidence(t *testing.T) {
	for _, name := range []string{"distinct", "same", "existing"} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			outputs := [2]string{filepath.Join(directory, "baseline.json"), filepath.Join(directory, "current.json")}
			if name == "same" {
				outputs[1] = outputs[0]
			}
			if name == "existing" {
				if err := os.WriteFile(outputs[0], []byte("retained evidence\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := validatePairedOutputs(outputs); (err == nil) != (name == "distinct") {
				t.Fatalf("%s output validation = %v", name, err)
			}
			if name == "existing" {
				assertFileBytes(t, outputs[0], []byte("retained evidence\n"))
			}
		})
	}
}

func TestPairedCommandRejectsIncompleteFlagsBeforeRecording(t *testing.T) {
	for _, args := range [][]string{nil, {"--unknown"}, {"--baseline-root", "."}} {
		if err := run(append([]string{"record-pair"}, args...), io.Discard); err == nil {
			t.Fatalf("incomplete paired invocation accepted: %v", args)
		}
	}
}
