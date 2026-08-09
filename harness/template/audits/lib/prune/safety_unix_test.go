//go:build !windows

package prune

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestLoadPolicyRejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prune-policy.yaml")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := LoadPolicy(path)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("LoadPolicy accepted FIFO")
		}
	case <-time.After(time.Second):
		t.Fatal("LoadPolicy blocked on FIFO")
	}
}

func TestPruneDirRejectsSymlinkWithoutDeletingTarget(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	target := filepath.Join(external, "keep.json")
	if err := os.WriteFile(target, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "sessions")
	if err := os.Symlink(external, linked); err != nil {
		t.Fatal(err)
	}
	report := Report{}
	_, deleted := pruneDir(linked, 0, false, &report)
	if deleted != 0 {
		t.Fatalf("deleted through symlink: %d", deleted)
	}
	if body, err := os.ReadFile(target); err != nil || string(body) != "keep\n" {
		t.Fatalf("external target changed: body=%q err=%v", body, err)
	}
}

func TestRunRejectsSymlinkedProjectStateWithoutDeletingTarget(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	external := t.TempDir()
	sessions := filepath.Join(external, "sessions")
	if err := os.Mkdir(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"old.json", "new.json"} {
		if err := os.WriteFile(filepath.Join(sessions, name), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	projects := filepath.Join(home, "sessions", "claude", "projects")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(projects, projectKey(repo))); err != nil {
		t.Fatal(err)
	}
	policy := DefaultPolicy()
	policy.SessionsRetention = 1
	report := Run(Options{RepoRoot: repo, ReconcHome: home, Policy: policy})
	if report.SessionsDeleted != 0 {
		t.Fatalf("deleted through project-state symlink: %d", report.SessionsDeleted)
	}
	for _, name := range []string{"old.json", "new.json"} {
		if _, err := os.Stat(filepath.Join(sessions, name)); err != nil {
			t.Fatalf("external session %s changed: %v", name, err)
		}
	}
}

func TestTrimJSONLRejectsSymlinkWithoutReplacingTarget(t *testing.T) {
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "audit.jsonl")
	if err := os.WriteFile(external, []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "audit.jsonl")
	if err := os.Symlink(external, linked); err != nil {
		t.Fatal(err)
	}
	report := Report{}
	dropped, freed := trimJsonl(linked, 1, 1, false, &report)
	if dropped != 0 || freed != 0 {
		t.Fatalf("trimmed symlinked log: dropped=%d freed=%d", dropped, freed)
	}
	if body, err := os.ReadFile(external); err != nil || string(body) != "one\ntwo\n" {
		t.Fatalf("external log changed: body=%q err=%v", body, err)
	}
}
