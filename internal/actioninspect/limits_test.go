package actioninspect

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
)

func TestInspectionItemBudgetCountsObjectKeysAndArrayItems(t *testing.T) {
	t.Parallel()
	value := mustValue(t, `{"a":[1,2],"b":{}}`)
	items, err := countInspectionItems(context.Background(), value, action.MaxJSONDepth, 4)
	if err != nil || items != 4 {
		t.Fatalf("items = %d, error = %v", items, err)
	}
	if _, err := countInspectionItems(context.Background(), value, action.MaxJSONDepth, 3); !errors.Is(err, errInspectionLimit) {
		t.Fatalf("limit error = %v", err)
	}
	primitive := mustValue(t, `"value"`)
	items, err = countInspectionItems(context.Background(), primitive, action.MaxJSONDepth, 1)
	if err != nil || items != 0 {
		t.Fatalf("primitive items = %d, error = %v", items, err)
	}
}

func TestInspectionItemBudgetAcceptsExactGlobalMaximum(t *testing.T) {
	t.Parallel()
	values := make([]action.Value, action.MaxJSONItems)
	for index := range values {
		values[index] = action.Null()
	}
	value, err := action.Array(values)
	if err != nil {
		t.Fatal(err)
	}
	items, err := countInspectionItems(context.Background(), value, action.MaxJSONDepth, action.MaxJSONItems)
	if err != nil || items != action.MaxJSONItems {
		t.Fatalf("maximum items = %d, error = %v", items, err)
	}
	if _, err := countInspectionItems(context.Background(), value, action.MaxJSONDepth, action.MaxJSONItems-1); !errors.Is(err, errInspectionLimit) {
		t.Fatalf("below-maximum error = %v", err)
	}
}

func TestInspectionDepthAndContextBudgetsFailClosed(t *testing.T) {
	t.Parallel()
	nested := mustValue(t, `{"a":{"b":1}}`)
	if _, err := countInspectionItems(context.Background(), nested, 1, action.MaxJSONItems); !errors.Is(err, errInspectionLimit) {
		t.Fatalf("depth error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := countInspectionItems(ctx, nested, action.MaxJSONDepth, action.MaxJSONItems); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
}

func TestEngineReportsExactLimitAndDeadlineReasons(t *testing.T) {
	t.Parallel()
	compiled := testCompiledPlan(t, action.PhasePreCall, []action.DetectorCategory{action.DetectorSecret}, nil, BuiltinPackIdentity())
	plan := compiled.Plan()
	plan.Detectors[0].Limits.MaxItems = 1
	compiled, err := action.CompilePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	key := testIdentityKey{id: strings.Repeat("a", 32), key: []byte(strings.Repeat("k", 32))}
	engine, err := NewEngine(compiled, key)
	if err != nil {
		t.Fatal(err)
	}
	arguments := mustValue(t, `{"payload":{"a":1,"b":2}}`)
	request := action.Request{
		Transport: action.TransportMCPStdio, ServerLabel: "server", Tool: "inspect",
		Phase: action.PhasePreCall, Arguments: &arguments,
	}
	evidence, err := engine.Inspect(context.Background(), request, nil, nil)
	if err != nil || evidence.Status != action.InspectionIncomplete || evidence.Reason != action.ReasonLimitExceeded {
		t.Fatalf("limit evidence = %#v, error = %v", evidence, err)
	}

	deadlineContext, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	evidence, err = engine.Inspect(deadlineContext, request, nil, nil)
	if err != nil || evidence.Status != action.InspectionIncomplete || evidence.Reason != action.ReasonDeadlineExceeded {
		t.Fatalf("deadline evidence = %#v, error = %v", evidence, err)
	}
}

func TestDetectorScanFindsMatchesAcrossChunkBoundaries(t *testing.T) {
	t.Parallel()
	pack, err := compileBuiltinPack()
	if err != nil {
		t.Fatal(err)
	}
	marker := "ignore previous instructions"
	text := strings.Repeat("x", builtinPackScanChunk-len(marker)/2) + marker
	findings, err := pack.scan(
		context.Background(), text,
		map[action.DetectorCategory]struct{}{action.DetectorPromptInjection: {}}, nil,
		action.MaxArgumentBytes,
	)
	if err != nil || len(findings) != 1 || findings[0].RuleID != "prompt-injection-direct" {
		t.Fatalf("boundary findings = %#v, error = %v", findings, err)
	}
	if _, err := pack.scan(
		context.Background(), "ﬃﬃ", map[action.DetectorCategory]struct{}{action.DetectorSecret: {}}, nil, 4,
	); !errors.Is(err, errInspectionLimit) {
		t.Fatalf("normalization expansion error = %v", err)
	}
}
