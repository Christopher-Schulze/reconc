package action

import (
	"context"
	"testing"
	"time"
)

func BenchmarkPreparedEvaluationDeadlineControlFastPath(b *testing.B) {
	evaluator, input := testActionEvaluator(
		b, evaluatorBenchmarkRules(64, true), Defaults{}, testExternalEffect(),
	)
	prepared := evaluator.Prepare(input)
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	for _, benchmark := range []struct {
		name     string
		evaluate func() EvaluationResult
	}{
		{name: "uncancellable", evaluate: prepared.Evaluate},
		{name: "cancellable", evaluate: func() EvaluationResult { return prepared.EvaluateContext(ctx) }},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				result := benchmark.evaluate()
				if !result.Decision.Valid() || result.Failure != nil {
					b.Fatalf("context evaluation = %#v", result.Failure)
				}
			}
		})
	}
}

func BenchmarkPreparedEvaluationMaximumPlan(b *testing.B) {
	evaluator, input := testActionEvaluator(
		b, evaluatorBenchmarkRules(MaxRules, false), Defaults{}, testExternalEffect(),
	)
	prepared := evaluator.Prepare(input)
	b.ReportAllocs()
	for b.Loop() {
		result := prepared.Evaluate()
		if !result.Decision.Valid() || result.Failure != nil {
			b.Fatalf("maximum evaluation = %#v", result.Failure)
		}
	}
}

func BenchmarkPreparedEvaluationDeadlineStop(b *testing.B) {
	evaluator, input := testActionEvaluator(
		b, evaluatorBenchmarkRules(MaxRules, false), Defaults{}, testExternalEffect(),
	)
	prepared := evaluator.Prepare(input)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		polls := 0
		result := prepared.evaluate(&evaluationControl{poll: func() ReasonCode {
			polls++
			if polls == 2 {
				return ReasonDeadlineExceeded
			}
			return ""
		}})
		if result.Failure == nil || result.Failure.Code != ReasonDeadlineExceeded {
			b.Fatalf("deadline evaluation = %#v", result.Failure)
		}
	}
}
