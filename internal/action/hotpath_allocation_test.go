package action

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestTraceCollectorBoundsStorageWhileCollecting(t *testing.T) {
	collector := newTraceCollector(MaxRules)
	for index := 0; index < MaxRules; index++ {
		collector.add(TraceEntry{
			RuleID: strconv.Itoa(index), Selector: SelectorUnmatched,
			Condition: ConditionFalse, Completeness: true,
		})
	}
	if len(collector.entries) > MaxTraceEntries || cap(collector.entries) > MaxTraceEntries {
		t.Fatalf("collector retained %d entries with capacity %d", len(collector.entries), cap(collector.entries))
	}
	trace, complete, omitted, err := collector.finish()
	if err != nil {
		t.Fatal(err)
	}
	wantOmitted := MaxRules - (MaxTraceEntries - 1)
	if complete || len(trace) != MaxTraceEntries || omitted != wantOmitted {
		t.Fatalf("bounded trace = len %d, complete %t, omitted %d; want len %d, complete false, omitted %d", len(trace), complete, omitted, MaxTraceEntries, wantOmitted)
	}
	marker := trace[len(trace)-1]
	if marker.RuleID != "trace-overflow" || marker.Omitted != wantOmitted {
		t.Fatalf("overflow marker = %#v", marker)
	}
}

func TestTraceCollectorStopsAfterByteOverflow(t *testing.T) {
	collector := newTraceCollector(2)
	collector.add(TraceEntry{
		RuleID: strings.Repeat("x", MaxTraceBytes), Selector: SelectorUnmatched,
		Condition: ConditionFalse, Completeness: true,
	})
	collector.add(TraceEntry{RuleID: "later", Selector: SelectorUnmatched, Condition: ConditionFalse, Completeness: true})
	trace, complete, omitted, err := collector.finish()
	if err != nil {
		t.Fatal(err)
	}
	if complete || omitted != 2 || len(trace) != 1 || trace[0].RuleID != "trace-overflow" || trace[0].Omitted != 2 {
		t.Fatalf("byte-bounded trace = %#v, complete %t, omitted %d", trace, complete, omitted)
	}
}

func TestTraceCollectorLogicalBytesMatchCompactJSONArray(t *testing.T) {
	collector := newTraceCollector(MaxTraceEntries)
	assertTraceLogicalBytes(t, &collector)
	for index := 0; index < MaxTraceEntries; index++ {
		collector.add(TraceEntry{
			RuleID: strconv.Itoa(index), Selector: SelectorUnmatched,
			Condition: ConditionFalse, Completeness: true,
		})
		if index == 0 || index == MaxTraceEntries-1 {
			assertTraceLogicalBytes(t, &collector)
		}
	}
}

func TestTraceCollectorHonorsExactOneByteBoundary(t *testing.T) {
	entry := TraceEntry{Selector: SelectorUnmatched, Condition: ConditionFalse, Completeness: true}
	body, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	entry.RuleID = strings.Repeat("x", MaxTraceBytes-2-len(body))

	exact := newTraceCollector(1)
	exact.add(entry)
	if exact.omitted != 0 || len(exact.entries) != 1 || exact.logicalBytes != MaxTraceBytes {
		t.Fatalf("exact boundary = entries %d, omitted %d, bytes %d", len(exact.entries), exact.omitted, exact.logicalBytes)
	}

	over := newTraceCollector(1)
	entry.RuleID += "x"
	over.add(entry)
	if over.omitted != 1 || len(over.entries) != 0 || over.logicalBytes != 2 {
		t.Fatalf("one-byte overflow = entries %d, omitted %d, bytes %d", len(over.entries), over.omitted, over.logicalBytes)
	}
}

func assertTraceLogicalBytes(t *testing.T, collector *traceCollector) {
	t.Helper()
	body, err := json.Marshal(collector.entries)
	if err != nil {
		t.Fatal(err)
	}
	if collector.logicalBytes != len(body) {
		t.Fatalf("logical bytes = %d, compact JSON = %d for %d entries", collector.logicalBytes, len(body), len(collector.entries))
	}
}

func TestValueIndexedTraversalDoesNotExposeBackingSlices(t *testing.T) {
	one, _ := String("one")
	two, _ := String("two")
	array, err := Array([]Value{one, two})
	if err != nil {
		t.Fatal(err)
	}
	if length, ok := array.ArrayLen(); !ok || length != 2 {
		t.Fatalf("array length = %d, %t", length, ok)
	}
	if item, ok := array.ArrayItem(1); !ok || !item.Equal(two) {
		t.Fatalf("array item = %#v, %t", item, ok)
	}
	if _, ok := array.ArrayItem(-1); ok {
		t.Fatal("negative array index was accepted")
	}
	copyItems, _ := array.Items()
	copyItems[0] = two
	item, _ := array.ArrayItem(0)
	if !item.Equal(one) {
		t.Fatal("defensive Items copy mutated indexed traversal")
	}

	object, err := Object([]Member{{Name: "b", Value: two}, {Name: "a", Value: one}})
	if err != nil {
		t.Fatal(err)
	}
	if length, ok := object.ObjectLen(); !ok || length != 2 {
		t.Fatalf("object length = %d, %t", length, ok)
	}
	member, ok := object.ObjectMember(0)
	if !ok || member.Name != "a" || !member.Value.Equal(one) {
		t.Fatalf("sorted object member = %#v, %t", member, ok)
	}
	if _, ok := object.ObjectMember(2); ok {
		t.Fatal("out-of-range object index was accepted")
	}
}

func TestPointerTraversalAndSummaryDoNotAllocate(t *testing.T) {
	leaf := mustTestValue(t, `{"escaped":"line\\n<>&","items":[1,2,3]}`)
	nested, err := Array([]Value{leaf})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := leaf.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	wantBytes := len(encoded)
	allocations := testing.AllocsPerRun(1_000, func() {
		result := resolvePointerTokens(nested, []string{"0"})
		summary := summarizePointer(result)
		if summary.PointerState != PointerPresent || summary.Kind != ValueObject ||
			summary.ItemCount != 2 || summary.ByteLength != wantBytes {
			panic("pointer summary changed")
		}
	})
	if allocations != 0 {
		t.Fatalf("pointer traversal and summary allocated %.2f times per run", allocations)
	}
}

func TestMembershipAndMaximumRuntimeSizingDoNotAllocate(t *testing.T) {
	values := make([]Value, MaxListValues)
	for index := range values {
		values[index] = testStringValue(t, strconv.Itoa(index))
	}
	operand, err := Array(values)
	if err != nil {
		t.Fatal(err)
	}
	target := values[len(values)-1]
	if allocations := testing.AllocsPerRun(1_000, func() {
		state, reason := evaluateMembership(OperatorIn, target, operand)
		if state != ConditionTrue || reason != "" {
			panic("membership changed")
		}
	}); allocations != 0 {
		t.Fatalf("maximum membership allocated %.2f times per run", allocations)
	}

	maximum := benchmarkMaximumLegalValue(t)
	if allocations := testing.AllocsPerRun(3, func() {
		_, size, cloneErr := cloneRuntimeValue(maximum)
		if cloneErr != nil || size != MaxArgumentBytes {
			panic("runtime value sizing changed")
		}
	}); allocations != 0 {
		t.Fatalf("maximum runtime sizing allocated %.2f times per run", allocations)
	}
}
