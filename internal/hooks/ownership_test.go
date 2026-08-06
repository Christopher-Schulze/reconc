package hooks

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestReconcCommandOwnedRequiresAnExecutablePosition proves ownership is
// decided on what an entry runs, not on the wrapper path appearing anywhere in
// its text. A user hook that only mentions the path would otherwise be
// classified as Reconc-owned and dropped on the next install.
func TestReconcCommandOwnedRequiresAnExecutablePosition(t *testing.T) {
	cases := []struct {
		name      string
		signature string
		owned     bool
	}{
		{
			name:      "claude exec form",
			signature: commandSignature("${CLAUDE_PROJECT_DIR}/tools/reconc/bin/hook", []interface{}{"claude-stop", "${CLAUDE_PROJECT_DIR}"}),
			owned:     true,
		},
		{
			name:      "zcode interpreter exec form",
			signature: commandSignature("sh", []interface{}{WrapperPath, "zcode-pre-tool-use", "."}),
			owned:     true,
		},
		{
			name:      "plain shell form",
			signature: commandSignature("tools/reconc/bin/hook grok-stop .", nil),
			owned:     true,
		},
		{
			name:      "shell form with deny fallback",
			signature: commandSignature(`tools/reconc/bin/hook grok-pre-tool-use . || printf '%s\n' '{"decision":"deny"}'`, nil),
			owned:     true,
		},
		{
			name:      "renamed wrapper stays a modified reconc entry",
			signature: commandSignature("tools/reconc/bin/hook-custom", []interface{}{"claude-stop", "."}),
			owned:     true,
		},
		{
			name:      "reconc binary runtime route",
			signature: commandSignature("reconc hook runtime claude-stop .", nil),
			owned:     true,
		},
		{
			name:      "user hook that only names the wrapper in an argument",
			signature: commandSignature("/bin/echo", []interface{}{"our gate runs tools/reconc/bin/hook, do not remove"}),
			owned:     false,
		},
		{
			name:      "user hook that only echoes the wrapper path",
			signature: commandSignature(`echo "see tools/reconc/bin/hook for the managed gate"`, nil),
			owned:     false,
		},
		{
			name:      "unrelated user hook",
			signature: commandSignature("npm test", nil),
			owned:     false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := reconcCommandOwned(tc.signature); got != tc.owned {
				t.Fatalf("reconcCommandOwned(%q) = %v, want %v", tc.signature, got, tc.owned)
			}
		})
	}
}

// TestReconcOwnershipSurvivesEveryGeneratedEntry is the regression guard for
// the narrowing: losing ownership of a generated entry would duplicate managed
// hooks on every reinstall, which is worse than the false positive above.
func TestReconcOwnershipSurvivesEveryGeneratedEntry(t *testing.T) {
	checked := 0
	for _, platform := range Platforms() {
		artifact, err := Generate(platform.Kind)
		if err != nil {
			continue
		}
		for _, entry := range generatedHookEntries(t, artifact.Content) {
			if !hookEntryContainsReconcInvocation(entry) {
				t.Fatalf("%s generated an entry that is no longer recognised as Reconc-owned: %#v", platform.Kind, entry)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no generated hook entries were inspected")
	}
}

// generatedHookEntries collects the entry objects from both generated shapes:
// a flat event map and the nested hooks.events map.
func generatedHookEntries(t *testing.T, content string) []map[string]interface{} {
	t.Helper()
	var document map[string]interface{}
	if err := json.Unmarshal([]byte(content), &document); err != nil {
		return nil
	}
	hooks, _ := document["hooks"].(map[string]interface{})
	if hooks == nil {
		return nil
	}
	eventMaps := []map[string]interface{}{hooks}
	if nested, ok := hooks["events"].(map[string]interface{}); ok {
		eventMaps = append(eventMaps, nested)
	}
	entries := []map[string]interface{}{}
	for _, events := range eventMaps {
		for _, raw := range events {
			group, ok := raw.([]interface{})
			if !ok {
				continue
			}
			for _, item := range group {
				entry, ok := item.(map[string]interface{})
				if !ok {
					continue
				}
				if !hookEntryContainsReconcInvocation(entry) {
					continue
				}
				entries = append(entries, entry)
			}
		}
	}
	return entries
}

// TestMissingRuntimeEventsMatchesCompleteRouteTokens proves a route that is a
// prefix of another route is still reported as missing. Thirteen registered
// routes are prefixes of a sibling, for example claude-stop of
// claude-stop-failure.
func TestMissingRuntimeEventsMatchesCompleteRouteTokens(t *testing.T) {
	prefixes := map[string]string{}
	for _, platform := range Platforms() {
		for _, capability := range platform.Capabilities {
			for _, binding := range capability.Bindings {
				if binding.RuntimeEvent == "" || binding.Compatibility {
					continue
				}
				for _, other := range platform.Capabilities {
					for _, otherBinding := range other.Bindings {
						route := otherBinding.RuntimeEvent
						if route == binding.RuntimeEvent || route == "" {
							continue
						}
						if strings.HasPrefix(route, binding.RuntimeEvent) {
							prefixes[binding.RuntimeEvent] = route
						}
					}
				}
			}
		}
	}
	if len(prefixes) == 0 {
		t.Skip("no prefix-colliding routes are registered")
	}
	for _, platform := range Platforms() {
		for short, long := range prefixes {
			if !platformHasRoute(platform, short) {
				continue
			}
			// An artifact that carries only the longer route must still report
			// the shorter one as missing.
			content := `{"hooks":{"X":[{"command":"tools/reconc/bin/hook ` + long + ` ."}]}}`
			missing := missingRuntimeEvents(platform, content)
			found := false
			for _, event := range missing {
				if event == short {
					found = true
				}
			}
			if !found {
				t.Fatalf("%s: route %q is a prefix of %q and was not reported missing: %v", platform.Kind, short, long, missing)
			}
		}
	}
}

// TestMissingRuntimeEventsAcceptsInstalledRoutes keeps the token match from
// degenerating into "everything is missing".
func TestMissingRuntimeEventsAcceptsInstalledRoutes(t *testing.T) {
	for _, platform := range Platforms() {
		artifact, err := Generate(platform.Kind)
		if err != nil {
			continue
		}
		if missing := missingRuntimeEvents(platform, artifact.Content); len(missing) != 0 {
			t.Fatalf("%s generated artifact reports missing routes: %v", platform.Kind, missing)
		}
	}
}

func platformHasRoute(platform Platform, route string) bool {
	for _, capability := range platform.Capabilities {
		for _, binding := range capability.Bindings {
			if binding.RuntimeEvent == route && !binding.Compatibility {
				return true
			}
		}
	}
	return false
}
