//go:build !windows

package agentsession

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStatusStateReadersRejectFIFOWithoutBlocking(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		path string
		read func() error
	}{
		{
			name: "hook liveness",
			path: hookLivenessPath(root),
			read: func() error {
				_, err := ReadHookLiveness(repo)
				return err
			},
		},
		{
			name: "MCP audit",
			path: mcpAuditPath(root),
			read: func() error {
				_, err := ReadMCPAudit(repo)
				return err
			},
		},
		{
			name: "session state",
			path: sessionStatePath(root, "fifo-session"),
			read: func() error {
				_, err := LoadSessionState(repo, "fifo-session")
				return err
			},
		},
		{
			name: "active session",
			path: activeSessionPath(root),
			read: func() error {
				_, err := ResolveActiveSessionID(repo)
				return err
			},
		},
		{
			name: "evidence taint",
			path: evidenceTaintPath(root),
			read: func() error {
				_, err := loadEvidenceTaint(root)
				return err
			},
		},
		{
			name: "evidence segment",
			path: evidenceSegmentPath(root, "fifo-session", 1),
			read: func() error {
				_, err := readEvidenceSegment(root, "fifo-session", 1)
				return err
			},
		},
		{
			name: "stop report",
			path: sessionReportPath(root, "fifo-session"),
			read: func() error {
				_, _, err := readLatestReport(root, "fifo-session")
				return err
			},
		},
		{
			name: "repository run state",
			path: repositoryRunStatePathResolved(root),
			read: func() error {
				_, err := loadRepositoryRunState(repo)
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.MkdirAll(filepath.Dir(test.path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(test.path, 0o600); err != nil {
				t.Skipf("FIFO creation unsupported on this host: %v", err)
			}
			result := make(chan error, 1)
			go func() { result <- test.read() }()
			select {
			case err := <-result:
				if err == nil || !strings.Contains(err.Error(), "regular file") {
					t.Fatalf("FIFO error = %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("status state reader blocked on a FIFO")
			}
		})
	}
}

func TestOptionalStateReadersRejectFIFOWithoutBlocking(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"session_id":"fifo-session","tool_use_id":"tool-1"}`)
	path := preDecisionCachePath(root, payload)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("FIFO creation unsupported on this host: %v", err)
	}
	result := make(chan bool, 1)
	go func() {
		_, ok := readPreDecisionCache(root, payload, "never-match")
		result <- ok
	}()
	select {
	case ok := <-result:
		if ok {
			t.Fatal("pre-decision cache accepted a FIFO")
		}
	case <-time.After(time.Second):
		t.Fatal("pre-decision cache reader blocked on a FIFO")
	}
}

func TestCompactionTaskReaderRejectsFIFOWithoutBlocking(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, "docs", "tasks.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("FIFO creation unsupported on this host: %v", err)
	}
	result := make(chan string, 1)
	go func() { result <- activeTaskLine(repo) }()
	select {
	case active := <-result:
		if active != "" {
			t.Fatalf("FIFO produced active task %q", active)
		}
	case <-time.After(time.Second):
		t.Fatal("compaction task reader blocked on a FIFO")
	}
}

func TestHookLivenessFastPathRejectsSymlinkMarker(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)
	if err := recordHookLivenessAt(root, "codex", "codex-stop", first); err != nil {
		t.Fatal(err)
	}
	marker := hookLivenessMarkerPath(root, "codex", "codex-stop")
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("untrusted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, marker); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if err := recordHookLivenessAt(root, "codex", "codex-stop", first.Add(time.Hour)); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("symlink marker error = %v", err)
	}
}
