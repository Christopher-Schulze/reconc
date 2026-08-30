package action

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestEvaluationControlUsesExactDeadlineBoundary(t *testing.T) {
	deadline := time.Unix(100, 0)
	tests := []struct {
		name string
		now  time.Time
		want ReasonCode
	}{
		{name: "before", now: deadline.Add(-time.Nanosecond)},
		{name: "exact", now: deadline, want: ReasonDeadlineExceeded},
		{name: "after", now: deadline.Add(time.Nanosecond), want: ReasonDeadlineExceeded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			control := evaluationControl{
				deadline: deadline, hasDeadline: true, now: func() time.Time { return test.now },
			}
			if got := control.stopReason(); got != test.want {
				t.Fatalf("stop reason = %s, want %s", got, test.want)
			}
		})
	}
}

func TestEvaluationContextCancellationAndFastPathEquivalence(t *testing.T) {
	evaluator, input := testActionEvaluator(
		t, evaluatorBenchmarkRules(64, true), Defaults{}, testExternalEffect(),
	)
	baseline := evaluator.Evaluate(input)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled := evaluator.EvaluateContext(ctx, input)
	if cancelled.Failure == nil || cancelled.Failure.Code != ReasonCancelled || cancelled.Cache.Eligible {
		t.Fatalf("cancelled evaluation = %#v", cancelled)
	}
	if contextual := evaluator.EvaluateContext(context.Background(), input); !reflect.DeepEqual(contextual, baseline) {
		t.Fatal("uncancellable context changed the deterministic evaluation result")
	}
}

func TestEvaluationWorkCheckpointsResolveDeadlineRacesDeterministically(t *testing.T) {
	evaluator, input := testActionEvaluator(
		t, evaluatorBenchmarkRules(MaxRules, false), Defaults{}, testExternalEffect(),
	)
	prepared := evaluator.Prepare(input)
	baseline := prepared.Evaluate()
	polls := 0
	observed := prepared.evaluate(&evaluationControl{poll: func() ReasonCode {
		polls++
		return ""
	}})
	if !reflect.DeepEqual(observed, baseline) || polls <= MaxRules/evaluationCollectionPollInterval {
		t.Fatalf("checkpoint baseline changed: polls %d result %#v", polls, observed.Failure)
	}

	tests := []struct {
		name       string
		stopAt     int
		reason     ReasonCode
		wantFailed bool
	}{
		{name: "early cancellation", stopAt: 2, reason: ReasonCancelled, wantFailed: true},
		{name: "deadline at final checkpoint", stopAt: polls, reason: ReasonDeadlineExceeded, wantFailed: true},
		{name: "completion before deadline", stopAt: polls + 1, reason: ReasonDeadlineExceeded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := 0
			result := prepared.evaluate(&evaluationControl{poll: func() ReasonCode {
				current++
				if current >= test.stopAt {
					return test.reason
				}
				return ""
			}})
			if test.wantFailed {
				if result.Failure == nil || result.Failure.Code != test.reason || result.Cache.Eligible {
					t.Fatalf("stopped result = %#v", result)
				}
				return
			}
			if !reflect.DeepEqual(result, baseline) {
				t.Fatal("completion before the deadline changed the result")
			}
		})
	}
}

func TestEvaluationDeadlineInterruptsHostileGlobWork(t *testing.T) {
	matcher, err := CompileGlob("**/missing")
	if err != nil {
		t.Fatal(err)
	}
	polls := 0
	matched, complete, reason := matcher.matchWithControl(
		strings.Repeat("segment/", 1<<15)+"present",
		&evaluationControl{poll: func() ReasonCode {
			polls++
			if polls == 3 {
				return ReasonDeadlineExceeded
			}
			return ""
		}},
	)
	if matched || complete || reason != ReasonDeadlineExceeded || polls != 3 {
		t.Fatalf("bounded glob = matched %t complete %t reason %s polls %d", matched, complete, reason, polls)
	}
}
