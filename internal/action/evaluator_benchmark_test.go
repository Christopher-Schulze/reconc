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

func BenchmarkActionEvaluatorRepresentativeCalibrated(b *testing.B) {
	benchmarkActionEvaluatorSerial(b, evaluatorBenchmarkRules(64, true), false)
}

func BenchmarkActionEvaluatorMaximumLegalPlanCalibrated(b *testing.B) {
	benchmarkActionEvaluatorSerial(b, evaluatorBenchmarkRules(MaxRules, false), false)
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

func BenchmarkLogicalConditionEvaluationRepresentative(b *testing.B) {
	benchmarkLogicalConditionEvaluation(b, ConditionAll, 4, 3)
}

func BenchmarkLogicalConditionEvaluationMaximum(b *testing.B) {
	benchmarkLogicalConditionEvaluation(b, ConditionAll, MaxConditionNodes-1, 1)
}

func benchmarkLogicalConditionEvaluation(b *testing.B, kind ConditionKind, children, depth int) {
	b.Helper()
	predicate := compileTestPredicate(b, Predicate{Source: SourceArguments, Pointer: "/target", Op: OperatorExists})
	condition := benchmarkLogicalCondition(predicate, kind, children, depth)
	arguments := mustTestValue(b, `{"target":"present"}`)
	request := Request{Arguments: &arguments}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		result := evaluateConditionTree(condition, request, DecisionBlock, 1)
		if result.state != ConditionTrue {
			b.Fatalf("condition state = %s", result.state)
		}
	}
}

func benchmarkLogicalCondition(predicate *CompiledPredicate, kind ConditionKind, children, depth int) *CompiledCondition {
	if depth == 0 {
		return &CompiledCondition{Kind: ConditionPredicate, Predicate: predicate}
	}
	childNodes := make([]*CompiledCondition, children)
	for index := range childNodes {
		childNodes[index] = benchmarkLogicalCondition(predicate, kind, children, depth-1)
	}
	return &CompiledCondition{Kind: kind, Children: childNodes}
}

func BenchmarkActionPointerSummaryScalar(b *testing.B) {
	root := mustTestValue(b, `{"value":"ready"}`)
	benchmarkActionPointerSummary(b, root, []string{"value"}, ValueString)
}

func BenchmarkActionPointerSummaryMaximumDepth(b *testing.B) {
	root := mustTestValue(b, `"ready"`)
	tokens := make([]string, MaxJSONDepth)
	for index := range tokens {
		var err error
		root, err = Array([]Value{root})
		if err != nil {
			b.Fatal(err)
		}
		tokens[index] = "0"
	}
	benchmarkActionPointerSummary(b, root, tokens, ValueString)
}

func BenchmarkActionMembershipMaximum(b *testing.B) {
	values := make([]Value, MaxListValues)
	for index := range values {
		values[index] = testStringValue(b, fmt.Sprintf("value-%03d", index))
	}
	operand, err := Array(values)
	if err != nil {
		b.Fatal(err)
	}
	target := values[len(values)-1]
	b.ReportAllocs()
	for b.Loop() {
		state, reason := evaluateMembership(OperatorIn, target, operand)
		if state != ConditionTrue || reason != "" {
			b.Fatalf("membership = %s, %s", state, reason)
		}
	}
}

func BenchmarkValidateRuntimeValueMaximumLegal(b *testing.B) {
	value := benchmarkMaximumLegalValue(b)
	b.ReportAllocs()
	b.SetBytes(MaxArgumentBytes)
	for b.Loop() {
		if _, _, err := cloneRuntimeValue(value); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkActionRootPointerSummaryMultiRule(b *testing.B) {
	rules := make([]Rule, 64)
	for index := range rules {
		rules[index] = Rule{
			ID:       fmt.Sprintf("root-%03d", index),
			Selector: Selector{Phases: []Phase{PhasePreCall}}, Decision: DecisionWarn,
			When:           &Condition{Predicate: &Predicate{Source: SourceArguments, Op: OperatorExists}},
			SourceIdentity: ".reconc.yml",
		}
	}
	evaluator, input := testActionEvaluator(b, rules, Defaults{}, testExternalEffect())
	arguments := benchmarkMaximumLegalValue(b)
	input.Request.Arguments = &arguments
	b.ReportAllocs()
	b.SetBytes(MaxArgumentBytes)
	for b.Loop() {
		result := evaluator.Evaluate(input)
		if result.Decision != DecisionWarn || len(result.Trace) != len(rules) {
			b.Fatalf("root evaluation = %s, trace %d", result.Decision, len(result.Trace))
		}
	}
}

func BenchmarkActionEvaluationNormalizationFailureMaximumValue(b *testing.B) {
	evaluator, input := testActionEvaluator(b, nil, Defaults{}, testExternalEffect())
	arguments := benchmarkMaximumLegalValue(b)
	input.Request.Arguments = &arguments
	input.Principal = ""
	b.ReportAllocs()
	b.SetBytes(MaxArgumentBytes)
	for b.Loop() {
		result := evaluator.Evaluate(input)
		if result.Failure == nil || result.Failure.Code != ReasonIdentityUnavailable {
			b.Fatalf("failure = %#v", result.Failure)
		}
	}
}

func benchmarkActionPointerSummary(b *testing.B, root Value, tokens []string, want ValueKind) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	var summary OperandSummary
	for iteration := 0; iteration < b.N; iteration++ {
		summary = summarizePointer(resolvePointerTokens(root, tokens))
	}
	if summary.PointerState != PointerPresent || summary.Kind != want {
		b.Fatalf("pointer summary = %#v", summary)
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

func BenchmarkPreparedDecisionCacheHit(b *testing.B) {
	evaluator, input := testActionEvaluator(b, evaluatorBenchmarkRules(16, true), Defaults{}, testExternalEffect())
	prepared := evaluator.Prepare(input)
	result := prepared.Evaluate()
	cache := NewDecisionCache()
	if !cache.StorePrepared(prepared, result) {
		b.Fatal("eligible prepared decision was not stored")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		if _, hit, _ := cache.LookupPrepared(prepared); !hit {
			b.Fatal("prepared cache hit became a miss")
		}
	}
}

func BenchmarkPreparedDecisionCacheStore(b *testing.B) {
	evaluator, input := testActionEvaluator(b, evaluatorBenchmarkRules(16, true), Defaults{}, testExternalEffect())
	prepared := evaluator.Prepare(input)
	result := prepared.Evaluate()
	cache := NewDecisionCache()
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		if !cache.StorePrepared(prepared, result) {
			b.Fatal("prepared cache store failed")
		}
	}
}

func benchmarkActionEvaluator(b *testing.B, rules []Rule) {
	b.Helper()
	b.Run("serial", func(b *testing.B) {
		benchmarkActionEvaluatorSerial(b, rules, true)
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

func benchmarkActionEvaluatorSerial(b *testing.B, rules []Rule, reportPercentiles bool) {
	b.Helper()
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
	if reportPercentiles {
		b.StopTimer()
		reportEvaluationPercentiles(b, evaluator, input)
	}
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
