package actioninspect

import (
	"context"
	"strings"
	"sync"
	"testing"

	"reconc.dev/reconc/internal/action"
)

func TestEngineFactoryReusesConcurrentDetectorPrograms(t *testing.T) {
	factory, err := NewEngineFactory()
	if err != nil {
		t.Fatal(err)
	}
	compiled := testCompiledPlan(t, action.PhasePreCall, []action.DetectorCategory{action.DetectorSecret}, nil, BuiltinPackIdentity())
	key := testIdentityKey{id: strings.Repeat("a", 32), key: []byte(strings.Repeat("k", 32))}
	first, err := factory.NewEngine(compiled, key)
	if err != nil {
		t.Fatal(err)
	}
	second, err := factory.NewEngine(compiled, key)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.pack.rules) == 0 || &first.pack.rules[0] != &second.pack.rules[0] {
		t.Fatal("engines did not share the immutable compiled detector pack")
	}
	arguments := mustValue(t, `{"payload":"ordinary value"}`)
	request := action.Request{
		Transport: action.TransportMCPStdio, ServerLabel: "server", Tool: "inspect",
		Phase: action.PhasePreCall, Arguments: &arguments,
	}
	const workers = 16
	var wait sync.WaitGroup
	wait.Add(workers)
	for index := 0; index < workers; index++ {
		go func() {
			defer wait.Done()
			engine, engineErr := factory.NewEngine(compiled, key)
			if engineErr != nil {
				t.Error(engineErr)
				return
			}
			evidence, inspectErr := engine.Inspect(context.Background(), request, nil, nil)
			if inspectErr != nil || evidence == nil || evidence.Status != action.InspectionClean {
				t.Errorf("concurrent inspection = %#v, %v", evidence, inspectErr)
			}
		}()
	}
	wait.Wait()
}

func TestEngineFactoryFailsClosedWhenUnavailable(t *testing.T) {
	var factory *EngineFactory
	if _, err := factory.NewEngine(nil, nil); err == nil {
		t.Fatal("nil engine factory was accepted")
	}
}

func BenchmarkEngineFactoryNewEngine(b *testing.B) {
	factory, err := NewEngineFactory()
	if err != nil {
		b.Fatal(err)
	}
	compiled := testCompiledPlan(b, action.PhasePreCall, []action.DetectorCategory{action.DetectorSecret}, nil, BuiltinPackIdentity())
	key := testIdentityKey{id: strings.Repeat("a", 32), key: []byte(strings.Repeat("k", 32))}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := factory.NewEngine(compiled, key); err != nil {
			b.Fatal(err)
		}
	}
}
