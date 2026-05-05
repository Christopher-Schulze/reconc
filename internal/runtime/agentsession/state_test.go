package agentsession

import (
	"os"
	"path/filepath"
	"testing"
)

func withStateRoot(t *testing.T) (string, string) {
	t.Helper()
	stateDir := t.TempDir()
	t.Setenv(StateRootEnv, stateDir)
	repo := t.TempDir()
	return stateDir, repo
}

func TestResolveRepoRootRejectsNonExistent(t *testing.T) {
	_, err := ResolveRepoRoot("/does/not/exist/anywhere")
	if err == nil {
		t.Fatal("expected error for non-existent path")
	}
}

func TestResolveRepoRootRejectsFile(t *testing.T) {
	f, err := os.CreateTemp("", "reconc-state-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.Close()
	_, err = ResolveRepoRoot(f.Name())
	if err == nil {
		t.Fatal("expected error for file path (not a dir)")
	}
}

func TestResolveRepoRootCanonicalisesSymlink(t *testing.T) {
	// On macOS /tmp is a symlink to /private/tmp. Creating the temp
	// repo there and resolving should yield the /private/tmp form.
	repo := t.TempDir()
	resolved, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	realResolved, _ := filepath.EvalSymlinks(repo)
	if resolved != realResolved {
		t.Errorf("expected %q, got %q", realResolved, resolved)
	}
}

func TestInitializeSessionStateCreatesEmptyState(t *testing.T) {
	_, repo := withStateRoot(t)
	state, err := InitializeSessionState(repo, "sess-001")
	if err != nil {
		t.Fatalf("InitializeSessionState: %v", err)
	}
	if state.SessionID != "sess-001" {
		t.Errorf("expected sess-001, got %s", state.SessionID)
	}
	if len(state.WritePaths) != 0 || len(state.ReadPaths) != 0 {
		t.Errorf("fresh state should be empty, got %+v", state)
	}
	// State file must now exist.
	path := sessionStatePath(state.RepoRoot, "sess-001")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("state file missing after init: %v", err)
	}
}

func TestLoadSessionStateRoundTrip(t *testing.T) {
	_, repo := withStateRoot(t)
	initial, err := InitializeSessionState(repo, "s1")
	if err != nil {
		t.Fatal(err)
	}
	initial = AppendReadPath(initial, "docs/x.md")
	initial = AppendWritePath(initial, "src/a.go")
	initial = AppendCommand(initial, "go test ./...")
	initial = AppendClaim(initial, "ci-green")
	if err := SaveSessionState(initial); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadSessionState(repo, "s1")
	if err != nil {
		t.Fatalf("LoadSessionState: %v", err)
	}
	if len(loaded.WritePaths) != 1 || loaded.WritePaths[0] != "src/a.go" {
		t.Errorf("WritePaths roundtrip failed: %v", loaded.WritePaths)
	}
	if len(loaded.Claims) != 1 || loaded.Claims[0] != "ci-green" {
		t.Errorf("Claims roundtrip failed: %v", loaded.Claims)
	}
}

func TestLoadSessionStateMissingReturnsEmpty(t *testing.T) {
	_, repo := withStateRoot(t)
	state, err := LoadSessionState(repo, "never-existed")
	if err != nil {
		t.Fatal(err)
	}
	if state.SessionID != "never-existed" {
		t.Errorf("expected echoed session_id, got %s", state.SessionID)
	}
	if len(state.WritePaths) != 0 {
		t.Errorf("empty-state should have no writes, got %v", state.WritePaths)
	}
}

func TestLoadSessionStateRejectsMalformedJSON(t *testing.T) {
	_, repo := withStateRoot(t)
	root, _ := ResolveRepoRoot(repo)
	path := sessionStatePath(root, "bad")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadSessionState(repo, "bad")
	if err == nil {
		t.Fatal("expected error for malformed state file")
	}
}

func TestAppendUniqueDeduplicates(t *testing.T) {
	state := emptyState("/x", "s1")
	state = AppendReadPath(state, "a.go")
	state = AppendReadPath(state, "a.go")
	state = AppendReadPath(state, "b.go")
	state = AppendReadPath(state, "  a.go  ") // whitespace trim
	if len(state.ReadPaths) != 2 {
		t.Errorf("expected 2 unique paths, got %v", state.ReadPaths)
	}
}

func TestAppendEmptyStringIgnored(t *testing.T) {
	state := emptyState("/x", "s1")
	state = AppendReadPath(state, "")
	state = AppendReadPath(state, "   ")
	if len(state.ReadPaths) != 0 {
		t.Errorf("empty/whitespace appends should be ignored, got %v", state.ReadPaths)
	}
}

func TestActiveSessionPointerTracksLatest(t *testing.T) {
	_, repo := withStateRoot(t)
	_, _ = InitializeSessionState(repo, "sess-A")
	_, _ = InitializeSessionState(repo, "sess-B")
	active, err := ResolveActiveSessionID(repo)
	if err != nil {
		t.Fatal(err)
	}
	if active != "sess-B" {
		t.Errorf("expected sess-B as active, got %s", active)
	}
}

func TestCleanupSessionStateRemovesFileAndPointer(t *testing.T) {
	_, repo := withStateRoot(t)
	_, _ = InitializeSessionState(repo, "sess-A")
	if err := CleanupSessionState(repo, "sess-A"); err != nil {
		t.Fatal(err)
	}
	root, _ := ResolveRepoRoot(repo)
	if _, err := os.Stat(sessionStatePath(root, "sess-A")); !os.IsNotExist(err) {
		t.Errorf("state file should be gone, stat err: %v", err)
	}
	if _, err := os.Stat(activeSessionPath(root)); !os.IsNotExist(err) {
		t.Errorf("active pointer should be gone, stat err: %v", err)
	}
}

func TestSanitiseIDScrubsUnsafeChars(t *testing.T) {
	cases := map[string]string{
		"uuid-1234": "uuid-1234",
		"../escape": "___escape",
		"a/b\\c":    "a_b_c",
		"":          "unknown",
		"a b":       "a_b",
	}
	for in, want := range cases {
		if got := sanitiseID(in); got != want {
			t.Errorf("sanitiseID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProjectKeyDeterministic(t *testing.T) {
	k1 := projectKey("/repo/a")
	k2 := projectKey("/repo/a")
	k3 := projectKey("/repo/b")
	if k1 != k2 {
		t.Error("same path must hash identically")
	}
	if k1 == k3 {
		t.Error("different paths must hash differently")
	}
	if len(k1) != 16 {
		t.Errorf("project key should be 16 chars, got %d", len(k1))
	}
}
