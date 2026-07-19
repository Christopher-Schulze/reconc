package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditUnreferencedTaskFilesPointsToRootLauncher(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "docs", "tasks", "TASK-9999-Unreferenced.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create task directory: %v", err)
	}
	if err := os.WriteFile(path, []byte("# TASK-9999-Unreferenced\n"), 0o644); err != nil {
		t.Fatalf("write task: %v", err)
	}

	failures := auditUnreferencedTaskFiles(root, map[string]bool{})
	if len(failures) != 1 {
		t.Fatalf("failures=%v, want one", failures)
	}
	const launcher = "tools/reconc/harness/template/utils/promote-task-done/run-promote-task-done"
	if !strings.Contains(failures[0], launcher) {
		t.Fatalf("remediation missing root launcher: %s", failures[0])
	}
	if strings.Contains(failures[0], "go run ./tools/reconc/harness/template/utils/promote-task-done") {
		t.Fatalf("remediation retained broken nested-module command: %s", failures[0])
	}
}
