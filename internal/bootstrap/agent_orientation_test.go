package bootstrap

import (
	"strings"
	"testing"
)

func TestBootstrapAgentOrientationIncludesMachineEntryPoints(t *testing.T) {
	for name, body := range map[string]string{
		"agent block": renderAgentBlock(),
		"start":       renderStart(),
	} {
		if !strings.Contains(body, "reconc session-briefing . --json") {
			t.Errorf("%s omits the machine-readable session entry point", name)
		}
	}
	if !strings.Contains(renderAgentBlock(), "reconc agent-intro --section NAME") {
		t.Error("agent block omits on-demand guide access")
	}
	if strings.Contains(renderStart(), "reconc status .") {
		t.Error("start duplicates session state through the legacy status command")
	}
}
