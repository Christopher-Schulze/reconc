package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reconc.dev/reconc/internal/retention"
)

func TestPruneCommandUsesCoreRetention(t *testing.T) {
	repo := t.TempDir()
	resolvedRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	stateRoot := t.TempDir()
	t.Setenv(retention.StateRootEnv, stateRoot)
	t.Setenv("TMPDIR", t.TempDir())
	project := retention.ProjectDir(stateRoot, resolvedRepo)
	for index := 0; index < 40; index++ {
		path := filepath.Join(project, "sessions", fmt.Sprintf("session-%02d.json", index))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(time.Duration(index-80) * 24 * time.Hour)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	err = Run([]string{"prune", repo, "--json"}, "test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("prune command: %v stderr=%s", err, stderr.String())
	}
	var report retention.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v output=%s", err, stdout.String())
	}
	if !report.Ran || report.StateBytesAfter > report.StateByteBudget {
		t.Fatalf("unexpected prune report: %+v", report)
	}
	entries, err := os.ReadDir(filepath.Join(project, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) > retention.DefaultPolicy().Sessions.MaxFiles {
		t.Fatalf("CLI did not enforce session count: %d", len(entries))
	}
}
