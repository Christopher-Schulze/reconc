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

func TestAuditUnreferencedTaskFilesNamesNonTaskFileCause(t *testing.T) {
	cases := []struct {
		name string
		file string
	}{
		{name: "session export", file: "session-export-TASK-2425-backend-forensic-reality-check.md"},
		{name: "lowercase note", file: "scratch-notes.md"},
		{name: "wrong id width", file: "TASK-999-Too-Short.md"},
		{name: "archived stray", file: filepath.Join("done", "handover.md")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "docs", "tasks", tc.file)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("create task directory: %v", err)
			}
			if err := os.WriteFile(path, []byte("stray content\n"), 0o644); err != nil {
				t.Fatalf("write stray file: %v", err)
			}

			failures := auditUnreferencedTaskFiles(root, map[string]bool{})
			if len(failures) != 1 {
				t.Fatalf("failures=%v, want one", failures)
			}
			got := failures[0]
			if !strings.Contains(got, "is not a TASK detail file") {
				t.Fatalf("failure does not name the real cause: %s", got)
			}
			for _, want := range []string{"Move the file outside docs/tasks/", "adding it to .gitignore does not clear it"} {
				if !strings.Contains(got, want) {
					t.Fatalf("failure missing repair information %q: %s", want, got)
				}
			}
			if strings.Contains(got, "utils/promote-task-done/run-promote-task-done") {
				t.Fatalf("failure prescribes the impossible archive command: %s", got)
			}
		})
	}
}

func TestAuditUnreferencedTaskFilesAcceptsReferencedDetail(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "docs", "tasks", "TASK-9999-Referenced.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create task directory: %v", err)
	}
	if err := os.WriteFile(path, []byte("# TASK-9999-Referenced\n"), 0o644); err != nil {
		t.Fatalf("write task: %v", err)
	}

	failures := auditUnreferencedTaskFiles(root, map[string]bool{"tasks/TASK-9999-Referenced.md": true})
	if len(failures) != 0 {
		t.Fatalf("failures=%v, want none", failures)
	}
}
