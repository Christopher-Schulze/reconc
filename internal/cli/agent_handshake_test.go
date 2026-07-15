package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/contextsize"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestSessionBriefingJSONIncludesVersionedRunState(t *testing.T) {
	repo := writeAgentHandshakeTaskRepo(t)
	if _, err := agentsession.SetRepositoryRun(repo, true); err != nil {
		t.Fatalf("enable repository run: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run([]string{"session-briefing", repo, "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("session-briefing: %v", err)
	}
	var payload struct {
		FormatVersion string                           `json:"format_version"`
		Run           agentsession.RepositoryRunStatus `json:"run"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode session briefing: %v\n%s", err, stdout.String())
	}
	if payload.FormatVersion != agentBriefingFormatVersion {
		t.Fatalf("format_version=%q, want %q", payload.FormatVersion, agentBriefingFormatVersion)
	}
	if !payload.Run.Enabled || payload.Run.TaskDisposition != "continue" || payload.Run.TaskID != "001" {
		t.Fatalf("unexpected run status: %+v", payload.Run)
	}
}

func TestContextSizeDefaultsToCanonicalActiveContext(t *testing.T) {
	repo := writeAgentHandshakeTaskRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), bytes.Repeat([]byte("r"), 20_000), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "docs", "todo.md"), bytes.Repeat([]byte("t"), 20_000), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run([]string{"context", "size", repo, "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("context size: %v", err)
	}
	var report contextsize.ScanReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode context report: %v\n%s", err, stdout.String())
	}
	if report.FormatVersion != "1" {
		t.Fatalf("format_version=%q", report.FormatVersion)
	}
	paths := make(map[string]bool, len(report.Files))
	for _, file := range report.Files {
		paths[file.Path] = true
	}
	for _, want := range []string{"AGENTS.md", "CLAUDE.md", "start.md", "docs/tasks.md", "docs/tasks/001-handshake.md"} {
		if !paths[want] {
			t.Errorf("canonical context missing %s: %+v", want, report.Files)
		}
	}
	for _, unwanted := range []string{"README.md", "docs/todo.md"} {
		if paths[unwanted] {
			t.Errorf("optional context %s was counted by default", unwanted)
		}
	}
}

func TestContextSizeRejectsRepositoryEscape(t *testing.T) {
	repo := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := Run([]string{"context", "size", repo, "--files", "../outside.md"}, "test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected repository escape to fail")
	}
}

func writeAgentHandshakeTaskRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "docs", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"AGENTS.md":                   "# Agent rules\n",
		"start.md":                    "# START\n",
		"docs/tasks.md":               "# TASK Control Plane\n\n## Active\n\n- [~] 001 Handshake -> tasks/001-handshake.md\n\n## Queue\n\n## Blocked\n\n## Done\n",
		"docs/tasks/001-handshake.md": "# TASK 001: Handshake\n\n## Why\n\nReason.\n\n## Acceptance\n\n- Works.\n\n## Sub-Tasks\n\n- [~] Work\n\n## Notes\n\nNone.\n\n## Deviations\n\nNone.\n",
	}
	for relative, body := range files {
		if err := os.WriteFile(filepath.Join(repo, filepath.FromSlash(relative)), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", relative, err)
		}
	}
	return repo
}
