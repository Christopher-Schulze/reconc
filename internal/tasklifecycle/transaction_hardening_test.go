package tasklifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/filelock"
)

func TestDoneVisiblePresenceIsValidated(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want int
		err  string
	}{
		{name: "absent", yaml: "task_lifecycle:\n  profile: sections-v1\n", want: defaultDoneVisible},
		{name: "explicit zero", yaml: "task_lifecycle:\n  profile: sections-v1\n  done_visible: 0\n", err: "between 1 and 1000"},
		{name: "negative", yaml: "task_lifecycle:\n  profile: sections-v1\n  done_visible: -1\n", err: "between 1 and 1000"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(test.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(repo)
			if test.err != "" {
				if err == nil || !strings.Contains(err.Error(), test.err) {
					t.Fatalf("LoadConfig error = %v, want %q", err, test.err)
				}
				return
			}
			if err != nil || cfg.DoneVisible != test.want {
				t.Fatalf("LoadConfig = %+v, %v; want done_visible=%d", cfg, err, test.want)
			}
		})
	}
}

func TestTaskSnapshotRejectsEveryReferencedFileMutation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string, string)
	}{
		{
			name: "content",
			mutate: func(t *testing.T, path, _ string) {
				t.Helper()
				if err := os.WriteFile(path, []byte("changed"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "missing",
			mutate: func(t *testing.T, path, _ string) {
				t.Helper()
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "replaced with identical bytes",
			mutate: func(t *testing.T, path, body string) {
				t.Helper()
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink replacement",
			mutate: func(t *testing.T, path, body string) {
				t.Helper()
				external := filepath.Join(t.TempDir(), "external.md")
				if err := os.WriteFile(external, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, path); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			overviewPath := filepath.Join(repo, "docs", "tasks.md")
			detailPath := filepath.Join(repo, "docs", "tasks", "001-current.md")
			detailBody := "# TASK 001: Current\n"
			writeFile(t, repo, "docs/tasks.md", []byte("# Tasks\n"))
			writeFile(t, repo, "docs/tasks/001-current.md", []byte(detailBody))
			overview, err := captureTaskFileSnapshot(overviewPath, true)
			if err != nil {
				t.Fatal(err)
			}
			detail, err := captureTaskFileSnapshot(detailPath, true)
			if err != nil {
				t.Fatal(err)
			}
			pathGuard := newTaskPathGuard(repo, 8)
			if err := pathGuard.reject(overviewPath); err != nil {
				t.Fatal(err)
			}
			if err := pathGuard.reject(detailPath); err != nil {
				t.Fatal(err)
			}
			test.mutate(t, detailPath, detailBody)
			issues := concurrentReadIssues(repo, defaultConfig(), []taskFileSnapshot{overview, detail}, pathGuard)
			if len(issues) != 1 || issues[0].ID != "task/read/concurrent-mutation" {
				t.Fatalf("mutation issues = %+v", issues)
			}
		})
	}
}

func TestTaskSnapshotRejectsTransactionAppearingAfterRead(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "docs/tasks.md", []byte("# Tasks\n"))
	overview, err := captureTaskFileSnapshot(filepath.Join(repo, "docs", "tasks.md"), true)
	if err != nil {
		t.Fatal(err)
	}
	pathGuard := newTaskPathGuard(repo, 4)
	if err := pathGuard.reject(filepath.Join(repo, "docs", "tasks.md")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, transactionRel, []byte("{}"))
	issues := concurrentReadIssues(repo, defaultConfig(), []taskFileSnapshot{overview}, pathGuard)
	if len(issues) != 1 || issues[0].ID != "task/read/concurrent-mutation" {
		t.Fatalf("transaction mutation issues = %+v", issues)
	}
}

func TestRollbackPathResolutionFailsBeforeMutation(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "docs/tasks.md", []byte("after\n"))
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "unsafe.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	journal := transaction{
		FormatVersion: transactionVersion,
		Action:        "test",
		Phase:         transactionPhasePrepared,
		Files: []transactionFile{
			{Path: "docs/tasks.md", Before: []byte("before\n"), BeforeMode: 0o644, BeforeHash: hashContent([]byte("before\n")), AfterHash: hashContent([]byte("after\n"))},
			{Path: "unsafe.md", Before: []byte("outside\n"), BeforeMode: 0o644, BeforeHash: hashContent([]byte("outside\n")), AfterHash: hashContent([]byte("outside\n"))},
		},
	}
	if err := rollbackTransaction(repo, journal); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("unsafe rollback error = %v", err)
	}
	body, err := os.ReadFile(filepath.Join(repo, "docs", "tasks.md"))
	if err != nil || string(body) != "after\n" {
		t.Fatalf("failed preflight mutated safe path: body=%q err=%v", body, err)
	}
	if body, err := os.ReadFile(outside); err != nil || string(body) != "outside\n" {
		t.Fatalf("failed preflight mutated symlink target: body=%q err=%v", body, err)
	}
	move := transactionMove{Source: "unsafe.md", Destination: "docs/tasks/done/unsafe.md"}
	if err := rollbackTransactionMove(repo, move, journal.Files[1]); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("rollback move ignored unsafe source path: %v", err)
	}
}

