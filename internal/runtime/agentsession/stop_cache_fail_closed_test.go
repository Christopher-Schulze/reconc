package agentsession

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/runtime"
)

func TestLatestReportReadAndWriteAreBounded(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	const sessionID = "bounded-report"
	report := &runtime.CheckReport{RepoRoot: root, Summary: strings.Repeat("x", maxStopReportBytes)}
	if err := writeLatestReport(root, sessionID, report); err == nil {
		t.Fatal("oversized report write must fail closed")
	}

	path := sessionReportPath(root, sessionID)
	if err := ensurePrivateStateDir(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxStopReportBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readLatestReport(root, sessionID); err == nil {
		t.Fatal("oversized report read must fail closed")
	}
}
