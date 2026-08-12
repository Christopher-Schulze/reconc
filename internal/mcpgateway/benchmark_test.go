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
)

func BenchmarkToolDiscovery(b *testing.B) {
	for _, test := range []struct {
		name   string
		unique bool
	}{
		{name: "shared-schemas"},
		{name: "distinct-schemas", unique: true},
	} {
		b.Run(test.name, func(b *testing.B) {
			benchmarkToolDiscovery(b, test.unique)
		})
	}
}

func benchmarkToolDiscovery(b *testing.B, unique bool) {
	tools := make([]json.RawMessage, 128)
	for index := range tools {
		property := "value"
		if unique {
			property = fmt.Sprintf("value-%03d", index)
		}
		tools[index] = json.RawMessage(fmt.Sprintf(
			`{"name":"tool-%03d","inputSchema":{"type":"object","properties":{%q:{"type":"string"}},"additionalProperties":false}}`,
			index, property,
		))
	}
	gateway := &Gateway{downstream: &catalogDownstream{pages: map[string]ToolPage{
		"": {Tools: tools},
	}}}
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		contracts, err := gateway.discoverTools(context.Background())
		if err != nil || len(contracts) != len(tools) {
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
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		result, err := harness.session.CallTool(context.Background(), params)
		if err != nil || result == nil || result.IsError {
			b.Fatalf("gateway call = %#v, %v", result, err)
		}
	}
}