func TestTransactionPresenceUsesStrictRegularFileAdmission(t *testing.T) {
	for _, test := range []struct {
		name string
		make func(*testing.T, string)
	}{
		{name: "directory", make: func(t *testing.T, path string) {
			t.Helper()
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "oversized", make: func(t *testing.T, path string) {
			t.Helper()
			file, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := file.Truncate(maxTransactionBytes + 1); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "dangling symlink", make: func(t *testing.T, path string) {
			t.Helper()
			if err := os.Symlink(path+".missing", path); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			if err := os.MkdirAll(filepath.Join(repo, ".reconc"), 0o755); err != nil {
				t.Fatal(err)
			}
			test.make(t, filepath.Join(repo, filepath.FromSlash(transactionRel)))
			if exists, err := transactionExists(repo); err == nil || exists {
				t.Fatalf("strict journal admission = exists=%v err=%v", exists, err)
			}
		})
	}
}

func TestTransactionRollbackRemovesOnlyOwnedNestedParents(t *testing.T) {
	for _, withUserFile := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "user file"}[withUserFile], func(t *testing.T) {
			repo := t.TempDir()
			writeFile(t, repo, "docs/tasks/001-source.md", []byte("detail\n"))
			move := moveMutation{Source: "docs/tasks/001-source.md", Destination: "archive/deep/001-source.md"}
			journal, err := buildTransaction(repo, "test", nil, []moveMutation{move})
			if err != nil {
				t.Fatal(err)
			}
			if len(journal.CreatedDirectories) != 2 {
				t.Fatalf("created directories = %+v", journal.CreatedDirectories)
			}
			if err := publishTransaction(repo, journal, nil, []moveMutation{move}); err != nil {
				t.Fatal(err)
			}
			userPath := filepath.Join(repo, "archive", "deep", "user.txt")
			if withUserFile {
				if err := os.WriteFile(userPath, []byte("user\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			err = rollbackTransaction(repo, journal)
			if withUserFile {
				if err == nil || !strings.Contains(err.Error(), "preserved non-empty") {
					t.Fatalf("non-empty rollback error = %v", err)
				}
				if body, readErr := os.ReadFile(userPath); readErr != nil || string(body) != "user\n" {
					t.Fatalf("user file was not preserved: %q, %v", body, readErr)
				}
			} else if _, statErr := os.Lstat(filepath.Join(repo, "archive")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("owned empty parents remain: %v", statErr)
			}
			if body, readErr := os.ReadFile(filepath.Join(repo, filepath.FromSlash(move.Source))); readErr != nil || string(body) != "detail\n" {
				t.Fatalf("rollback did not restore source: %q, %v", body, readErr)
			}
		})
	}
}

func TestTransactionPublicationPreservesConcurrentlyCreatedParent(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "docs/tasks/001-source.md", []byte("detail\n"))
	move := moveMutation{Source: "docs/tasks/001-source.md", Destination: "archive/001-source.md"}
	journal, err := buildTransaction(repo, "test", nil, []moveMutation{move})
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(repo, "archive")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := publishTransaction(repo, journal, nil, []moveMutation{move}); err == nil || !strings.Contains(err.Error(), "directory precondition") {
		t.Fatalf("concurrent parent was not refused: %v", err)
	}
	if info, err := os.Lstat(parent); err != nil || !info.IsDir() {
		t.Fatalf("concurrent parent was removed: %v, %v", info, err)
	}
}

func TestCommittedTransactionRecoveryFinalizesWithoutRollback(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "docs/tasks/001-source.md", []byte("detail\n"))
	move := moveMutation{Source: "docs/tasks/001-source.md", Destination: "archive/deep/001-source.md"}
	journal, err := buildTransaction(repo, "test", nil, []moveMutation{move})
	if err != nil {
		t.Fatal(err)
	}
	if err := publishTransaction(repo, journal, nil, []moveMutation{move}); err != nil {
		t.Fatal(err)
	}
	journal.Phase = transactionPhaseCommitted
	body, err := encodeTransaction(journal)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, transactionRel, body)
	marker, err := transactionDirectoryMarkerPath(repo, journal.CreatedDirectories[len(journal.CreatedDirectories)-1])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	recovered, err := RecoverIfNeeded(repo)
	if err != nil || !recovered {
		t.Fatalf("committed recovery = %v, %v", recovered, err)
	}
	if body, readErr := os.ReadFile(filepath.Join(repo, filepath.FromSlash(move.Destination))); readErr != nil || string(body) != "detail\n" {
		t.Fatalf("committed recovery rolled back destination: %q, %v", body, readErr)
	}
	if _, statErr := os.Lstat(filepath.Join(repo, filepath.FromSlash(transactionRel))); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("committed recovery retained journal: %v", statErr)
	}
}

