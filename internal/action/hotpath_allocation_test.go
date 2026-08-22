package action

import (
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
	trace, complete, omitted := collector.finish()
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
	trace, complete, omitted := collector.finish()
	if complete || omitted != 2 || len(trace) != 1 || trace[0].RuleID != "trace-overflow" || trace[0].Omitted != 2 {
		t.Fatalf("byte-bounded trace = %#v, complete %t, omitted %d", trace, complete, omitted)
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
