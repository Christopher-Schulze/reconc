package cli

import (
	"testing"

	"reconc.dev/reconc/internal/hooks"
)

func TestInheritedDevinProjectDirNeverSkipsABlockingGate(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("DEVIN_PROJECT_DIR", repo)
	claudePayload := []byte(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"}}`)
	blocking, observing := 0, 0
	for _, event := range []string{"claude-pre-tool-use", "claude-permission-request", "claude-stop", "claude-post-tool-use", "claude-session-start"} {
		route, ok := hooks.RuntimeEvent(event)
		if !ok {
			t.Fatalf("unknown route %s", event)
		}
		owned := devinOwnsEvent(route, claudePayload, repo)
		if route.ErrorPolicy == hooks.FailureBlock {
			blocking++
			if owned {
				t.Fatalf("%s was deferred to Devin on the inherited environment alone", event)
			}
			if !devinOwnsEvent(route, []byte(`{"source":"devin-cli"}`), repo) {
				t.Fatalf("%s ignored an explicit Devin payload marker", event)
			}
		} else {
			observing++
			if !owned {
				t.Fatalf("%s lost its duplicate suppression under DEVIN_PROJECT_DIR", event)
			}
		}
	}
	if blocking == 0 || observing == 0 {
		t.Fatalf("fixture covers %d blocking and %d observing routes", blocking, observing)
	}
}
