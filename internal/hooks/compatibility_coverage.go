package hooks

import (
	"encoding/json"
	"regexp"
	"strings"
)

// compatibilityNativeAliases names the first-class native event a host fires
// for the same call when it runs another host's hooks under a different
// native event name.
var compatibilityNativeAliases = map[string]map[string]string{
	KindCursor: {"UserPromptSubmit": "beforeSubmitPrompt"},
}

// FirstClassRouteCovers reports whether the generated first-class hooks of
// kind run for the same host call that reached route with toolName. Only an
// entry registered for the same native event (compared without case, or its
// documented alias) counts, and only when its matcher is absent or fully
// matches toolName without regard to case. A host that runs another host's
// hooks next to its own fires both for one call, so a compatible duplicate
// may defer to the first-class route only when that route provably evaluates
// the call.
func FirstClassRouteCovers(kind string, route RuntimeRoute, toolName string) bool {
	native := route.NativeEvent
	if alias, ok := compatibilityNativeAliases[kind][native]; ok {
		native = alias
	}
	if native == "" {
		return false
	}
	artifact, err := Generate(kind)
	if err != nil {
		return false
	}
	entries, ok := firstClassNativeEntries(artifact.Content)
	if !ok {
		return false
	}
	for event, list := range entries {
		if !strings.EqualFold(event, native) {
			continue
		}
		for _, entry := range list {
			if matcherCoversTool(entry.Matcher, toolName) {
				return true
			}
		}
	}
	return false
}

type firstClassEntry struct {
	Matcher *string `json:"matcher"`
}

// firstClassNativeEntries decodes the native event entries of a generated
// JSON hook artifact, whether nested under "hooks" or at the top level.
func firstClassNativeEntries(content string) (map[string][]firstClassEntry, bool) {
	var document map[string]json.RawMessage
	if json.Unmarshal([]byte(content), &document) != nil {
		return nil, false
	}
	if nested, ok := document["hooks"]; ok {
		if json.Unmarshal(nested, &document) != nil {
			return nil, false
		}
	}
	entries := make(map[string][]firstClassEntry, len(document))
	for event, raw := range document {
		var list []firstClassEntry
		if json.Unmarshal(raw, &list) == nil {
			entries[event] = list
		}
	}
	return entries, true
}

func matcherCoversTool(matcher *string, toolName string) bool {
	if matcher == nil || *matcher == "" {
		return true
	}
	// Hosts reuse their own tool names in both hook formats but not always
	// their casing, so the comparison ignores case.
	pattern, err := regexp.Compile("(?i)^(?:" + *matcher + ")$")
	return err == nil && pattern.MatchString(toolName)
}
