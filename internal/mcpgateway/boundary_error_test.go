package mcpgateway

import (
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"reconc.dev/reconc/internal/action"
)

func TestBoundaryErrorRedactsCauseAndPreservesIdentity(t *testing.T) {
	cause := errors.New("private downstream value")
	err := wrapBoundaryError("initialize downstream MCP session", cause)
	if err == nil || err.Error() != "initialize downstream MCP session failed" {
		t.Fatalf("boundary error = %v", err)
	}
	if strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("boundary error leaked cause: %v", err)
	}
	if !errors.Is(err, cause) {
		t.Fatal("boundary error did not preserve cause identity")
	}
	if wrapBoundaryError("unused", nil) != nil {
		t.Fatal("nil cause produced a boundary error")
	}
}

func TestBlockedGatewayResultExposesOnlyTheSafeReasonCategory(t *testing.T) {
	t.Parallel()
	for _, reason := range []action.ReasonCode{
		action.ReasonApprovalRequired,
		action.ReasonBudgetExhausted,
		action.ReasonRuleMatched,
	} {
		result := blockedGatewayResultValue("act_aaaaaaaaaaaaaaaaaaaaaaaaaa", reason)
		content, ok := result.Content[0].(*mcp.TextContent)
		if !ok || content.Text != "Reconc blocked this tool call ("+string(reason)+")." {
			t.Fatalf("blocked %s content = %#v", reason, result.Content)
		}
	}
}
