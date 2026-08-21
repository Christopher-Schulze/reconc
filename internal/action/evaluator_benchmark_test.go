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

func BenchmarkActionContextRootPredicates(b *testing.B) {
	role := testStringValue(b, "operator")
	request := Request{Context: []ContextValue{{
		Name: "role", Value: role, Provenance: ProvenanceHostObserved, Available: true,
	}}}
	predicates := make([]*CompiledPredicate, 128)
	for index := range predicates {
		predicates[index] = compileTestPredicate(b, Predicate{
			Source: SourceContext, Op: OperatorExists,
		})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		roots := predicateRoots{}
		for _, predicate := range predicates {
			if result := evaluatePredicateWithRoots(predicate, request, DecisionBlock, &roots); result.state != ConditionTrue {
				b.Fatal("context predicate did not match")
			}
		}
		if roots.contextBuilds != 1 {
			b.Fatalf("context root builds = %d", roots.contextBuilds)
		}
	}
}

func BenchmarkActionCompilerRepresentative(b *testing.B) {
	plan := Plan{
		Tools: []Tool{{
			ID: "database-write", Transport: TransportMCPStdio,
			ServerLabel: "database", ServerFingerprint: testServerFingerprint,
			Tool: "execute", Effect: testExternalEffect(), Origin: OriginActions,
			SourceIdentity: ".reconc.yml",
		}},
		Rules: evaluatorBenchmarkRules(64, true), Defaults: FrozenDefaults(),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		if _, err := CompilePlan(plan); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecisionCacheHit(b *testing.B) {
	evaluator, input := testActionEvaluator(b, evaluatorBenchmarkRules(16, true), Defaults{}, testExternalEffect())
	result := evaluator.Evaluate(input)
	cache := NewDecisionCache()
	if !cache.Store(evaluator, input, result) {
		b.Fatal("eligible decision was not stored")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		if _, hit, _ := cache.Lookup(evaluator, input); !hit {
			b.Fatal("cache hit became a miss")
		}
	}
}

func BenchmarkDecisionCacheMiss(b *testing.B) {
	evaluator, input := testActionEvaluator(b, evaluatorBenchmarkRules(16, true), Defaults{}, testExternalEffect())
	cache := NewDecisionCache()
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		if _, hit, _ := cache.Lookup(evaluator, input); hit {
			b.Fatal("empty cache returned a hit")
		}
	}
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
