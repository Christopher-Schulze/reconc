package hooks

import (
	"sort"
	"testing"
)

// piHostEvents are the Pi lifecycle events this adapter subscribes to, taken
// from Pi's published extension API. Pinning them here makes a host-contract
// change visible as a failing test rather than as a silently missing route.
//
// Pi exposes further events that carry no policy decision for this product:
// session navigation vetoes (`session_before_switch`, `session_before_fork`,
// `session_before_tree`), provider and turn observations, and `project_trust`,
// which gates whether the project is trusted at all rather than whether one
// tool call is permitted. Reconc reads that trust decision through the status
// probe instead of answering it.
var piHostEvents = []string{
	"input",
	"agent_settled",
	"session_before_compact",
	"session_compact",
	"session_shutdown",
	"session_start",
	"tool_call",
	"tool_result",
	"user_bash",
}

// TestPiRegistersEveryPolicyRelevantHostEvent proves the Pi platform binds each
// host event the adapter claims, and binds nothing the host does not publish.
func TestPiRegistersEveryPolicyRelevantHostEvent(t *testing.T) {
	platform, ok := PlatformForKind(KindPi)
	if !ok {
		t.Fatal("Pi platform is not registered")
	}
	published := map[string]bool{}
	for _, event := range piHostEvents {
		published[event] = true
	}
	bound := map[string]bool{}
	for _, capability := range platform.Capabilities {
		if capability.Support == SupportUnsupported {
			continue
		}
		for _, binding := range capability.Bindings {
			if binding.NativeEvent == "" {
				continue
			}
			if !published[binding.NativeEvent] {
				t.Fatalf("Pi binds %q, which Pi's extension API does not publish", binding.NativeEvent)
			}
			bound[binding.NativeEvent] = true
		}
	}
	missing := []string{}
	for _, event := range piHostEvents {
		if !bound[event] {
			missing = append(missing, event)
		}
	}
	sort.Strings(missing)
	if len(missing) != 0 {
		t.Fatalf("Pi publishes policy-relevant events the adapter does not bind: %v", missing)
	}
}

// TestPiDegradedRoutesStayHonest keeps the registry from claiming a capability
// Pi cannot provide. Each of these has no host event behind it, so the route
// must stay unsupported rather than pretend to enforce.
func TestPiDegradedRoutesStayHonest(t *testing.T) {
	platform, ok := PlatformForKind(KindPi)
	if !ok {
		t.Fatal("Pi platform is not registered")
	}
	mustDegrade := map[Event]bool{
		EventPermissionRequest: true,
		EventMCPBefore:         true,
		EventMCPAfter:          true,
		EventStop:              true,
	}
	seen := map[Event]bool{}
	for _, capability := range platform.Capabilities {
		if !mustDegrade[capability.Event] {
			continue
		}
		seen[capability.Event] = true
		if capability.Support == SupportNative {
			t.Fatalf("Pi claims native support for %s, which its extension API does not publish", capability.Event)
		}
	}
	for event := range mustDegrade {
		if !seen[event] {
			t.Fatalf("Pi no longer carries a declared route for %s", event)
		}
	}
}
