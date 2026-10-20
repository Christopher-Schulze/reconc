package tasklifecycle

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// One complete TASK transition (active detail archived, overview rewritten
// atomically) lands between the reader's path probe and its read. A fresh read
// sees a valid board, so the bounded retry must succeed in both read paths.
func TestTransientArchiveDuringReadIsRetried(t *testing.T) {
	for _, mode := range []string{"fast-run-state", "full-inspect"} {
		t.Run(mode, func(t *testing.T) {
			repo := writeParityBoard(t, "", nil)
			previous := lstatTaskPathComponent
			t.Cleanup(func() { lstatTaskPathComponent = previous })
			fired := false
			lstatTaskPathComponent = func(path string) (os.FileInfo, error) {
				info, err := previous(path)
				if !fired && strings.HasSuffix(path, filepath.FromSlash("/docs/tasks/001-current.md")) {
					fired = true
					next := "# Tasks\n\n## Active\n\n## Queue\n\n## Blocked\n\n## Done\n\n- [x] 001 Current -> tasks/done/001-current.md\n"
					tmp := filepath.Join(repo, "docs", ".tasks.md.tmp")
					if err := os.WriteFile(tmp, []byte(next), 0o644); err != nil {
						t.Fatal(err)
					}
					if err := os.MkdirAll(filepath.Join(repo, "docs/tasks/done"), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(filepath.Join(repo, "docs/tasks/001-current.md"), filepath.Join(repo, "docs/tasks/done/001-current.md")); err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(tmp, filepath.Join(repo, "docs/tasks.md")); err != nil {
						t.Fatal(err)
					}
				}
				return info, err
			}
			var err error
			if mode == "fast-run-state" {
				_, err = InspectRunState(repo)
			} else {
				_, err = Inspect(repo)
			}
			if !fired || err != nil {
				t.Fatalf("one transient transition was not retried: fired=%t err=%v", fired, err)
			}
		})
	}
}

// A journal held by a live mutation is waited for; an abandoned one still
// reports pending recovery.
func TestReadWaitsForALiveTaskMutation(t *testing.T) {
	repo := writeParityBoard(t, "", nil)
	journal := filepath.Join(repo, filepath.FromSlash(transactionRel))
	started, finished := make(chan struct{}), make(chan error, 1)
	go func() {
		finished <- withMutationLock(repo, func() error {
			if err := os.WriteFile(journal, []byte("{}"), 0o600); err != nil {
				close(started)
				return err
			}
			close(started)
			time.Sleep(300 * time.Millisecond)
			return os.Remove(journal)
		})
	}()
	<-started
	if _, err := Inspect(repo); err != nil {
		t.Fatalf("a read during a live mutation failed: %v", err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journal, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Inspect(repo)
	var validation *ValidationError
	if !errors.As(err, &validation) || validation.Issues[0].ID != "task/transaction/pending" {
		t.Fatalf("an abandoned journal was not reported as pending: %v", err)
	}
}
