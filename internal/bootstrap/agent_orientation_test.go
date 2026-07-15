package bootstrap

import (
	"strings"
	"testing"
)

func TestBootstrapAgentOrientationUsesCompactMachineContract(t *testing.T) {
	for name, body := range map[string]string{
		"agent block": renderAgentBlock(),
		"start":       renderStart(),
	} {
		if !strings.Contains(body, "reconc session-briefing . --json") {
			t.Errorf("%s does not route agents through the compact JSON briefing:\n%s", name, body)
		}
	}
	if strings.Contains(renderStart(), "reconc status .") {
		t.Fatalf("start retained redundant policy status call:\n%s", renderStart())
	}
	if !strings.Contains(renderAgentBlock(), "reconc agent-intro --section NAME") {
		t.Fatalf("agent block does not expose on-demand guide sections:\n%s", renderAgentBlock())
	}
}
