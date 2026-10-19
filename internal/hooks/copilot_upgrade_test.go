package hooks

import (
	"encoding/json"
	"testing"
)

func TestCopilotOwnershipAcceptsAnOlderGeneratedRouteSubset(t *testing.T) {
	artifact, err := Generate(KindGitHubCopilot)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]interface{}
	if err := json.Unmarshal([]byte(artifact.Content), &document); err != nil {
		t.Fatal(err)
	}
	hooks := document["hooks"].(map[string]interface{})
	delete(hooks, "Notification")
	body, err := json.Marshal(document)
	if err != nil || !isManagedGitHubCopilotConfig(body) {
		t.Fatalf("older generated subset lost ownership: %v", err)
	}
	entries := hooks["PreToolUse"].([]interface{})
	entries[0].(map[string]interface{})["bash"] = "echo copilot-pre-tool-use"
	body, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if isManagedGitHubCopilotConfig(body) {
		t.Fatal("a route name inside echo granted replacement ownership")
	}
}
