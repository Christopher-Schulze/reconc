package mcpgateway

import (
	"context"
	"encoding/json"
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

type catalogDownstream struct {
	pages map[string]ToolPage
}

func (*catalogDownstream) ProtocolVersion() string { return "2026-07-28" }

func (d *catalogDownstream) ListTools(_ context.Context, cursor string) (ToolPage, error) {
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
