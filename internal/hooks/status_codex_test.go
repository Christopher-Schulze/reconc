package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexActivationRejectsDuplicateFeatureTable(t *testing.T) {
	repo := t.TempDir()
	writeExecutableWrapper(t, repo)
	if _, err := Install(KindCodex, repo, false); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(repo, ".codex", "config.toml")
	if err := os.WriteFile(config, []byte("[features]\nhooks = true\n[features]\nexperimental = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if status := statusForKind(t, repo, KindCodex); status.State != StateDegraded {
		t.Fatalf("duplicate feature table status = %+v, want degraded", status)
	}
}

func TestCodexActivationStatusAcceptsQuotedHashesAndDottedKey(t *testing.T) {
	repo := t.TempDir()
	writeExecutableWrapper(t, repo)
	if _, err := Install(KindCodex, repo, false); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(repo, ".codex", "config.toml")
	if err := os.WriteFile(config, []byte("model = \"release#candidate\"\nfeatures.hooks = true # enabled\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if status := statusForKind(t, repo, KindCodex); status.State != StateConfigured {
		t.Fatalf("quoted/dotted activation status = %+v", status)
	}
}

func TestCodexActivationRejectsMissingRouteBudget(t *testing.T) {
	repo := t.TempDir()
	writeExecutableWrapper(t, repo)
	if _, err := Install(KindCodex, repo, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, filepath.FromSlash(CodexHooksPath))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	drifted := strings.Replace(string(data), "\"timeout\": 30,", "", 1)
	if drifted == string(data) {
		t.Fatal("generated Codex Stop route had no timeout to remove")
	}
	if err := os.WriteFile(path, []byte(drifted), 0o644); err != nil {
		t.Fatal(err)
	}
	status := statusForKind(t, repo, KindCodex)
	if status.State != StateDegraded || !strings.Contains(status.Detail, "timeout must be 30s") {
		t.Fatalf("missing Codex route budget status = %+v, want degraded", status)
	}
}

func TestCodexRouteBudgetUsesExactOwnedRouteCommands(t *testing.T) {
	artifact, err := Generate(KindCodex)
	if err != nil {
		t.Fatal(err)
	}
	platform, ok := PlatformForKind(KindCodex)
	if !ok {
		t.Fatal("Codex platform is not registered")
	}

	t.Run("longer route cannot satisfy prefix", func(t *testing.T) {
		drifted := strings.ReplaceAll(artifact.Content, "codex-stop", "codex-stop-failure")
		issues := codexRouteBudgetIssues([]byte(drifted), platform)
		if !containsString(issues, "codex-stop route count is 0") {
			t.Fatalf("route issues = %v", issues)
		}
	})

	t.Run("foreign mention is ignored", func(t *testing.T) {
		var document map[string]interface{}
		if err := json.Unmarshal([]byte(artifact.Content), &document); err != nil {
			t.Fatal(err)
		}
		hooks := document["hooks"].(map[string]interface{})
		groups := hooks["Stop"].([]interface{})
		group := groups[0].(map[string]interface{})
		handlers := group["hooks"].([]interface{})
		group["hooks"] = append(handlers, map[string]interface{}{
			"type": "command", "command": "echo codex-stop", "timeout": float64(30),
		})
		body, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		if issues := codexRouteBudgetIssues(body, platform); len(issues) != 0 {
			t.Fatalf("foreign command changed route budget: %v", issues)
		}
	})
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
