package mcpgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionstate"
)

func BenchmarkToolDiscovery(b *testing.B) {
	for _, test := range []struct {
		name         string
		unique       bool
		pages        int
		toolsPerPage int
	}{
		{name: "shared-schemas", pages: 1, toolsPerPage: MaxToolsPerPage},
		{name: "distinct-schemas", unique: true, pages: 1, toolsPerPage: MaxToolsPerPage},
		{name: "max-paginated", pages: MaxTools / MaxToolsPerPage, toolsPerPage: MaxToolsPerPage},
	} {
		b.Run(test.name, func(b *testing.B) {
			benchmarkToolDiscovery(b, test.unique, test.pages, test.toolsPerPage)
		})
	}
}

func BenchmarkParseFrameSmall(b *testing.B) {
	benchmarkParseFrame(b, []byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
}

func BenchmarkParseFrameProgress(b *testing.B) {
	benchmarkParseFrame(b, []byte(`{"jsonrpc":"2.0","method":"notifications/progress","params":{"progressToken":"token","progress":1,"message":"working"}}`))
}

func BenchmarkParseFrameRepresentative(b *testing.B) {
	benchmarkParseFrame(b, []byte(`{"jsonrpc":"2.0","id":"request","method":"tools/call","params":{"name":"echo","arguments":{"value":"payload","items":[1,2,3,4]}}}`))
}

func benchmarkParseFrame(b *testing.B, frame []byte) {
	b.Helper()
	b.ReportAllocs()
	b.SetBytes(int64(len(frame)))
	for b.Loop() {
		if _, err := parseFrameJSON(frame); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkToolDiscovery(b *testing.B, unique bool, pageCount, toolsPerPage int) {
	pages := make(map[string]ToolPage, pageCount)
	toolIndex := 0
	cursor := ""
	for pageIndex := 0; pageIndex < pageCount; pageIndex++ {
		tools := make([]json.RawMessage, toolsPerPage)
		for index := range tools {
			property := "value"
			if unique {
				property = fmt.Sprintf("value-%03d", toolIndex)
			}
			tools[index] = json.RawMessage(fmt.Sprintf(
				`{"name":"tool-%03d","inputSchema":{"type":"object","properties":{%q:{"type":"string"}},"additionalProperties":false}}`,
				toolIndex, property,
			))
			toolIndex++
		}
		next := ""
		if pageIndex+1 < pageCount {
			next = fmt.Sprintf("page-%d", pageIndex+1)
		}
		pages[cursor] = ToolPage{Tools: tools, NextCursor: next}
		cursor = next
	}
	gateway := &Gateway{downstream: &catalogDownstream{pages: pages}}
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		contracts, err := gateway.discoverTools(context.Background())
		if err != nil || len(contracts) != toolIndex {
			b.Fatalf("discover tools: %d contracts, %v", len(contracts), err)
		}
	}
}

func BenchmarkGatewayCallEndToEnd(b *testing.B) {
	directory := b.TempDir()
	b.Setenv(fakeProcessEnvironment, "1")
	b.Setenv(fakeMarkerEnvironment, filepath.Join(directory, "invoked"))
	b.Setenv(fakeModeEnvironment, "normal")
	b.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(directory, "cancelled"))
	plan, err := action.CompilePlan(action.Plan{
		Tools: []action.Tool{{
			ID: "echo-tool", Transport: action.TransportMCPStdio,
			ServerLabel: "fake", Tool: "echo", Effect: action.Effect{Kind: action.EffectExternal},
			Origin: action.OriginActions, SourceIdentity: "benchmark-policy",
		}},
		Rules: []action.Rule{}, Budgets: []action.Budget{},
		Approvals: []action.ApprovalDisclosure{}, Detectors: []action.DetectorPolicy{},
		Defaults: action.FrozenDefaults(), Ledger: &action.LedgerPolicy{
			Mode: action.LedgerOff, ToolIdentity: action.LedgerDeclarationID,
			SelectedFields: []action.LedgerField{},
		},
	})
	if err != nil {
		b.Fatal(err)
	}
	evaluator, err := action.NewEvaluator(plan)
	if err != nil {
		b.Fatal(err)
	}
	harness := newGatewayLifecycleHarness(b, plan, evaluator, "", "", nil, 5*time.Second)
	params := &mcp.CallToolParams{
		Name: "echo", Arguments: json.RawMessage(`{"value":"benchmark"}`),
	}
	for _, concurrency := range []int{1, MaxConcurrentCalls} {
		b.Run(fmt.Sprintf("concurrency-%d", concurrency), func(b *testing.B) {
			semaphore := make(chan struct{}, concurrency)
			b.ReportAllocs()
			b.RunParallel(func(worker *testing.PB) {
				for worker.Next() {
					semaphore <- struct{}{}
					result, callErr := harness.session.CallTool(context.Background(), params)
					<-semaphore
					if callErr != nil || result == nil || result.IsError {
						b.Errorf("gateway call = %#v, %v", result, callErr)
						return
					}
				}
			})
		})
	}
}

func BenchmarkGatewayPreDispatchEvidenceContention(b *testing.B) {
	directory := b.TempDir()
	b.Setenv(fakeProcessEnvironment, "1")
	b.Setenv(fakeMarkerEnvironment, filepath.Join(directory, "invoked"))
	b.Setenv(fakeModeEnvironment, "normal")
	b.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(directory, "cancelled"))
	plan, err := action.CompilePlan(action.Plan{
		Tools: []action.Tool{{
			ID: "echo-tool", Transport: action.TransportMCPStdio,
			ServerLabel: "fake", Tool: "echo", Effect: action.Effect{Kind: action.EffectExternal},
			Origin: action.OriginActions, SourceIdentity: "benchmark-policy",
		}},
		Rules: []action.Rule{{
			ID: "block-echo", Selector: action.Selector{ToolIDs: []string{"echo-tool"}},
			Decision: action.DecisionBlock, OnIndeterminate: action.DecisionBlock,
			Cache: action.CacheExact, SourceIdentity: "benchmark-policy",
		}},
		Budgets: []action.Budget{}, Approvals: []action.ApprovalDisclosure{},
		Detectors: []action.DetectorPolicy{}, Defaults: action.FrozenDefaults(),
		Ledger: &action.LedgerPolicy{
			Mode: action.LedgerOff, ToolIdentity: action.LedgerDeclarationID,
			SelectedFields: []action.LedgerField{},
		},
	})
	if err != nil {
		b.Fatal(err)
	}
	evaluator, err := action.NewEvaluator(plan)
	if err != nil {
		b.Fatal(err)
	}
	harness := newGatewayLifecycleHarness(b, plan, evaluator, "", "", nil, 5*time.Second)
	harness.gateway.config.EvidenceProvider = evidenceProviderFunc(func(
		ctx context.Context,
		_ PolicySnapshot,
		_ action.Request,
		_ action.Tool,
	) (EvidenceSnapshot, error) {
		timer := time.NewTimer(10 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			return cleanEvidenceSnapshot(), nil
		case <-ctx.Done():
			return EvidenceSnapshot{}, ctx.Err()
		}
	})
	contract, generation, exists := harness.gateway.tool("echo")
	if !exists {
		b.Fatal("gateway echo contract is unavailable")
	}
	request := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Name: "echo", Arguments: json.RawMessage(`{"value":"benchmark"}`),
	}}
	wire := upstreamWireCall{
		id: json.RawMessage(`1`),
		params: json.RawMessage(
			`{"name":"echo","arguments":{"value":"benchmark"}}`,
		),
	}
	for _, concurrency := range []int{1, MaxConcurrentCalls} {
		b.Run(fmt.Sprintf("concurrency-%d", concurrency), func(b *testing.B) {
			semaphore := make(chan struct{}, concurrency)
			b.RunParallel(func(worker *testing.PB) {
				for worker.Next() {
					semaphore <- struct{}{}
					callID, callIDErr := actionstate.NewRandomCallID()
					if callIDErr != nil {
						<-semaphore
						b.Error(callIDErr)
						return
					}
					result, callErr := harness.gateway.startCall(
						context.Background(), request, wire, contract, generation, callID,
					)
					<-semaphore
					if callErr != nil || result == nil || !result.IsError {
						b.Errorf("blocked gateway call = %#v, %v", result, callErr)
						return
					}
				}
			})
		})
	}
}
