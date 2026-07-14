package hooks

import (
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
