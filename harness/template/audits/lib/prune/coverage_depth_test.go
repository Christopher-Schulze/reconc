package prune

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveStateRootHonorsExplicitAndEnvironmentPrecedence(t *testing.T) {
	t.Setenv("RECONC_CLAUDE_STATE_DIR", "")
	t.Setenv("RECONC_HOME", "")
	explicit := filepath.Join(t.TempDir(), "explicit")
	if got, want := resolveStateRoot(explicit), filepath.Join(explicit, "sessions", "claude"); got != want {
		t.Fatalf("explicit resolveStateRoot() = %q, want %q", got, want)
	}

	stateDir := filepath.Join(t.TempDir(), "state")
	t.Setenv("RECONC_CLAUDE_STATE_DIR", stateDir)
	t.Setenv("RECONC_HOME", filepath.Join(t.TempDir(), "ignored-home"))
	if got := resolveStateRoot(""); got != stateDir {
		t.Fatalf("state-dir resolveStateRoot() = %q, want %q", got, stateDir)
	}

	t.Setenv("RECONC_CLAUDE_STATE_DIR", "")
	reconcHome := filepath.Join(t.TempDir(), "home")
	t.Setenv("RECONC_HOME", reconcHome)
	if got, want := resolveStateRoot(""), filepath.Join(reconcHome, "sessions", "claude"); got != want {
		t.Fatalf("home resolveStateRoot() = %q, want %q", got, want)
	}
}

func TestPruneDirNegativeRetentionDeletesEveryRegularFileAndIgnoresDirs(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.json", "b.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	report := Report{}
	kept, deleted := pruneDir(dir, -1, false, &report)
	if kept != 0 || deleted != 2 || len(report.Errors) != 0 {
		t.Fatalf("pruneDir() = kept %d deleted %d report %+v", kept, deleted, report)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read pruned directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "nested" || !entries[0].IsDir() {
		t.Fatalf("remaining entries = %#v", entries)
	}
}

func TestPruneDirAndLoadPolicyReportNonDirectoryReadFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain-file")
	if err := os.WriteFile(path, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	report := Report{}
	kept, deleted := pruneDir(path, 1, false, &report)
	if kept != 0 || deleted != 0 || len(report.Errors) != 1 || !strings.Contains(report.Errors[0], "read ") {
		t.Fatalf("pruneDir file result = kept %d deleted %d report %+v", kept, deleted, report)
	}

	directoryPolicy := filepath.Join(t.TempDir(), "policy")
	if err := os.Mkdir(directoryPolicy, 0o755); err != nil {
		t.Fatalf("mkdir policy fixture: %v", err)
	}
	policy, err := LoadPolicy(directoryPolicy)
	if err == nil || !strings.Contains(err.Error(), "read ") {
		t.Fatalf("LoadPolicy(directory) = (%+v, %v)", policy, err)
	}
	if policy != DefaultPolicy() {
		t.Fatalf("LoadPolicy read failure must return defaults, got %+v", policy)
	}
}
