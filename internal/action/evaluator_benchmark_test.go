package action

import (
	"fmt"
	"sort"
	"testing"
	"time"
)

func BenchmarkActionEvaluatorSmall(b *testing.B) {
	benchmarkActionEvaluator(b, evaluatorBenchmarkRules(1, true))
}

func BenchmarkActionEvaluatorRepresentative(b *testing.B) {
	benchmarkActionEvaluator(b, evaluatorBenchmarkRules(64, true))
}

func BenchmarkActionEvaluatorMaximumLegalPlan(b *testing.B) {
	benchmarkActionEvaluator(b, evaluatorBenchmarkRules(MaxRules, false))
}

func benchmarkActionEvaluator(b *testing.B, rules []Rule) {
	b.Helper()
	b.Run("serial", func(b *testing.B) {
		evaluator, input := testActionEvaluator(b, rules, Defaults{}, testExternalEffect())
		b.ReportAllocs()
		b.ResetTimer()
		var result EvaluationResult
		for iteration := 0; iteration < b.N; iteration++ {
			result = evaluator.Evaluate(input)
		}
		if !result.Decision.Valid() {
			b.Fatal("evaluator returned an invalid decision")
		}
		b.StopTimer()
		reportEvaluationPercentiles(b, evaluator, input)
	})
	b.Run("parallel", func(b *testing.B) {
		evaluator, input := testActionEvaluator(b, rules, Defaults{}, testExternalEffect())
		b.ReportAllocs()
		b.RunParallel(func(parallel *testing.PB) {
			for parallel.Next() {
				if result := evaluator.Evaluate(input); !result.Decision.Valid() {
					b.Error("evaluator returned an invalid decision")
					return
				}
			}
		})
	})
}

func evaluatorBenchmarkRules(count int, matching bool) []Rule {
	rules := make([]Rule, count)
	for index := range rules {
		selector := Selector{}
		if !matching {
			selector.Tools = []string{"never-matches"}
		}
		rules[index] = Rule{
			ID: fmt.Sprintf("rule-%04d", index), Selector: selector,
			Decision: DecisionWarn, SourceIdentity: ".reconc.yml",
		}
	}
	return rules
}

func reportEvaluationPercentiles(b *testing.B, evaluator *Evaluator, input EvaluationInput) {
	b.Helper()
	const samples = 256
	durations := make([]int64, samples)
	for index := range durations {
		started := time.Now()
		result := evaluator.Evaluate(input)
		durations[index] = time.Since(started).Nanoseconds()
		if !result.Decision.Valid() {
			b.Fatal("percentile sample returned an invalid decision")
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	b.ReportMetric(float64(durations[samples*50/100]), "p50-ns/op")
	b.ReportMetric(float64(durations[samples*95/100]), "p95-ns/op")
	b.ReportMetric(float64(durations[samples*99/100]), "p99-ns/op")
}
