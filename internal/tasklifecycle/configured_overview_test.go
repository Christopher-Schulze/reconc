package tasklifecycle

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestExplicitTaskLifecycleFailsClosedWhenOverviewMissing(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte("task_lifecycle:\n  profile: sections-v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := InspectRunState(repo)
	var validation *ValidationError
	if !errors.As(err, &validation) || len(validation.Issues) != 1 || validation.Issues[0].ID != "task/overview/missing" {
		t.Fatalf("expected configured overview validation error, got %v", err)
	}
}

func TestUnconfiguredRepositoryMayHaveNoTaskOverview(t *testing.T) {
	state, err := InspectRunState(t.TempDir())
	if err != nil || state.Disposition != RunAbsent {
		t.Fatalf("unconfigured repository must remain absent: state=%+v err=%v", state, err)
	}
}

func TestCompletionRequireCommittedConfig(t *testing.T) {
	repo := t.TempDir()
	body := "task_lifecycle:\n  profile: sections-v1\n  completion:\n    require_committed: true\n"
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "docs", "tasks.md"), []byte("# TASK Control Plane\n\n## Active\n\n## Queue\n\n## Blocked\n\n## Done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(repo)
	if err != nil || !cfg.Configured || !cfg.Completion.RequireCommitted {
		t.Fatalf("completion config not loaded: cfg=%+v err=%v", cfg, err)
	}
}
