package hooks

import (
	"encoding/json"
	"fmt"
)

func generateZCode() (*Artifact, error) {
	timeouts, err := requiredTimeouts(KindZCode,
		EventSessionStart, EventUserPromptSubmit, EventPreToolUse,
		EventPermissionRequest, EventPostToolUse, EventPostToolUseFailure, EventStop,
	)
	if err != nil {
		return nil, err
	}
	process := func(event string, lifecycle Event) map[string]interface{} {
		return map[string]interface{}{
			"type":      "process",
			"command":   "sh",
			"args":      []interface{}{WrapperPath, event, "."},
			"timeoutMs": timeouts[lifecycle] * 1000,
		}
	}
	entry := func(event string, lifecycle Event, matcher string) map[string]interface{} {
		group := map[string]interface{}{"hooks": []interface{}{process(event, lifecycle)}}
		if matcher != "" {
			group["matcher"] = matcher
		}
		return group
	}
	template := map[string]interface{}{
		"hooks": map[string]interface{}{
			"enabled": true,
			"events": map[string]interface{}{
				"SessionStart":       []interface{}{entry("zcode-session-start", EventSessionStart, "")},
				"UserPromptSubmit":   []interface{}{entry("zcode-user-prompt-submit", EventUserPromptSubmit, "")},
				"PreToolUse":         []interface{}{entry("zcode-pre-tool-use", EventPreToolUse, "*")},
				"PermissionRequest":  []interface{}{entry("zcode-permission-request", EventPermissionRequest, "*")},
				"PostToolUse":        []interface{}{entry("zcode-post-tool-use", EventPostToolUse, "*")},
				"PostToolUseFailure": []interface{}{entry("zcode-post-tool-use-failure", EventPostToolUseFailure, "*")},
				"Stop":               []interface{}{entry("zcode-stop", EventStop, "")},
			},
		},
	}
	data, _ := json.MarshalIndent(template, "", "  ")
	return &Artifact{Kind: KindZCode, TargetPath: ZCodeConfigPath, Content: string(data) + "\n"}, nil
}

func validateZCodeHookMergeShape(document map[string]interface{}) error {
	rawHooks, exists := document["hooks"]
	if !exists {
		return nil
	}
	hooks, ok := rawHooks.(map[string]interface{})
	if !ok {
		return fmt.Errorf("hooks must be an object, got %s", describeJSONType(rawHooks))
	}
	if rawEnabled, exists := hooks["enabled"]; exists {
		if _, ok := rawEnabled.(bool); !ok {
			return fmt.Errorf("hooks.enabled must be a boolean, got %s", describeJSONType(rawEnabled))
		}
	}
	rawEvents, exists := hooks["events"]
	if !exists {
		return nil
	}
	if _, ok := rawEvents.(map[string]interface{}); !ok {
		return fmt.Errorf("hooks.events must be an object, got %s", describeJSONType(rawEvents))
	}
	return nil
}

func repairZCodeHookMergeShape(document map[string]interface{}) {
	rawHooks, exists := document["hooks"]
	hooks, ok := rawHooks.(map[string]interface{})
	if !exists || !ok {
		document["hooks"] = map[string]interface{}{}
		return
	}
	if rawEvents, exists := hooks["events"]; exists {
		if _, ok := rawEvents.(map[string]interface{}); !ok {
			hooks["events"] = map[string]interface{}{}
		}
	}
	if rawEnabled, exists := hooks["enabled"]; exists {
		if _, ok := rawEnabled.(bool); !ok {
			delete(hooks, "enabled")
		}
	}
}
