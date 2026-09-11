package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBaselineCommitRequiresValidatedImmutableSource(t *testing.T) {
	for _, test := range []struct {
		name, commit string
		dirty, valid bool
	}{
		{"sha1", strings.Repeat("a", 40), false, true},
		{"sha256", strings.Repeat("b", 64), false, true},
		{"abbreviated", "abcdef0", false, false},
		{"branch", "main", false, false},
		{"uppercase", strings.Repeat("A", 40), false, false},
		{"output injection", strings.Repeat("a", 40) + "\nother=value", false, false},
		{"dirty", strings.Repeat("a", 40), true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			baseline, err := refreshBaseline(syntheticResult())
			if err != nil {
				t.Fatal(err)
			}
			baseline.Result.Environment.Commit = test.commit
			baseline.Result.Environment.Dirty = test.dirty
			path := filepath.Join(t.TempDir(), "baseline.json")
			writeTestContract(t, path, baseline)
			var output bytes.Buffer
			err = run([]string{"baseline-commit", "--baseline", path}, &output)
			if test.valid {
				if err != nil || output.String() != test.commit+"\n" {
					t.Fatalf("commit = %q, error = %v", output.String(), err)
				}
			} else if err == nil || output.Len() != 0 {
				t.Fatalf("untrusted commit emitted: %q, error = %v", output.String(), err)
			}
		})
	}
}

func TestRunnerBaselineRejectsUnboundMeasurements(t *testing.T) {
	for _, name := range []string{"different commit", "mutable reference", "dirty result", "dirty reference", "benchtime", "count", "repetitions", "CPU parallelism", "invalid measurements", "invalid tolerances"} {
		t.Run(name, func(t *testing.T) {
			reference, err := refreshBaseline(syntheticResult())
			if err != nil {
				t.Fatal(err)
			}
			result := syntheticResult()
			switch name {
			case "different commit":
				result.Environment.Commit = strings.Repeat("b", 40)
			case "mutable reference":
				reference.Result.Environment.Commit, result.Environment.Commit = "main", "main"
			case "dirty result":
				result.Environment.Dirty = true
			case "dirty reference":
				reference.Result.Environment.Dirty = true
			case "benchtime":
				result.Parameters.Benchtime = "1s"
			case "count":
				result.Parameters.Count++
			case "repetitions":
				result.Parameters.Repetitions++
			case "CPU parallelism":
				result.Parameters.CPU++
			case "invalid measurements":
				result.Groups[0].Targets[0].Benchmark.Median.NSPerOp++
			case "invalid tolerances":
				reference.Tolerances.AbsoluteNSPerOp = 2
			}
			if _, err := bindRunnerBaseline(reference, result); err == nil {
				t.Fatal("unbound measurement accepted")
			}
		})
	}
}

func TestRunnerBaselinePreservesReviewedBudgetsAndInput(t *testing.T) {
	for _, name := range []string{"passing", "regression", "incompatible runner"} {
		t.Run(name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			reference, err := refreshBaseline(syntheticResult())
			if err != nil {
				t.Fatal(err)
			}
			reference.Tolerances = Tolerances{0.01, 0.02, 0.03, 0.04, 0.05, 0.06, 0.07}
			writeTestContract(t, "checked.json", reference)
			checked, err := os.ReadFile("checked.json")
			if err != nil {
				t.Fatal(err)
			}
			runner := syntheticResult()
			runner.Environment.CPU = "CI CPU (Virtual)"
			runner.Environment.GoVersion = "go1.27.1"
			writeTestContract(t, "runner.json", runner)
			args := []string{"baseline", "--reference", "checked.json", "--result", "runner.json", "--output", "generated.json", "--refresh"}
			if err := run(args, io.Discard); err != nil {
				t.Fatal(err)
			}
			generated, err := readBaseline("generated.json")
			if err != nil || generated.Tolerances != reference.Tolerances || generated.Result.Environment != runner.Environment {
				t.Fatalf("runner baseline lost its binding: %+v, error = %v", generated, err)
			}
			assertFileBytes(t, "checked.json", checked)
			current := syntheticResult()
			current.Environment = runner.Environment
			current.Environment.Commit = strings.Repeat("b", 40)
			if name == "regression" {
				setTargetNS(&current, 55) // 10% passes default 20%, but exceeds both reviewed timing budgets.
			}
			if name == "incompatible runner" {
				current.Environment.CPU = "different CPU"
			}
			writeTestContract(t, "current.json", current)
			var output bytes.Buffer
			err = runRecipeComparison("generated.json", "current.json", "comparison.json", suiteVersion, current.Environment.Commit, &output)
			switch name {
			case "passing":
				if err != nil || !strings.Contains(output.String(), `"result":"pass"`) {
					t.Fatalf("compatible recipe failed: %s, %v", output.String(), err)
				}
			case "regression":
				if !errors.Is(err, errRegression) || !strings.Contains(output.String(), `"result":"block"`) {
					t.Fatalf("reviewed budget was weakened: %s, %v", output.String(), err)
				}
			case "incompatible runner":
				if err == nil || errors.Is(err, errRegression) || output.Len() != 0 {
					t.Fatalf("incompatible runner accepted: %s, %v", output.String(), err)
				}
			}
			if err := run(args, io.Discard); err == nil {
				t.Fatal("existing output accepted for generated runner baseline")
			}
			args[6] = "checked.json"
			if err := run(args, io.Discard); err == nil {
				t.Fatal("checked reference accepted as runner output")
			}
			assertFileBytes(t, "checked.json", checked)
		})
	}
}
