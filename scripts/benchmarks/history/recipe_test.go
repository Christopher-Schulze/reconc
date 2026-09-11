package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/templates"
)

func TestRecipeComparisonProducesBoundBudgetEvidence(t *testing.T) {
	for _, test := range []struct {
		name    string
		regress bool
	}{{"passing", false}, {"allocation regression", true}} {
		t.Run(test.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			baseline, err := refreshBaseline(syntheticResult())
			if err != nil {
				t.Fatal(err)
			}
			current := syntheticResult()
			if test.regress {
				target := &current.Groups[0].Targets[0]
				target.Benchmark = syntheticStats(target.Benchmark.Name, MetricValues{NSPerOp: 50, BytesPerOp: 50, AllocsPerOp: 10})
				target.Normalized, err = normalize(target.Benchmark.Median, current.Groups[0].Calibration.Median)
				if err != nil {
					t.Fatal(err)
				}
			}
			writeTestContract(t, "baseline.json", baseline)
			writeTestContract(t, "result.json", current)
			args := []string{"--recipe", "--baseline", "baseline.json", "--result", "result.json", "--output", "comparison.json", "--suite", suiteVersion, "--current", current.Environment.Commit}
			var stdout bytes.Buffer
			err = runCompare(args, &stdout)
			if test.regress != errors.Is(err, errRegression) || (!test.regress && err != nil) {
				t.Fatalf("comparison error = %v", err)
			}
			disposition := "pass"
			if test.regress {
				disposition = "block"
			}
			if err := templates.ValidateRecipeEvidence(policy.RecipeContractPerformance, args, stdout.String(), disposition, "test"); err != nil {
				t.Fatal(err)
			}
			var evidence templates.RecipeEvidence
			if err := json.Unmarshal(stdout.Bytes(), &evidence); err != nil {
				t.Fatal(err)
			}
			if evidence.AbsoluteBudgetPass == test.regress || evidence.NormalizedBudgetPass == test.regress {
				t.Fatalf("wrong budget outcomes: %+v", evidence)
			}
			body, err := os.ReadFile("comparison.json")
			if err != nil {
				t.Fatal(err)
			}
			if evidence.Evidence != fmt.Sprintf("sha256:%x", sha256.Sum256(body)) {
				t.Fatal("evidence does not bind published comparison bytes")
			}
		})
	}
}

func TestRecipeComparisonRejectsUnattributableResults(t *testing.T) {
	for _, name := range []string{"stale commit", "dirty source", "incompatible CPU", "wrong suite", "output aliases input"} {
		t.Run(name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			baseline, err := refreshBaseline(syntheticResult())
			if err != nil {
				t.Fatal(err)
			}
			current := syntheticResult()
			expected, suite, output := current.Environment.Commit, suiteVersion, "comparison.json"
			switch name {
			case "stale commit":
				expected = strings.Repeat("b", 40)
			case "dirty source":
				current.Environment.Dirty = true
			case "incompatible CPU":
				current.Environment.CPU = "different CPU"
			case "wrong suite":
				suite = "different-suite"
			case "output aliases input":
				output = "./result.json"
			}
			writeTestContract(t, "baseline.json", baseline)
			writeTestContract(t, "result.json", current)
			var stdout bytes.Buffer
			if err := runRecipeComparison("baseline.json", "result.json", output, suite, expected, &stdout); err == nil {
				t.Fatal("unattributable result accepted")
			}
			if stdout.Len() != 0 {
				t.Fatalf("untrusted evidence emitted: %s", stdout.String())
			}
		})
	}
}
