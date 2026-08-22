package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/audit"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/retention"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestAppendObservationErrorIsVisibleAndBounded(t *testing.T) {
	view := &View{}
	appendObservationError(view, "audit observation", errors.New(strings.Repeat("x", maxObservationErrorBytes*2)))
	if len(view.Errors) != 1 || !strings.HasPrefix(view.Errors[0], "audit observation: ") {
		t.Fatalf("observation errors = %#v", view.Errors)
	}
	if len(view.Errors[0]) > maxObservationErrorBytes {
		t.Fatalf("observation error has %d bytes, want <= %d", len(view.Errors[0]), maxObservationErrorBytes)
	}
}

func TestBuildSurfacesAuditObservationFailure(t *testing.T) {
	repo := observationErrorRepo(t)
	if err := os.WriteFile(filepath.Join(repo, audit.AuditFileRelative), []byte("{broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	view, err := Build(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !containsErrorPrefix(view.Errors, "audit observation: ") {
		t.Fatalf("TUI errors = %#v, want audit observation failure", view.Errors)
	}
}

func TestBuildSurfacesSessionObservationFailure(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv(agentsession.StateRootEnv, stateRoot)
	repo := observationErrorRepo(t)
	state, err := agentsession.InitializeSessionState(repo, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	activePath := filepath.Join(retention.ProjectDir(stateRoot, state.RepoRoot), "active-session.txt")
	if err := os.WriteFile(activePath, []byte("invalid\nsession\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	view, err := Build(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !containsErrorPrefix(view.Errors, "session observation: ") {
		t.Fatalf("TUI errors = %#v, want session observation failure", view.Errors)
	}
}

func observationErrorRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatal(err)
	}
	return repo
}

func containsErrorPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
