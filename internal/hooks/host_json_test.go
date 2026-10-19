package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostJSONKeepsShellOperatorsReadableAndAcceptsLegacyEscapes(t *testing.T) {
	repo := t.TempDir()
	if _, err := Install(KindGitHubCopilot, repo, false); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repo, filepath.FromSlash(GitHubCopilotHooksPath))
	current, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(current), `"& sh `) || strings.Contains(string(current), `\u0026`) {
		t.Fatalf("installed host JSON must keep shell operators unescaped:\n%s", current)
	}
	fresh, err := InspectPlatform(repo, KindGitHubCopilot)
	if err != nil {
		t.Fatal(err)
	}
	var legacy bytes.Buffer
	json.HTMLEscape(&legacy, current)
	if !strings.Contains(legacy.String(), `\u0026`) {
		t.Fatal("fixture must contain an escaped operator")
	}
	if err := os.WriteFile(target, legacy.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	upgraded, err := InspectPlatform(repo, KindGitHubCopilot)
	if err != nil {
		t.Fatal(err)
	}
	if upgraded.State != fresh.State || upgraded.Detail != fresh.Detail {
		t.Fatalf("a legacy-escaped install must keep its status: fresh=%s/%q legacy=%s/%q", fresh.State, fresh.Detail, upgraded.State, upgraded.Detail)
	}
	if err := os.WriteFile(target, bytes.Replace(legacy.Bytes(), []byte("copilot-stop"), []byte("copilot-stopp"), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	drifted, err := InspectPlatform(repo, KindGitHubCopilot)
	if err != nil {
		t.Fatal(err)
	}
	if drifted.State != StateDegraded {
		t.Fatalf("a changed command must still be drift: %s/%q", drifted.State, drifted.Detail)
	}
}
