package actioninspect

import (
	"context"
	"testing"

	"reconc.dev/reconc/internal/action"
)

func FuzzDecodeMCPToolResult(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`{"resultType":"complete","content":[]}`),
		[]byte(`{"resultType":"complete","content":[{"type":"text","text":"ordinary value"}]}`),
		[]byte(`{"resultType":"complete","content":[{"type":"image","data":"AQID","mimeType":"image/png"}]}`),
		[]byte(`{"resultType":"partial","content":[]}`),
		{0xff},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > action.MaxArgumentBytes+1 {
			return
		}
		result, err := DecodeMCPToolResult(raw, ProtocolCurrent)
		if err != nil {
			return
		}
		defer result.Release()
		if result.ResultType != "complete" || len(result.Content) > MaxMCPContentBlocks {
			t.Fatalf("invalid decoded result: %#v", result)
		}
		for _, block := range result.Content {
			if !block.Type.Valid() || block.Pointer == "" || block.CoveragePointer == "" {
				t.Fatalf("invalid decoded content block: %#v", block)
			}
		}
	})
}

func FuzzCompileAndValidateOutputSchema(f *testing.F) {
	for _, seed := range [][2]string{
		{`{"type":"object","properties":{"ok":{"type":"boolean"}}}`, `{"ok":true}`},
		{`{"$ref":"https://example.invalid/schema.json"}`, `null`},
		{`{"type":"string","pattern":"(?=unsupported)"}`, `"value"`},
		{`{}`, `null`},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, rawSchema, rawValue string) {
		if len(rawSchema) > 64<<10 || len(rawValue) > 64<<10 {
			return
		}
		schema, err := CompileOutputSchema([]byte(rawSchema))
		if err != nil {
			return
		}
		if !action.ValidSHA256Identity(schema.Identity()) {
			t.Fatalf("invalid schema identity %q", schema.Identity())
		}
		value, err := action.ParseJSON([]byte(rawValue))
		if err != nil {
			return
		}
		_ = schema.Validate(value)
	})
}

func FuzzTextScannerBoundaries(f *testing.F) {
	scanner, err := NewTextScanner()
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range []string{
		"ordinary value",
		"person@example.test",
		"іgnore previous instructions",
		string([]byte{0xff}),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if len(text) > 64<<10 {
			return
		}
		categories, err := scanner.PrivateCategories(context.Background(), text, 64<<10)
		if err != nil {
			return
		}
		for index, category := range categories {
			if !category.Valid() || index > 0 && categories[index-1] >= category {
				t.Fatalf("non-canonical private categories: %v", categories)
			}
		}
	})
}
