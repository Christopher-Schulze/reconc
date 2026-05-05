package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRunVerifyWarnsWhenBinaryLacksHookRuntime(t *testing.T) {
	repo := makeCheckRepo(t,
		"rules:\n  - id: deny-generated\n    kind: deny_write\n    paths: ['generated/**']\n    mode: warn\n    message: generated files are read-only\n")
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	config := "{\n  \"hooks\": {\n    \"SessionStart\": [\n      {\n        \"hooks\": [\n          {\n            \"type\": \"command\",\n            \"command\": \"reconc hook runtime claude-session-start \\\"$CLAUDE_PROJECT_DIR\\\"\"\n          }\n        ]\n      }\n    ]\n  }\n}\n"
	if err := os.WriteFile(filepath.Join(repo, ".claude", "settings.json"), []byte(config), 0o644); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}

	oldProbe := hookRuntimeSupportProbe
	hookRuntimeSupportProbe = func() bool { return false }
	defer func() { hookRuntimeSupportProbe = oldProbe }()

	var stdout, stderr bytes.Buffer
	if err := Run([]string{"verify", repo, "--json"}, "0.1.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("verify --json: %v", err)
	}

	var payload struct {
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Detail string `json:"detail"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("verify output should be valid JSON: %v\n%s", err, stdout.String())
	}

	found := false
	for _, check := range payload.Checks {
		if check.Name != "agent hooks runtime compatibility" {
			continue
		}
		found = true
		if check.Status != doctorStatusWarn {
			t.Fatalf("expected WARN, got %s", check.Status)
		}
		if check.Detail == "" {
			t.Fatal("expected non-empty detail for agent hooks runtime compatibility")
		}
	}
	if !found {
		t.Fatal("missing agent hooks runtime compatibility verify row")
	}
}
