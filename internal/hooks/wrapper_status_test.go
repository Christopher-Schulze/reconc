package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusReportsAnOutdatedManagedWrapper(t *testing.T) {
	repo := t.TempDir()
	if _, err := Install(KindClaudeCode, repo, false); err != nil {
		t.Fatal(err)
	}
	if status := statusForKind(t, repo, KindClaudeCode); status.State != StateConfigured {
		t.Fatalf("fresh install = %+v", status)
	}
	wrapper := filepath.Join(repo, filepath.FromSlash(WrapperPath))
	body, err := os.ReadFile(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	outdated := strings.Replace(string(body), `echo "reconc hook wrapper: no executable Reconc binary found" >&2`, `exit 0`, 1)
	if outdated == string(body) {
		t.Fatal("fixture could not age the wrapper")
	}
	if err := os.WriteFile(wrapper, []byte(outdated), 0o755); err != nil {
		t.Fatal(err)
	}
	status := statusForKind(t, repo, KindClaudeCode)
	if status.State != StateDegraded || !strings.Contains(status.Detail, "differs from the current generator") {
		t.Fatalf("an outdated managed wrapper reported %s: %s", status.State, status.Detail)
	}
}
