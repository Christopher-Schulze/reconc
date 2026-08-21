package mcpgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestValidateCatalogSortsAndRejectsDuplicateOrExcessTools(t *testing.T) {
	tool := func(name string) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"name":%q,"inputSchema":{"type":"object"}}`, name))
	}
	contracts, err := validateCatalog(context.Background(), []ToolPage{{
		Tools: []json.RawMessage{tool("zeta"), tool("alpha")},
	}})
	if err != nil || len(contracts) != 2 || contracts[0].Name != "alpha" || contracts[1].Name != "zeta" {
		t.Fatalf("sorted catalog = %#v, %v", contracts, err)
	}
	if _, err := validateCatalog(context.Background(), []ToolPage{
		{Tools: []json.RawMessage{tool("same")}},
		{Tools: []json.RawMessage{tool("same")}},
	}); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("duplicate catalog error = %v", err)
	}
	pages := make([]ToolPage, 0, MaxTools/MaxToolsPerPage+1)
	toolIndex := 0
	for pageIndex := 0; pageIndex <= MaxTools/MaxToolsPerPage; pageIndex++ {
		count := MaxToolsPerPage
		if pageIndex == MaxTools/MaxToolsPerPage {
			count = 1
		}
		page := ToolPage{Tools: make([]json.RawMessage, count)}
		for index := range page.Tools {
			page.Tools[index] = tool(fmt.Sprintf("tool-%03d", toolIndex))
			toolIndex++
		}
		pages = append(pages, page)
	}
	if _, err := validateCatalog(context.Background(), pages); err == nil ||
		!strings.Contains(err.Error(), "boundary") {
		t.Fatalf("excess catalog error = %v", err)
	}
}

func TestValidateCatalogReusesIdenticalCompiledSchemas(t *testing.T) {
	tool := func(name, schema string) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"name":%q,"inputSchema":%s}`, name, schema))
	}
	shared := `{"type":"object","additionalProperties":false}`
	distinct := `{"type":"object","properties":{"value":{"type":"string"}}}`
	contracts, err := validateCatalog(context.Background(), []ToolPage{{Tools: []json.RawMessage{
		tool("alpha", shared), tool("beta", shared), tool("gamma", distinct),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if contracts[0].InputSchema != contracts[1].InputSchema {
		t.Fatal("identical canonical schemas were compiled more than once")
	}
	if contracts[0].InputSchema == contracts[2].InputSchema {
		t.Fatal("distinct canonical schemas shared one compiled schema")
	}
}

func TestDiscoverToolsRejectsRepeatedCursor(t *testing.T) {
	downstream := &catalogDownstream{pages: map[string]ToolPage{
		"":       {NextCursor: "repeat"},
		"repeat": {NextCursor: "repeat"},
	}}
	gateway := &Gateway{downstream: downstream}
	if _, err := gateway.discoverTools(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "cursor repeated") {
		t.Fatalf("repeated cursor error = %v", err)
	}
}

func TestDiscoverToolsRejectsAggregateBytesBeforeFollowingCursor(t *testing.T) {
	tools := make([]json.RawMessage, 33)
	for index := range tools {
		tools[index] = paddedCatalogTool(t, fmt.Sprintf("large-%02d", index), MaxToolMetadataBytes)
	}
	downstream := &catalogDownstream{pages: map[string]ToolPage{
		"": {Tools: tools, NextCursor: "must-not-be-requested"},
	}}
	gateway := &Gateway{downstream: downstream}
	_, err := gateway.discoverTools(context.Background())
	if cause := errors.Unwrap(err); cause == nil || !strings.Contains(cause.Error(), "boundary") {
		t.Fatalf("aggregate catalog error = %v (cause %v)", err, cause)
	}
	if downstream.listCalls != 1 {
		t.Fatalf("downstream page requests = %d, want 1", downstream.listCalls)
	}
}

func paddedCatalogTool(t *testing.T, name string, size int) json.RawMessage {
	t.Helper()
	prefix := fmt.Sprintf(`{"name":%q,"inputSchema":{"type":"object","description":"`, name)
	suffix := `"}}`
	if size < len(prefix)+len(suffix) {
		t.Fatalf("tool size %d is smaller than its metadata envelope", size)
	}
	tool := json.RawMessage(prefix + strings.Repeat("a", size-len(prefix)-len(suffix)) + suffix)
	if len(tool) != size {
		t.Fatalf("padded tool bytes = %d, want %d", len(tool), size)
	}
	return tool
}

type catalogDownstream struct {
	pages     map[string]ToolPage
	listCalls int
}

func (*catalogDownstream) ProtocolVersion() string { return "2026-07-28" }

func (d *catalogDownstream) ListTools(_ context.Context, cursor string) (ToolPage, error) {
	d.listCalls++
	page, exists := d.pages[cursor]
	if !exists {
		return ToolPage{}, fmt.Errorf("unexpected cursor %q", cursor)
	}
	return page, nil
}

func (*catalogDownstream) CallTool(
	context.Context,
	string,
	json.RawMessage,
	ProgressSink,
) (CallResult, error) {
	return CallResult{}, fmt.Errorf("unexpected tool call")
}

func (*catalogDownstream) Close() error { return nil }
func (*catalogDownstream) Wait() error  { return nil }
