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
	secondFactory, err := NewEngineFactory()
	if err != nil {
		t.Fatal(err)
	}
	scanner, err := NewTextScanner()
	if err != nil {
		t.Fatal(err)
	}
	if &first.pack.rules[0] != &secondFactory.pack.rules[0] || &first.pack.rules[0] != &scanner.pack.rules[0] {
		t.Fatal("constructors did not share the process-wide detector programs")
	}
	wantPrograms := detectorProgramsSignature(first.pack)
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
			workerFactory, factoryErr := NewEngineFactory()
			if factoryErr != nil {
				t.Error(factoryErr)
				return
			}
			engine, engineErr := workerFactory.NewEngine(compiled, key)
			if engineErr != nil {
				t.Error(engineErr)
				return
			}
			workerScanner, scannerErr := NewTextScanner()
			if scannerErr != nil {
				t.Error(scannerErr)
				return
			}
			if &engine.pack.rules[0] != &first.pack.rules[0] || &workerScanner.pack.rules[0] != &first.pack.rules[0] {
				t.Error("concurrent constructor returned different detector programs")
				return
			}
			evidence, inspectErr := engine.Inspect(context.Background(), request, nil, nil)
			if inspectErr != nil || evidence == nil || evidence.Status != action.InspectionClean {
				t.Errorf("concurrent inspection = %#v, %v", evidence, inspectErr)
			}
			categories, scanErr := workerScanner.PrivateCategories(
				context.Background(), "api_key=Q7m9V2p4R8x6L3n5", action.MaxArgumentBytes,
			)
			if scanErr != nil || !containsCategory(categories, action.DetectorSecret) {
				t.Errorf("concurrent scan = %v, %v", categories, scanErr)
			}
		}()
	}
	wait.Wait()
	if got := detectorProgramsSignature(first.pack); got != wantPrograms {
		t.Fatal("concurrent constructors or scans mutated the shared detector programs")
	}
}

func detectorProgramsSignature(pack compiledDetectorPack) string {
	var signature strings.Builder
	signature.WriteString(pack.identity)
	for _, rule := range pack.rules {
		signature.WriteByte(0)
		signature.WriteString(rule.rule.ID)
		signature.WriteByte(0)
		if rule.pattern != nil {
			signature.WriteString(rule.pattern.String())
		}
		for _, marker := range rule.markers {
			signature.WriteByte(0)
			signature.WriteString(marker)
		}
	}
	return signature.String()
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
