package customruntime

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestBoundResponseNeverBreaksTheHostContract pins the three properties a host
// depends on when a reason is too long to ship: the body stays inside the
// declared budget, it stays parseable JSON, and the decision survives.
// Truncating into the middle of a multi-byte rune or dropping the decision
// would turn a block into an unreadable answer, which a host reads as no
// answer at all.
func TestBoundResponseNeverBreaksTheHostContract(t *testing.T) {
	reasons := map[string]string{
		"empty":              "",
		"short":              "policy blocked this write",
		"long ascii":         strings.Repeat("a", 10000),
		"multi-byte latin":   strings.Repeat("ä", 5000),
		"multi-byte emoji":   strings.Repeat("🙂", 3000),
		"json metacharacter": strings.Repeat("\"\\\n\t", 2000),
	}
	budgets := []int{256, 257, 300, 512, 1024, 4096}
	for name, reason := range reasons {
		for _, budget := range budgets {
			t.Run(name, func(t *testing.T) {
				response := NeutralResponse{
					Schema: NeutralResponseSchemaURL, FormatVersion: ResponseFormatVersion,
					Runtime: "custom:probe", HostEvent: "before-tool", Event: "pre-tool-use",
					Decision: DecisionBlock, ExitCode: 2, Reason: reason,
				}
				body, err := BoundResponse(response, budget)
				if err != nil {
					metadata := response
					metadata.Reason = ""
					if budget >= len(MarshalResponse(metadata)) {
						t.Fatalf("budget %d fits metadata but was rejected: %v", budget, err)
					}
					return
				}
				if len(body) > budget {
					t.Fatalf("budget %d produced %d bytes", budget, len(body))
				}
				var decoded NeutralResponse
				if err := json.Unmarshal(body, &decoded); err != nil {
					t.Fatalf("budget %d produced invalid JSON: %v\n%s", budget, err, body)
				}
				if decoded.Decision != DecisionBlock || decoded.ExitCode != 2 {
					t.Fatalf("budget %d lost the decision: %+v", budget, decoded)
				}
			})
		}
	}
}

// TestBoundResponseRejectsAnUnusableBudget keeps the floor explicit: below it
// even a reason-free response cannot be expressed, and silently emitting an
// oversized body would break the contract the budget exists to enforce.
func TestBoundResponseRejectsAnUnusableBudget(t *testing.T) {
	response := NeutralResponse{
		Schema: NeutralResponseSchemaURL, FormatVersion: ResponseFormatVersion,
		Runtime: "custom:probe", HostEvent: "before-tool", Event: "pre-tool-use",
		Decision: DecisionBlock, ExitCode: 2,
	}
	if _, err := BoundResponse(response, 255); err == nil {
		t.Fatal("a budget below the documented floor must be refused")
	}
	if _, err := BoundResponse(response, 0); err == nil {
		t.Fatal("a zero budget must be refused")
	}
}