func TestPreparedDirectoryOnlyCrashRecoveryRemovesOwnedParents(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "docs/tasks/001-source.md", []byte("detail\n"))
	move := moveMutation{Source: "docs/tasks/001-source.md", Destination: "archive/deep/001-source.md"}
	journal, err := buildTransaction(repo, "test", nil, []moveMutation{move})
	if err != nil {
		t.Fatal(err)
	}
	body, err := encodeTransaction(journal)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, transactionRel, body)
	if err := prepareTransactionDirectories(repo, journal.CreatedDirectories); err != nil {
		t.Fatal(err)
	}
	if err := Recover(repo); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(repo, "archive")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("prepared-only recovery retained directories: %v", err)
	}
}

func TestApplyTransactionCommitsNestedMoveWithoutMarkerResidue(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "docs/tasks/001-source.md", []byte("detail\n"))
	move := moveMutation{Source: "docs/tasks/001-source.md", Destination: "archive/deep/001-source.md"}
	if err := applyTransaction(repo, "test", nil, []moveMutation{move}); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(repo, filepath.FromSlash(move.Destination))
	if body, err := os.ReadFile(destination); err != nil || string(body) != "detail\n" {
		t.Fatalf("committed destination = %q, %v", body, err)
	}
	for _, directory := range []string{"archive", "archive/deep"} {
		marker := filepath.Join(repo, filepath.FromSlash(directory), transactionDirectoryMarker)
		if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("committed marker remains at %s: %v", marker, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(repo, filepath.FromSlash(transactionRel))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("committed journal remains: %v", err)
	}
}

func TestLegacyTransactionJournalRemainsRecoverable(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "docs/tasks.md", []byte("before\n"))
	files := []fileMutation{{Path: "docs/tasks.md", After: []byte("after\n")}}
	journal, err := buildTransaction(repo, "test", files, nil)
	if err != nil {
		t.Fatal(err)
	}
	journal.FormatVersion = legacyTransactionVersion
	journal.Phase = ""
	journal.CreatedDirectories = nil
	body, err := encodeTransaction(journal)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, transactionRel, body)
	if err := publishTransaction(repo, journal, files, nil); err != nil {
		t.Fatal(err)
	}
	if err := Recover(repo); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(filepath.Join(repo, "docs/tasks.md")); err != nil || string(body) != "before\n" {
		t.Fatalf("legacy recovery body = %q, %v", body, err)
	}
}

func TestMutationLockJoinsOperationUnlockAndCloseErrors(t *testing.T) {
	originalAcquire := acquireMutationLock
	originalClose := closeMutationLock
	t.Cleanup(func() {
		acquireMutationLock = originalAcquire
		closeMutationLock = originalClose
	})
	operationErr := errors.New("operation failure")
	unlockErr := errors.New("unlock failure")
	closeErr := errors.New("close failure")
	acquireMutationLock = func(ctx context.Context, file *os.File, timeout time.Duration) (func() error, error) {
		unlock, err := filelock.LockContext(ctx, file, timeout)
		if err != nil {
			return nil, err
		}
		return func() error { return errors.Join(unlock(), unlockErr) }, nil
	}
	closeMutationLock = func(file *os.File) error { return errors.Join(file.Close(), closeErr) }
	err := withMutationLock(t.TempDir(), func() error { return operationErr })
	for _, want := range []error{operationErr, unlockErr, closeErr} {
		if !errors.Is(err, want) {
			t.Fatalf("joined lock error %v does not contain %v", err, want)
		}
	}
	acquireErr := errors.New("acquire failure")
	acquireMutationLock = func(context.Context, *os.File, time.Duration) (func() error, error) {
		return nil, acquireErr
	}
	err = withMutationLock(t.TempDir(), func() error {
		t.Fatal("operation ran after failed lock acquisition")
		return nil
	})
	if !errors.Is(err, acquireErr) || !errors.Is(err, closeErr) {
		t.Fatalf("acquire/close error = %v", err)
	}
}

func TestSectionsDoneRowsMustBeNewestFirst(t *testing.T) {
	repo := sectionedRepo(t, "", []testTask{
		{id: "001", title: "Older", state: StateDone, subTasks: "- [x] Done"},
		{id: "003", title: "Newer", state: StateDone, subTasks: "- [x] Done"},
	})
	_, err := Inspect(repo)
	if err == nil || !strings.Contains(err.Error(), "descending TASK ID order") {
		t.Fatalf("out-of-order Done rows passed: %v", err)
	}
}

func TestTransactionDirectoryPlanningSupportsCurrentPlatformPaths(t *testing.T) {
	repo := t.TempDir()
	target := filepath.Join("nested", "deeper", "target.md")
	directories, err := planTransactionDirectories(repo, []string{filepath.ToSlash(target)})
	if err != nil || len(directories) != 2 {
		t.Fatalf("directory plan = %+v, %v", directories, err)
	}
	if runtime.GOOS == "windows" && (directories[0].Path != "nested" || directories[1].Path != "nested/deeper") {
		t.Fatalf("Windows directory paths are not canonical: %+v", directories)
	}
}
