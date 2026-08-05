package cli

import (
	"testing"

	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestEveryRegisteredRuntimeRouteHasNormalizedHandler(t *testing.T) {
	for _, event := range hooks.RuntimeEvents() {
		route, ok := hooks.RuntimeEvent(event)
		if !ok {
			t.Fatalf("registry lost runtime event %q", event)
		}
		handler, executable := hookHandlerForRoute(event, route)
		if !executable || handler == "" {
			t.Errorf("runtime event %q (%s/%s) has no normalized handler", event, route.PlatformKind, route.Event)
		}
	}
}

func TestNormalizedHandlerPreservesEventSpecificRouting(t *testing.T) {
	tests := []struct {
		event string
		want  agentsession.HookHandler
	}{
		{event: "omp-permission-request", want: agentsession.HookHandlerPassive},
		{event: "opencode-post-tool-use", want: agentsession.HookHandlerMCPAwarePostToolUse},
		{event: "codex-post-tool-use", want: agentsession.HookHandlerPostToolUseComplete},
		{event: "cursor-subagent-stop", want: agentsession.HookHandlerStop},
		{event: "antigravity-stop", want: agentsession.HookHandlerAntigravityStop},
	}
	for _, test := range tests {
		route, ok := hooks.RuntimeEvent(test.event)
		if !ok {
			t.Fatalf("missing registry fixture %q", test.event)
		}
		got, executable := hookHandlerForRoute(test.event, route)
		if !executable || got != test.want {
			t.Errorf("handler for %q = %q/%t, want %q/true", test.event, got, executable, test.want)
		}
	}
}
