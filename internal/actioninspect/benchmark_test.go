package actioninspect

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/action"
)

func BenchmarkTextScanRepresentative(b *testing.B) {
	benchmarkTextScan(b, strings.Repeat("ordinary repository output ", 160))
}

func BenchmarkTextScanMaximumLegal(b *testing.B) {
	benchmarkTextScan(b, strings.Repeat("a", action.MaxArgumentBytes))
}

func BenchmarkForbiddenTermsMaximumLegal(b *testing.B) {
	pack, err := compileBuiltinPack()
	if err != nil {
		b.Fatal(err)
	}
	text := strings.Repeat("a", action.MaxArgumentBytes)
	terms := make([]string, action.MaxForbiddenTerms)
	for index := range terms {
		terms[index] = fmt.Sprintf("forbidden-term-%03d-%s", index, strings.Repeat("z", 480))
	}
	categories := map[action.DetectorCategory]struct{}{action.DetectorForbiddenData: {}}
	b.ReportAllocs()
	b.SetBytes(int64(len(text)))
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		findings, err := pack.scan(context.Background(), text, categories, terms, action.MaxArgumentBytes)
		if err != nil || len(findings) != 0 {
			b.Fatalf("forbidden-term scan = %v, %v", findings, err)
		}
	}
}

func BenchmarkLikelySecretValueASCII(b *testing.B) {
	value := "q7m9v2p4r8x6l3n5"
	b.ReportAllocs()
	for iteration := 0; iteration < b.N; iteration++ {
		if !likelySecretValue(value) {
			b.Fatal("synthetic secret was not detected")
		}
	}
}

func benchmarkTextScan(b *testing.B, text string) {
	b.Helper()
	scanner, err := NewTextScanner()
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(text)))
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := scanner.PrivateCategories(context.Background(), text, uint64(len(text))); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStructuredJSONRepresentative(b *testing.B) {
	engine, request := testEngine(b, action.PhasePreCall, []action.DetectorCategory{
		action.DetectorSecret, action.DetectorPIIEmail, action.DetectorPromptInjection,
	}, nil)
	value := mustValue(b, `{"payload":{"status":"ok","items":["one","two","three"],"message":"ordinary value"}}`)
	request.Arguments = &value
	benchmarkEngine(b, engine, request, nil)
}

func BenchmarkStructuredJSONMaximumLegal(b *testing.B) {
	engine, request := testEngine(b, action.PhasePreCall, []action.DetectorCategory{action.DetectorSecret}, nil)
	first, err := action.String(strings.Repeat("a", action.MaxJSONStringBytes))
	if err != nil {
		b.Fatal(err)
	}
	second, err := action.String(strings.Repeat("b", action.MaxArgumentBytes-action.MaxJSONStringBytes-15))
	if err != nil {
		b.Fatal(err)
	}
	payload, err := action.Object([]action.Member{{Name: "a", Value: first}, {Name: "b", Value: second}})
	if err != nil {
		b.Fatal(err)
	}
	payloadBody, err := payload.MarshalJSON()
	if err != nil {
		b.Fatal(err)
	}
	if len(payloadBody) != action.MaxArgumentBytes {
		b.Fatalf("maximum structured payload bytes = %d, want %d", len(payloadBody), action.MaxArgumentBytes)
	}
	arguments, err := action.Object([]action.Member{{Name: "payload", Value: payload}})
	if err != nil {
		b.Fatal(err)
	}
	request.Arguments = &arguments
	benchmarkEngine(b, engine, request, nil)
}

func BenchmarkMaximumLegalContentArray(b *testing.B) {
	engine := testContentEngine(b, "", []action.DetectorCategory{
		action.DetectorSecret, action.DetectorPIIEmail, action.DetectorPromptInjection,
	}, nil)
	blocks := make([]string, MaxMCPContentBlocks)
	for index := range blocks {
		blocks[index] = `{"type":"text","text":"ordinary value"}`
	}
	raw := []byte(`{"resultType":"complete","content":[` + strings.Join(blocks, ",") + `]}`)
	result, err := DecodeMCPToolResult(raw, ProtocolCurrent)
	if err != nil {
		b.Fatal(err)
	}
	defer result.Release()
	request := action.Request{
		Transport: action.TransportMCPStdio, ServerLabel: "server", Tool: "inspect",
		Phase: action.PhasePostResult, Result: &result.Root,
	}
	benchmarkEngine(b, engine, request, result)
}

func benchmarkEngine(b *testing.B, engine *Engine, request action.Request, result *MCPToolResult) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		evidence, err := engine.Inspect(context.Background(), request, result, nil)
		if err != nil || evidence == nil || evidence.Status != action.InspectionClean {
			if evidence == nil {
				b.Fatalf("inspection evidence is nil, error = %v", err)
			}
			b.Fatalf(
				"inspection status = %q, reason = %q, fields = %d, unsupported = %d, error = %v",
				evidence.Status, evidence.Reason, len(evidence.Fields), len(evidence.UnsupportedContent), err,
			)
		}
	}
}
